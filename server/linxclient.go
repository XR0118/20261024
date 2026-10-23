package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// LinxClient 以设备身份直连灵矽云：OTA 换 token → WebSocket 会话。
// 协议参考 https://linx.qiniu.com/docs/xrobot/platform/websocket 与 linx_app 行为。
type LinxClient struct {
	Mac        string
	wsURL      string
	token      string
	conn       *websocket.Conn
	sessionID  string
	mu         sync.Mutex
	muted      bool // TTS 播放期间暂停上行，防止扬声器串回麦克风
	OnSTT      func(text string)
	OnTTSFrame func(opus []byte)
	OnVoiceState func(listening bool)
}

const otaURL = "https://xrobo.qiniuapi.com/v1/ota/"

func NewLinxClient(mac string) *LinxClient {
	return &LinxClient{Mac: mac}
}

type otaResponse struct {
	Websocket struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	} `json:"websocket"`
	Activation *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"activation"`
}

func (c *LinxClient) ota(ctx context.Context) error {
	// 与 linx_app 上报体保持一致，平台按 mac_address 匹配预注册设备
	body, _ := json.Marshal(map[string]any{
		"application":     map[string]any{"name": "Linx", "version": "1.6.0"},
		"board":           map[string]any{"type": "bread-compact-wifi", "ip": "127.0.0.1", "mac": c.Mac},
		"chip_model_name": "esp32s3",
		"flash_size":      16777216,
		"mac_address":     c.Mac,
		"ota":             map[string]any{"label": "factory"},
		"partition_table": []any{},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, otaURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Device-Id", c.Mac)
	req.Header.Set("Client-Id", c.Mac)
	req.Header.Set("Activation-Version", "1")
	req.Header.Set("User-Agent", "LoudLeap/0.1")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ota status %d", resp.StatusCode)
	}
	var ota otaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ota); err != nil {
		return err
	}
	if ota.Activation != nil {
		return fmt.Errorf("device unbound, activate with code %s (%s)", ota.Activation.Code, ota.Activation.Message)
	}
	if ota.Websocket.URL == "" || ota.Websocket.Token == "" {
		return fmt.Errorf("ota returned no websocket credentials")
	}
	c.wsURL, c.token = ota.Websocket.URL, ota.Websocket.Token
	return nil
}

// Run 阻塞维持会话，断线以 5s 退避重连（OTA 会换发新 token）。
func (c *LinxClient) Run(ctx context.Context) {
	for {
		if err := c.connect(ctx); err != nil {
			log.Printf("linx: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *LinxClient) connect(ctx context.Context) error {
	if err := c.ota(ctx); err != nil {
		return fmt.Errorf("ota: %w", err)
	}
	conn, _, err := websocket.Dial(ctx, c.wsURL, &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			"Authorization":   {"Bearer " + c.token},
			"Protocol-Version": {"1"},
			"Device-Id":       {c.Mac},
			"Client-Id":       {c.Mac},
		},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.Close(websocket.StatusInternalError, "bye")

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	log.Printf("linx: connected %s", c.wsURL)

	// 协议要求连接建立后立即上报 hello（音频参数），服务器据此初始化会话；
	// 漏发会被当空闲连接在 ~30s 后踢掉
	if err := wsjsonWrite(ctx, conn, map[string]any{
		"type":       "hello",
		"version":    1,
		"transport":  "websocket",
		"audio_params": map[string]any{
			"format":         "opus",
			"sample_rate":    16000,
			"channels":       1,
			"frame_duration": 60,
		},
	}); err != nil {
		return fmt.Errorf("hello: %w", err)
	}

	err = c.loop(ctx, conn)

	c.mu.Lock()
	c.conn = nil
	c.sessionID = ""
	c.mu.Unlock()
	return err
}

func (c *LinxClient) loop(ctx context.Context, conn *websocket.Conn) error {
	for {
		msgType, data, err := conn.Read(ctx)
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		switch msgType {
		case websocket.MessageBinary:
			if c.OnTTSFrame != nil {
				c.OnTTSFrame(data)
			}
		case websocket.MessageText:
			var msg struct {
				Type      string `json:"type"`
				SessionID string `json:"session_id"`
				State     string `json:"state"`
				Text      string `json:"text"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				log.Printf("linx: bad json %s", data)
				continue
			}
			switch msg.Type {
			case "hello":
				c.mu.Lock()
				c.sessionID = msg.SessionID
				c.mu.Unlock()
				if err := c.sendListenStart(ctx, conn); err != nil {
					return err
				}
				c.setListening(true)
			case "stt":
				if c.OnSTT != nil && msg.Text != "" {
					log.Printf("linx stt: %q", msg.Text)
					c.OnSTT(msg.Text)
				}
			case "tts":
				// 播放期静音上行（防回音），结束后重新监听，与 linx_app 行为一致
				switch msg.State {
				case "start":
					c.setMuted(true)
					c.setListening(false)
				case "stop":
					c.setMuted(false)
					if err := c.sendListenStart(ctx, conn); err != nil {
						return err
					}
					c.setListening(true)
				}
			case "goodbye":
				log.Printf("linx: session goodbye")
				c.setListening(false)
			}
		}
	}
}

func (c *LinxClient) sendListenStart(ctx context.Context, conn *websocket.Conn) error {
	c.mu.Lock()
	sessionID := c.sessionID
	c.mu.Unlock()
	return wsjsonWrite(ctx, conn, map[string]any{
		"session_id": sessionID,
		"type":       "listen",
		"state":      "start",
		"mode":       "auto",
	})
}

// SendAudio 转发浏览器编码的 OPUS 帧。TTS 播放期间丢弃（防回音）。
func (c *LinxClient) SendAudio(ctx context.Context, opus []byte) error {
	c.mu.Lock()
	conn, muted := c.conn, c.muted
	c.mu.Unlock()
	if conn == nil || muted {
		return nil
	}
	return conn.Write(ctx, websocket.MessageBinary, opus)
}

func (c *LinxClient) setMuted(v bool) {
	c.mu.Lock()
	c.muted = v
	c.mu.Unlock()
}

func (c *LinxClient) setListening(v bool) {
	if c.OnVoiceState != nil {
		c.OnVoiceState(v)
	}
}
