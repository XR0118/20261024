package main

import (
	"context"
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"loudleap/server/boss"
)

// 游戏网页上报的最新状态，供台词生成时取用（如结算点评报距离和摔死次数）。
type GameState struct {
	Playing bool   `json:"playing"`
	Phase   string `json:"phase,omitempty"` // idle/running/boss；空=按 playing 推导（旧前端兼容）
	Dist    int    `json:"dist"`
	Deaths  int    `json:"deaths"`
	Won     bool   `json:"won"`
	Seq     int64  `json:"seq"`
}

// voiceLink 是 hub 对语音链路的全部依赖（LinxClient 实现）。
// 抽接口是为了 hub 测试能注入假链路验证三阶段联动。
type voiceLink interface {
	SetPlaying(bool)
	SendAudio(context.Context, []byte) error
}

type GameHub struct {
	mu    sync.RWMutex
	state GameState
	conn  *websocket.Conn
	brain *Brain
	linx  voiceLink
	bconn *websocket.Conn // 浏览器主通道：音频上行/下行 + 游戏动作 + 状态

	bossMu sync.Mutex
	battle *boss.Battle
	newRng func() *rand.Rand // Boss 战随机源；测试注入固定 seed
}

func NewGameHub(brain *Brain) *GameHub {
	return &GameHub{
		brain:  brain,
		newRng: func() *rand.Rand { return rand.New(rand.NewSource(time.Now().UnixNano())) },
	}
}

func (h *GameHub) SetLinxClient(lc *LinxClient) {
	h.linx = lc
	lc.OnSTT = h.handleSTT
	lc.OnTTSFrame = h.pushTTSFrame
	lc.OnVoiceState = h.pushVoiceState
	lc.OnTTSState = func(playing bool) {
		h.pushBrowserJSON(map[string]any{"type": "tts-state", "playing": playing})
	}
}

