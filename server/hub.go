package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"

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
}

func NewGameHub(brain *Brain) *GameHub {
	return &GameHub{brain: brain}
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
