package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// 游戏网页上报的最新状态，供台词生成时取用（如结算点评报距离和摔死次数）。
type GameState struct {
	Playing bool  `json:"playing"`
	Dist    int   `json:"dist"`
	Deaths  int   `json:"deaths"`
	Won     bool  `json:"won"`
	Seq     int64 `json:"seq"`
}

type GameHub struct {
	mu    sync.RWMutex
	state GameState
	conn  *websocket.Conn
	brain *Brain
	linx  *LinxClient
	bconn *websocket.Conn // 浏览器主通道：音频上行/下行 + 游戏动作 + 状态
}

func NewGameHub(brain *Brain) *GameHub {
	return &GameHub{brain: brain}
}

func (h *GameHub) SetLinxClient(lc *LinxClient) {
	h.linx = lc
	lc.OnSTT = h.handleSTT
	lc.OnTTSFrame = h.pushTTSFrame
	lc.OnVoiceState = h.pushVoiceState
}

// stt 文本直达游戏动作（ADR-0002 主线：进程内，零外部依赖）。
func (h *GameHub) handleSTT(text string) {
	actions := gameActions(classify(text))
	if len(actions) == 0 {
		return
	}
	h.pushBrowserJSON(map[string]any{"type": "game-action", "actions": actions})
}

func (h *GameHub) pushTTSFrame(opus []byte) {
	h.mu.RLock()
	conn := h.bconn
	h.mu.RUnlock()
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, opus); err != nil {
		log.Printf("tts push: %v", err)
	}
}

func (h *GameHub) pushVoiceState(listening bool) {
	h.pushBrowserJSON(map[string]any{"type": "voice-state", "listening": listening})
}

func (h *GameHub) pushBrowserJSON(payload map[string]any) {
	h.mu.RLock()
	conn := h.bconn
	h.mu.RUnlock()
	if conn == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := wsjsonWrite(ctx, conn, payload); err != nil {
		log.Printf("browser push: %v", err)
	}
}

// 浏览器主通道：二进制=上行 OPUS 帧；文本=游戏状态上报。
func (h *GameHub) HandleBrowserWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	h.mu.Lock()
	if h.bconn != nil {
		h.bconn.Close(websocket.StatusPolicyViolation, "replaced")
	}
	h.bconn = conn
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		if h.bconn == conn {
			h.bconn = nil
		}
		h.mu.Unlock()
	}()

	for {
		msgType, data, err := conn.Read(r.Context())
		if err != nil {
			log.Printf("browser ws closed: %v", err)
			return
		}
		switch msgType {
		case websocket.MessageBinary:
			if h.linx != nil {
				if err := h.linx.SendAudio(r.Context(), data); err != nil {
					log.Printf("uplink: %v", err)
				}
			}
		case websocket.MessageText:
			var msg struct {
				Type  string    `json:"type"`
				State GameState `json:"state"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msg.Type == "game-state" {
				h.mu.Lock()
				h.state = msg.State
				h.mu.Unlock()
			}
		}
	}
}

// 灵矽第三方应用接入端点。协议要点：平台把 ASR 文本 POST 过来，
// 我们以 SSE 回复；只有 type=chat 的 message 会被平台 TTS 播给设备；
// 所有事件必须原样带回 session_id，并以 message_end 收尾。
func (h *GameHub) HandleLinxChat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Auth struct {
			DeviceID string `json:"device_id"`
		} `json:"auth"`
		Type string `json:"type"`
		Details struct {
			Msg       string `json:"msg"`
			SessionID string `json:"session_id"`
			Timestamp int64  `json:"timestamp"`
		} `json:"details"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	reply, actions, err := h.brain.Generate(r.Context(), req.Details.Msg, h.State())
	if err != nil {
		log.Printf("brain error: %v", err)
		reply, actions = "哎呀，我脑子转不动了，再来一次？", nil
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher := w.(http.Flusher)

	writeSSE(w, map[string]any{
		"event":      "message",
		"type":       "chat",
		"session_id": req.Details.SessionID,
		"details":    map[string]any{"answer": reply},
	})
	flusher.Flush()

	if len(actions) > 0 {
		writeSSE(w, map[string]any{
			"event":      "message",
			"type":       "command",
			"session_id": req.Details.SessionID,
			"details":    map[string]any{"actions": actions},
		})
		flusher.Flush()
	}

	writeSSE(w, map[string]any{
		"event":      "message_end",
		"session_id": req.Details.SessionID,
	})
	flusher.Flush()

	// 游戏驱动不走平台的 command 通道（linx_app 不解析它），由我们直推网页
	h.PushGame(r.Context(), actions)
}

func writeSSE(w http.ResponseWriter, payload map[string]any) {
	data, _ := json.Marshal(payload)
	_, _ = w.Write(append(append([]byte("data: "), data...), '\n', '\n'))
}

// 网页游戏的 WS 通道：上行状态上报，下行游戏指令（开始/换关卡/Boss 事件）。
func (h *GameHub) HandleGameWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	h.mu.Lock()
	if h.conn != nil {
		// 单玩家 demo：新连接顶掉旧连接，避免僵尸连接占位
		h.conn.Close(websocket.StatusPolicyViolation, "replaced")
	}
	h.conn = conn
	h.mu.Unlock()

	for {
		var st GameState
		err := wsjsonRead(r.Context(), conn, &st)
		if err != nil {
			log.Printf("game ws closed: %v", err)
			return
		}
		h.mu.Lock()
		h.state = st
		h.mu.Unlock()
	}
}

func (h *GameHub) State() GameState {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.state
}

// 设备端（linx_app）把灵矽下发的 stt 文本转过来，本地意图匹配后直接驱动游戏。
// 这条路不经过平台 LLM/MCP，是免公网的主线通道（见 ADR-0002）。
func (h *GameHub) HandleIntent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	log.Printf("intent: %q", req.Text)
	actions := gameActions(classify(req.Text))
	if len(actions) > 0 {
		h.PushGame(r.Context(), actions)
		// 调试入口与主线行为一致：动作也推给浏览器主通道
		h.pushBrowserJSON(map[string]any{"type": "game-action", "actions": actions})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"actions": actions})
}

func (h *GameHub) PushGame(ctx context.Context, actions []map[string]any) {
	if len(actions) == 0 {
		return
	}
	h.mu.RLock()
	conn := h.conn
	h.mu.RUnlock()
	if conn == nil {
		return
	}
	if err := wsjsonWrite(ctx, conn, actions); err != nil {
		log.Printf("push game: %v", err)
	}
}

func wsjsonRead(ctx context.Context, conn *websocket.Conn, v any) error {
	return wsjson.Read(ctx, conn, v)
}

func wsjsonWrite(ctx context.Context, conn *websocket.Conn, v any) error {
	return wsjson.Write(ctx, conn, v)
}
