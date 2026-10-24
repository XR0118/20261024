package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"loudleap/server/boss"
)

// 游戏相位（ADR-0003 三阶段）。语音链路只在跨越 running 边界时切换。
const (
	phaseIdle    = "idle"
	phaseRunning = "running"
	phaseBoss    = "boss"
)

// 游戏网页上报的最新状态，供台词生成时取用（如结算点评报距离和摔死次数）。
type GameState struct {
	Playing bool   `json:"playing"`
	Phase   string `json:"phase,omitempty"` // idle/running/boss；空或非法值=按 playing 推导（旧前端兼容）
	Dist    int    `json:"dist"`
	Deaths  int    `json:"deaths"`
	Won     bool   `json:"won"`
	Seq     int64  `json:"seq"`
}

// voiceLink 是 hub 对语音链路的全部依赖（LinxClient 实现）。
// 抽接口是为了 hub 测试能注入假链路验证三阶段联动与台词层。
type voiceLink interface {
	SetPlaying(bool)
	SendAudio(context.Context, []byte) error
	Speak(text string, exact bool)
}

// browserMsg 浏览器主通道文本消息的全集。
type browserMsg struct {
	Type    string    `json:"type"`
	State   GameState `json:"state"`
	Coins   int       `json:"coins"`
	Action  string    `json:"action"`
	Digits  string    `json:"digits"`
	Blocked *bool     `json:"blocked"`
	Dist    int       `json:"dist"`
	Deaths  int       `json:"deaths"`
}

type GameHub struct {
	mu    sync.RWMutex
	state GameState
	conn  *websocket.Conn
	brain *Brain
	linx  voiceLink
	bconn *websocket.Conn // 浏览器主通道：音频上行/下行 + 游戏动作 + 状态
	mem   *MemoryClient   // 局终记忆写入；nil=未配置，无操作

	bossMu       sync.Mutex
	battle       *boss.Battle
	newRng       func() *rand.Rand // Boss 战随机源；测试注入固定 seed
	blockTimeout time.Duration     // 攻击等格挡结果的兜底超时；测试注入短值
	blockGen     uint64            // 攻击代数：新一轮攻击使旧超时计时作废
}

