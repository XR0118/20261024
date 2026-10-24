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
	Mac          string
	wsURL        string
	token        string
	conn         *websocket.Conn
	sessionID    string
	mu           sync.Mutex
	muted        bool // TTS 播放期间暂停上行，防止扬声器串回麦克风
	playing      bool // 跑酷进行中：无语音需求，停上行，TTS 收尾后主动断会话（按会话时长计费）
	wake         chan struct{}
	speakCh      chan speakReq        // 台词队列：串行播放，防 detect 打断
	wmu          sync.Mutex           // conn 写互斥：三类写来源并存（音频转发/台词/监听控制）
	ttsWait      *ttsWaitState        // 当前台词的 TTS 完成信号（start→stop 成对）
	echoGuard    map[string]time.Time // detect 注入文本的回环防抖（见 guardEcho）
	OnSTT        func(text string)
	OnTTSFrame   func(opus []byte)
	OnVoiceState func(listening bool)
	OnTTSState   func(playing bool) // 平台侧 TTS 起/止；真正"播完"由浏览器结合本地播放队列判定
}

type speakReq struct {
	text  string
	exact bool
	after func() // 台词播完（或发送失败/超时）后回调，恰好一次——用于「先说完再推进」的时序
}

// ttsWaitState 台词的 TTS 完成判定：必须观察到 start 之后的 stop 才算播完——
// 防上一条迟到的 stop、或普通对话轮的 stop 误放行当前台词。
type ttsWaitState struct {
	started bool
	ch      chan struct{}
}

func NewLinxClient(mac string) *LinxClient {
	return &LinxClient{
		Mac:       mac,
		wake:      make(chan struct{}, 1),
		speakCh:   make(chan speakReq, 8),
		echoGuard: map[string]time.Time{},
	}
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
	go c.speakLoop(ctx)
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
			"Authorization":    {"Bearer " + c.token},
			"Protocol-Version": {"1"},
			"Device-Id":        {c.Mac},
			"Client-Id":        {c.Mac},
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
	// 漏发会被当空闲连接在 ~30s 后踢掉。conn 已对外可见，写须走 writeConn。
	hello, _ := json.Marshal(map[string]any{
		"type":      "hello",
		"version":   1,
		"transport": "websocket",
		"audio_params": map[string]any{
			"format":         "opus",
			"sample_rate":    16000,
			"channels":       1,
			"frame_duration": 60,
		},
	})
	if err := c.writeConn(ctx, conn, websocket.MessageText, hello); err != nil {
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
				// detect 注入的台词会被平台原样回显为 stt：不过滤，Boss 报的
				// 数字会被当成玩家的报数再次进引擎，自播死循环（实机踩过）
				if c.OnSTT != nil && msg.Text != "" && !c.isEcho(msg.Text) {
					log.Printf("linx stt: %q", msg.Text)
					c.OnSTT(msg.Text)
				}
			case "tts":
				// 播放期静音上行（防回音），结束后重新监听，与 linx_app 行为一致
				switch msg.State {
				case "start":
					c.setMuted(true)
					c.markTTSStart()
					c.setListening(false)
					if c.OnTTSState != nil {
						c.OnTTSState(true)
					}
				case "stop":
					c.setMuted(false)
					c.signalTTSDone()
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
	b, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"type":       "listen",
		"state":      "start",
		"mode":       "auto",
	})
	return c.writeConn(ctx, conn, websocket.MessageText, b)
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
	return c.writeConn(ctx, conn, websocket.MessageBinary, opus)
}

// writeConn 串行化所有对 conn 的写入——coder/websocket 只允许单一并发写者，
// 而本客户端有音频转发、台词 detect、监听控制三类并发写来源。
func (c *LinxClient) writeConn(ctx context.Context, conn *websocket.Conn, typ websocket.MessageType, data []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return conn.Write(ctx, typ, data)
}

// guardEcho 登记一句即将注入的台词：平台会把 detect 的文本原样回显为
// stt 消息，isEcho 据此丢弃回显，防止自己的台词重入引擎。
func (c *LinxClient) guardEcho(text string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	c.echoGuard[text] = now.Add(15 * time.Second)
	for k, exp := range c.echoGuard {
		if now.After(exp) {
			delete(c.echoGuard, k)
		}
	}
}

// isEcho 一次性命中：真正的玩家重复话语最多牺牲一次。
func (c *LinxClient) isEcho(text string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.echoGuard[text]; !ok {
		return false
	}
	delete(c.echoGuard, text)
	return true
}

// Speak 排队一句台词（非阻塞）；exact 为 true 时逐字直发而非交平台 LLM。
func (c *LinxClient) Speak(text string, exact bool) {
	c.enqueueSpeak(text, exact, nil)
}

// SpeakThen 排队一句台词并在其播完后回调——攻击事件押后到宣告说完再下发，
// 玩家听到「接我这招」的瞬间 QTE 窗口才开。
func (c *LinxClient) SpeakThen(text string, exact bool, after func()) {
	c.enqueueSpeak(text, exact, after)
}

func (c *LinxClient) enqueueSpeak(text string, exact bool, after func()) {
	select {
	case c.speakCh <- speakReq{text, exact, after}:
	default:
		log.Printf("linx: 台词队列已满，丢弃 %q", text)
		if after != nil {
			go after()
		}
	}
}

func (c *LinxClient) speakLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case req := <-c.speakCh:
			c.deliverSpeak(ctx, req)
		}
	}
}

// deliverSpeak 发一条 detect。必须串行：新 detect 会打断未播完的 TTS
// （spike 实测 tts stop is_aborted=true），发完等 tts stop 再放下一条。
// after 在所有出口都恰好触发一次——押后的游戏事件不能因台词丢失而永久卡住。
func (c *LinxClient) deliverSpeak(ctx context.Context, req speakReq) {
	defer func() {
		if req.after != nil {
			go req.after()
		}
	}()
	deadline := time.Now().Add(4 * time.Second) // Boss 进场后会话可能还在重连
	for {
		c.mu.Lock()
		conn, sid := c.conn, c.sessionID
		c.mu.Unlock()
		if conn != nil && sid != "" {
			c.guardEcho(req.text)
			c.armTTSDone()
			msg := map[string]any{
				"session_id": sid,
				"type":       "listen",
				"state":      "detect",
			}
			if req.exact {
				msg["text"] = "boss_event"
				msg["text_response"] = req.text
			} else {
				msg["text"] = req.text
			}
			b, _ := json.Marshal(msg)
			if err := c.writeConn(ctx, conn, websocket.MessageText, b); err != nil {
				log.Printf("linx speak: %v", err)
				return
			}
			c.waitTTSDone(8 * time.Second)
			return
		}
		if time.Now().After(deadline) {
			log.Printf("linx speak: 4s 无会话，丢弃 %q", req.text)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (c *LinxClient) armTTSDone() {
	c.mu.Lock()
	c.ttsWait = &ttsWaitState{ch: make(chan struct{})}
	c.mu.Unlock()
}

func (c *LinxClient) markTTSStart() {
	c.mu.Lock()
	if c.ttsWait != nil {
		c.ttsWait.started = true
	}
	c.mu.Unlock()
}

func (c *LinxClient) waitTTSDone(timeout time.Duration) {
	c.mu.Lock()
	w := c.ttsWait
	c.mu.Unlock()
	if w == nil {
		return
	}
	select {
	case <-w.ch:
	case <-time.After(timeout):
	}
}

func (c *LinxClient) signalTTSDone() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ttsWait != nil && c.ttsWait.started {
		select {
		case <-c.ttsWait.ch:
		default:
			close(c.ttsWait.ch)
		}
	}
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