// stt 文本直达游戏动作（ADR-0002 主线：进程内，零外部依赖）。
// 开局动作配一条 prepare 先行：何时真正开局由浏览器决定——只有它知道
// 本地播放队列何时排空（平台的 tts stop 只代表下发完，不等于播完）。
// Boss 战进行中文本先进引擎（报数/投降），跑酷指令此时装聋。
func (h *GameHub) handleSTT(text string) {
	if h.bossActive() {
		h.applyBossSTT(text)
		return
	}
	actions := gameActions(classify(text))
	if len(actions) == 0 {
		return
	}
	name, _ := actions[0]["action"].(string)
	if name == "start" || name == "load_level" {
		h.pushBrowserJSON(map[string]any{
			"type":    "game-action",
			"actions": []map[string]any{{"action": "prepare"}},
		})
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

// effectivePhase 三阶段的判定口径：显式 phase 优先，否则按旧 playing 推导。
func effectivePhase(st GameState) string {
	switch {
	case st.Phase != "":
		return st.Phase
	case st.Playing:
		return "running"
	default:
		return "idle"
	}
}

func (h *GameHub) EffPhase() string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return effectivePhase(h.state)
}

// setPhase 服务端主动切阶段（进场/收局），联动语音链路：
// 只有跨越 running 边界才拨动——boss 与 idle 同为保持连接（ADR-0003 三阶段）。
func (h *GameHub) setPhase(p string) {
	h.mu.Lock()
	prev := effectivePhase(h.state)
	h.state.Phase = p
	h.mu.Unlock()
	h.syncVoice(prev, p)
}

// syncVoice 语音会话的真实生命周期只有连/断两态：跑酷断，待命与 Boss 战连。
func (h *GameHub) syncVoice(prev, cur string) {
	if (prev == "running") != (cur == "running") {
		h.setVoiceRunning(cur == "running")
	}
}

func (h *GameHub) setVoiceRunning(running bool) {
	h.mu.RLock()
	linx := h.linx
	h.mu.RUnlock()
	if linx != nil {
		linx.SetPlaying(running)
	}
}

// ---------------- Boss 战接线（#15） ----------------

// bossActive 报数/投降等 STT 是否应进引擎。
func (h *GameHub) bossActive() bool {
	h.bossMu.Lock()
	defer h.bossMu.Unlock()
	return h.battle != nil && h.battle.Phase() != boss.PhaseEnded
}

// enterBoss 浏览器跑完全程进场开战：本局金币原额交给引擎托管，
// 结算（胜=翻倍）由 battle-ended 事件带回。
func (h *GameHub) enterBoss(coins int) {
	h.bossMu.Lock()
	defer h.bossMu.Unlock()
	if h.battle != nil && h.battle.Phase() != boss.PhaseEnded {
		h.pushBossEvents([]boss.Event{{Kind: "error", Message: "这局的挑战者还在，先打完"}})
		return
	}
	h.battle = boss.NewBattle(h.newRng(), coins)
	h.setPhase("boss")
	h.pushBossEvents([]boss.Event{{Kind: "awaiting-secret", Message: "报出你的三位密数，应战。"}})
}

// applyBossSTT 语音输入进引擎：先 RouteInput（数字→报数，否则关键词意图）。
func (h *GameHub) applyBossSTT(text string) {
	h.bossMu.Lock()
	defer h.bossMu.Unlock()
	if h.battle == nil || h.battle.Phase() == boss.PhaseEnded {
		return
	}
	in, err := boss.RouteInput(text)
	if err != nil {
		h.pushBossEvents([]boss.Event{{Kind: "error", Message: err.Error()}})
		return
	}
	h.applyInput(in)
}

// bossAction 浏览器显式动作（调试按键/QTE 格挡结果），与语音通道等价。
func (h *GameHub) bossAction(action, digits string, blocked *bool) {
	h.bossMu.Lock()
	defer h.bossMu.Unlock()
	if h.battle == nil || h.battle.Phase() == boss.PhaseEnded {
		return
	}
	switch action {
	case "block":
		if blocked == nil {
			return
		}
		evs, err := h.battle.ReportBlock(*blocked)
		h.settleBoss(evs, err)
	case "secret", "guess", "swap":
		in, perr := boss.RouteInput(digits)
		if perr != nil {
			h.pushBossEvents([]boss.Event{{Kind: "error", Message: perr.Error()}})
			return
		}
		h.applyInput(in)
	case "decline-swap":
		h.applyInput(boss.Input{Intent: boss.IntentDeclineSwap})
	case "surrender":
		h.applyInput(boss.Input{Intent: boss.IntentSurrender})
	}
}

// applyInput 按引擎当前阶段分发一次已解析的输入。调用方须持有 bossMu。
func (h *GameHub) applyInput(in boss.Input) {
	b := h.battle
	if b == nil || b.Phase() == boss.PhaseEnded {
		return
	}
	// 投降任何未终局阶段都受理（含攻击等待期）
	if in.Intent == boss.IntentSurrender {
		evs, err := b.Surrender()
		h.settleBoss(evs, err)
		return
	}
	var evs []boss.Event
	var err error
	switch b.Phase() {
	case boss.PhaseAwaitingSecret:
		if in.Guess != (boss.Secret{}) {
			evs, err = b.ReportSecret(in.Guess)
		}
	case boss.PhasePlayerTurn:
		if in.Guess != (boss.Secret{}) {
			evs, err = b.PlayerGuess(in.Guess)
		}
	case boss.PhaseSwap:
		switch {
		case in.Guess != (boss.Secret{}):
			evs, err = b.SwapSecret(in.Guess)
		case in.Intent == boss.IntentDeclineSwap:
			evs, err = b.DeclineSwap()
		case in.Intent == boss.IntentSwap:
			evs = []boss.Event{{Kind: "swap-prompt", Message: "好，报出你的新密数。"}}
		}
	case boss.PhaseAttack:
		// QTE 进行中：报数与意图都不受理，格挡结果走 boss-action/语音外通道
	}
	h.settleBoss(evs, err)
}

// settleBoss 收局并推送：battle-ended 时先回 idle 再推（读端收到
// battle-ended 的瞬间相位必然已是 idle），语音回到待命连接。
func (h *GameHub) settleBoss(evs []boss.Event, err error) {
	if err != nil {
		h.pushBossEvents([]boss.Event{{Kind: "error", Message: err.Error()}})
		return
	}
	if h.battle != nil && h.battle.Phase() == boss.PhaseEnded {
		h.setPhase("idle")
	}
	h.pushBossEvents(evs)
}

func (h *GameHub) pushBossEvents(evs []boss.Event) {
	if len(evs) == 0 {
		return
	}
	h.pushBrowserJSON(map[string]any{"type": "boss-events", "events": evs})
}

// 浏览器主通道：二进制=上行 OPUS 帧；文本=游戏状态上报 + Boss 战动作。
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
				Type    string    `json:"type"`
				State   GameState `json:"state"`
				Coins   int       `json:"coins"`
				Action  string    `json:"action"`
				Digits  string    `json:"digits"`
				Blocked *bool     `json:"blocked"`
			}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "game-state":
				h.mu.Lock()
				prev := effectivePhase(h.state)
				h.state = msg.State
				cur := effectivePhase(h.state)
				h.mu.Unlock()
				// 阶段切换联动语音会话生命周期（ADR-0002/0003：仅跑酷断连）
				h.syncVoice(prev, cur)
			case "boss-enter":
				h.enterBoss(msg.Coins)
			case "boss-action":
				h.bossAction(msg.Action, msg.Digits, msg.Blocked)
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
		Type    string `json:"type"`
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
// Boss 战中与语音通道等价：文本先进引擎。
func (h *GameHub) HandleIntent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Text string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Text == "" {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	log.Printf("intent: %q", req.Text)
	if h.bossActive() {
		h.applyBossSTT(req.Text)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"actions": []any{}})
		return
	}
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
