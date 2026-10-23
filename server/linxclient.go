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
	playing    bool // 跑酷进行中：无语音需求，停上行，TTS 收尾后主动断会话（按会话时长计费）
	wake       chan struct{}
	OnSTT      func(text string)
	OnTTSFrame func(opus []byte)
	OnVoiceState func(listening bool)
	OnTTSState func(playing bool) // 平台侧 TTS 起/止；真正"播完"由浏览器结合本地播放队列判定
}

func NewLinxClient(mac string) *LinxClient {
	return &LinxClient{Mac: mac, wake: make(chan struct{}, 1)}
}

const otaURL = "https://xrobo.qiniuapi.com/v1/ota/"

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

// Run 阻塞维持会话。playing 期间主动断开并 hold（不重连——会话按秒计费，
// 跑酷中没有语音需求）；回 idle 经 wake 立即重连；异常断线 5s 退避重连。
func (c *LinxClient) Run(ctx context.Context) {
	for {
		if err := c.connect(ctx); err != nil {
			log.Printf("linx: %v", err)
		}
		c.mu.Lock()
		playing := c.playing
		c.mu.Unlock()
		if playing {
			log.Printf("linx: holding off (playing), waiting for idle")
			select {
			case <-ctx.Done():
				return
			case <-c.wake:
			}
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-c.wake:
		case <-time.After(5 * time.Second):
		}
	}
}

// SetPlaying 切换游戏阶段。playing：停上行并断会话（TTS 在播则等播完由 tts 状态机断）；
// idle：连接还在就补发 listen 恢复监听，已断则经 wake 立即重连。
func (c *LinxClient) SetPlaying(playing bool) {
	c.mu.Lock()
	c.playing = playing
	conn := c.conn
	muted := c.muted
	c.mu.Unlock()

	if playing {
		c.setListening(false)
		if conn != nil && !muted {
			log.Printf("linx: playing, closing session immediately")
			_ = conn.Close(websocket.StatusNormalClosure, "playing")
		}
		return
	}

	select {
	case c.wake <- struct{}{}:
	default:
	}
	if conn != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = c.sendListenStart(ctx, conn)
		c.setListening(true)
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
					if c.OnTTSState != nil {
						c.OnTTSState(true)
					}
				case "stop":
					c.setMuted(false)
					if c.OnTTSState != nil {
						c.OnTTSState(false)
					}
					c.mu.Lock()
					playing := c.playing
					c.mu.Unlock()
					if playing {
						// 跑酷中无语音需求：倒计时播完即断，会话按秒计费不空烧
						log.Printf("linx: playing, closing session to save usage")
						return nil
					}
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

// SendAudio 转发浏览器编码的 OPUS 帧。TTS 播放期间（防回音）与跑酷中（无语音需求）丢弃。
func (c *LinxClient) SendAudio(ctx context.Context, opus []byte) error {
	c.mu.Lock()
	conn, muted := c.conn, c.muted
	playing := c.playing
	c.mu.Unlock()
	if conn == nil || muted || playing {
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