func NewGameHub(brain *Brain) *GameHub {
	return &GameHub{
		brain:        brain,
		newRng:       func() *rand.Rand { return rand.New(rand.NewSource(time.Now().UnixNano())) },
		blockTimeout: 3 * time.Second,
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

// effectivePhase 三阶段判定口径：显式且合法的 phase 优先，否则按旧 playing 推导。
func effectivePhase(st GameState) string {
	switch st.Phase {
	case phaseIdle, phaseRunning, phaseBoss:
		return st.Phase
	}
	if st.Playing {
		return phaseRunning
	}
	return phaseIdle
}

// currentPhase 报告当前有效相位。
func (h *GameHub) currentPhase() string {
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
	if (prev == phaseRunning) != (cur == phaseRunning) {
		h.setVoiceRunning(cur == phaseRunning)
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

// applyGameState 合并浏览器状态上报。战斗进行中相位由服务端持有：
// 旧式 playing 上报（无 phase）不得把 Boss 战打回 running 切断语音。
func (h *GameHub) applyGameState(st GameState) {
	active := h.bossActive()
	h.mu.Lock()
	prev := effectivePhase(h.state)
	if active && effectivePhase(st) != phaseBoss {
		st.Phase = phaseBoss
	}
	h.state = st
	cur := effectivePhase(h.state)
	h.mu.Unlock()
	h.syncVoice(prev, cur)
}

// bossActive 报数/投降等 STT 是否应进引擎。
func (h *GameHub) bossActive() bool {
	h.bossMu.Lock()
	defer h.bossMu.Unlock()
	return h.liveBattle() != nil
}

// liveBattle 进行中的战斗；nil=无战局或已终局。调用方须持有 bossMu。
func (h *GameHub) liveBattle() *boss.Battle {
	if h.battle != nil && h.battle.Phase() != boss.PhaseEnded {
		return h.battle
	}
	return nil
}

// enterBoss 浏览器跑完全程进场开战：本局金币原额交给引擎托管，
// 结算（胜=翻倍）由 battle-ended 事件带回。
func (h *GameHub) enterBoss(coins int) {
	h.bossMu.Lock()
	defer h.bossMu.Unlock()
	if h.liveBattle() != nil {
		h.pushBossEvents([]boss.Event{{Kind: "error", Message: "本局 Boss 战已在进行"}})
		return
	}
	h.battle = boss.NewBattle(h.newRng(), coins)
	h.setPhase(phaseBoss)
	h.pushBossEvents([]boss.Event{{Kind: "awaiting-secret", Message: "请报出你的三位密数"}})
}

// applyBossSTT 语音输入进引擎（与 /intent 调试通道共用）。
func (h *GameHub) applyBossSTT(text string) {
	h.bossMu.Lock()
	defer h.bossMu.Unlock()
	if h.liveBattle() == nil {
		return
	}
	h.applyParsed(text)
}

// bossAction 浏览器显式动作（调试按键/QTE 格挡结果），与语音通道等价。
func (h *GameHub) bossAction(m browserMsg) {
	h.bossMu.Lock()
	defer h.bossMu.Unlock()
	if h.liveBattle() == nil {
		return
	}
	switch m.Action {
	case "block":
		if m.Blocked == nil {
			return
		}
		evs, err := h.battle.ReportBlock(*m.Blocked)
		h.settleBoss(evs, err)
	case "secret", "guess":
		h.applyParsed(m.Digits)
	case "swap":
		if m.Digits == "" {
			// 与语音通道对称：只说「换数」先给提示，新密数随后报
			h.applyInput(boss.Input{Intent: boss.IntentSwap})
			return
		}
		h.applyParsed(m.Digits)
	case "decline-swap":
		h.applyInput(boss.Input{Intent: boss.IntentDeclineSwap})
	case "surrender":
		h.applyInput(boss.Input{Intent: boss.IntentSurrender})
	}
}

// applyParsed 解析一段报数文本并分发（调用方须持有 bossMu）。
func (h *GameHub) applyParsed(text string) {
	in, err := boss.RouteInput(text)
	if err != nil {
		h.pushBossEvents([]boss.Event{{Kind: "error", Message: err.Error()}})
		return
	}
	h.applyInput(in)
}

// applyInput 按引擎当前阶段分发一次已解析的输入。调用方须持有 bossMu。
func (h *GameHub) applyInput(in boss.Input) {
	b := h.liveBattle()
	if b == nil {
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
			if err == nil {
				// 界面确认：语音上报的密数须回显给玩家核对（键盘路径同源）
				evs = append(evs, boss.Event{Kind: "secret-confirmed", Guess: in.Guess.String(), Message: "密数已确认"})
			}
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
			evs = []boss.Event{{Kind: "swap-prompt", Message: "请报出你的新密数"}}
		}
	case boss.PhaseAttack:
		// QTE 进行中：报数与意图都不受理，格挡结果只走浏览器上报
	}
	h.settleBoss(evs, err)
	for _, e := range evs {
		if e.Kind == boss.KindAttack {
			h.armBlockTimeout()
		}
	}
}

// armBlockTimeout 攻击兜底：窗口内无格挡结果上报（页面僵死/断连）按失败判负。
// 代数计数防同一场对局的二次攻击被首攻残留计时器误判（快速格挡→放弃
// 换数→Boss 再全中可能落在首攻超时窗内）；调用方须持有 bossMu。
func (h *GameHub) armBlockTimeout() {
	h.blockGen++
	b, gen := h.battle, h.blockGen
	time.AfterFunc(h.blockTimeout, func() {
		h.bossMu.Lock()
		defer h.bossMu.Unlock()
		if gen != h.blockGen || h.battle != b || b.Phase() != boss.PhaseAttack {
			return // 新一轮攻击/上报/弃局/新对局，本计时作废
		}
		evs, err := b.ReportBlock(false)
		h.settleBoss(evs, err)
	})
}

// settleBoss 收局并推送：battle-ended 时先回 idle 再推（读端收到
// battle-ended 的瞬间相位必然已是 idle），语音回到待命连接。
func (h *GameHub) settleBoss(evs []boss.Event, err error) {
	if err != nil {
		h.pushBossEvents([]boss.Event{{Kind: "error", Message: err.Error()}})
		return
	}
	if h.battle != nil && h.battle.Phase() == boss.PhaseEnded {
		h.setPhase(phaseIdle)
	}
	h.pushBossEvents(evs)
	if h.battle != nil && h.battle.Phase() == boss.PhaseEnded {
		h.writeRunMemory(evs)
	}
}

// writeRunMemory 局终两件事：REST 写记忆片段（教练复盘的数据来源，失败仅
// 日志）；向人格注入【本局结束】标记——按 persona 提示词教练默认做总结。
func (h *GameHub) writeRunMemory(evs []boss.Event) {
	var ended *boss.Event
	for i := range evs {
		if evs[i].Kind == boss.KindBattleEnded {
			ended = &evs[i]
		}
	}
	if ended == nil {
		return
	}
	st := h.State()
	fact := fmt.Sprintf("距离 %d 米，摔死 %d 次，金币 %d，Boss 战：%s",
		st.Dist, st.Deaths, ended.FinalCoins, outcomeText(ended.Outcome))
	h.speak("【本局结束】"+fact, false)
	h.writeMemory("【对局】" + fact)
}

// handleRunEnded 途中局终（掉坑/撞杆，未到 Boss 战）的记忆与教练标记。
// Boss 战进行中忽略：该局的记忆由 battle-ended 路径负责，防同局双写
// （异常旧页面在战斗中补发 run-ended 的场景）。
func (h *GameHub) handleRunEnded(m browserMsg) {
	if h.bossActive() {
		return
	}
	fact := fmt.Sprintf("距离 %d 米，摔死 %d 次，金币 %d，未到 Boss 战（途中局终）",
		m.Dist, m.Deaths, m.Coins)
	h.speak("【本局结束】"+fact, false)
	h.writeMemory("【对局】" + fact)
}

func (h *GameHub) writeMemory(summary string) {
	if h.mem == nil {
		return
	}
	mem := h.mem
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := mem.WriteRun(ctx, summary); err != nil {
			log.Printf("memory write: %v", err)
		}
	}()
}

func outcomeText(o boss.Outcome) string {
	switch o {
	case boss.OutcomeWin:
		return "胜（金币翻倍）"
	case boss.OutcomeLose:
		return "负（格挡失败）"
	case boss.OutcomeSurrender:
		return "认输"
	}
	return string(o)
}

// abandonBoss 页面重载即弃局：刷新后的新页面不应接手旧战斗状态。
func (h *GameHub) abandonBoss() {
	h.bossMu.Lock()
	h.battle = nil
	h.bossMu.Unlock()
	h.setPhase(phaseIdle)
}

func (h *GameHub) pushBossEvents(evs []boss.Event) {
	if len(evs) == 0 {
		return
	}
	h.pushBrowserJSON(map[string]any{"type": "boss-events", "events": evs})
	for _, e := range evs {
		if line, exact, ok := challengerLine(e); ok {
			h.speak(line, exact)
		}
	}
}

func (h *GameHub) speak(line string, exact bool) {
	h.mu.RLock()
	linx := h.linx
	h.mu.RUnlock()
	if linx != nil {
		linx.Speak(line, exact)
	}
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
	// 页面连接（无论顶掉旧连接还是旧连接已自行断开）一律开启新对局状态：
	// 刷新后残留的战斗会霸占 boss 相位与语音输入路由
	h.abandonBoss()
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
			var msg browserMsg
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			switch msg.Type {
			case "game-state":
				h.applyGameState(msg.State)
			case "boss-enter":
				h.enterBoss(msg.Coins)
			case "boss-action":
				h.bossAction(msg)
			case "run-ended":
				h.handleRunEnded(msg)
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
		h.applyGameState(st)
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
