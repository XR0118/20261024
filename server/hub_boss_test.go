package main

import (
	"context"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"loudleap/server/boss"
)

// voiceLink 的假实现：记录 SetPlaying 调用（三阶段联动）与台词（台词层）。
type fakeVoiceLink struct {
	calls  []bool
	spoken []string // "exact:台词" | "llm:事件标记"
}

func (f *fakeVoiceLink) SetPlaying(p bool)                       { f.calls = append(f.calls, p) }
func (f *fakeVoiceLink) SendAudio(context.Context, []byte) error { return nil }
func (f *fakeVoiceLink) Speak(text string, exact bool) {
	tag := "llm"
	if exact {
		tag = "exact"
	}
	f.spoken = append(f.spoken, tag+":"+text)
}

func newBossHub(t *testing.T) (*GameHub, *fakeVoiceLink) {
	t.Helper()
	h := NewGameHub(NewBrain(BrainConfig{}))
	vl := &fakeVoiceLink{}
	h.linx = vl
	h.newRng = func() *rand.Rand { return rand.New(rand.NewSource(7)) }
	return h, vl
}

func dialHub(t *testing.T, h *GameHub) *websocket.Conn {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(h.HandleBrowserWS))
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial hub ws: %v", err)
	}
	t.Cleanup(func() { conn.CloseNow() })
	return conn
}

func readBossEvents(t *testing.T, conn *websocket.Conn) []boss.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var msg struct {
		Type   string       `json:"type"`
		Events []boss.Event `json:"events"`
	}
	if err := wsjson.Read(ctx, conn, &msg); err != nil {
		t.Fatalf("read boss-events: %v", err)
	}
	if msg.Type != "boss-events" {
		t.Fatalf("want boss-events message, got type=%s", msg.Type)
	}
	return msg.Events
}

func sendWS(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, conn, v); err != nil {
		t.Fatalf("ws write: %v", err)
	}
}

// game-state 在服务端 goroutine 异步处理，相位断言用轮询等到位。
func waitPhase(t *testing.T, h *GameHub, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if h.currentPhase() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("phase 未变为 %s，got %s", want, h.currentPhase())
}

func enterBattle(t *testing.T, h *GameHub, conn *websocket.Conn, secret string) {
	t.Helper()
	sendWS(t, conn, map[string]any{"type": "boss-enter", "coins": 30})
	if evs := readBossEvents(t, conn); len(evs) != 1 || evs[0].Kind != "awaiting-secret" {
		t.Fatalf("进场应先索要密数，got %+v", evs)
	}
	h.handleSTT("我的密数是" + secret)
	evs := readBossEvents(t, conn)
	if len(evs) != 2 || evs[0].Kind != "battle-started" || evs[1].Kind != "secret-confirmed" {
		t.Fatalf("上报密数应开战并回显确认，got %+v", evs)
	}
	if evs[1].Guess == "" {
		t.Fatalf("secret-confirmed 应携带密数回显，got %+v", evs[1])
	}
}

// 端到端：WS 进场 → STT 密数/报数 → 攻击 → 格挡失败判负原额 → 回 idle。
func TestBossBattleLifecycleOverWS(t *testing.T) {
	h, _ := newBossHub(t)
	conn := dialHub(t, h)

	enterBattle(t, h, conn, "四八三")
	if p := h.currentPhase(); p != "boss" {
		t.Fatalf("Boss 战阶段应为 boss，got %s", p)
	}

	// Boss 阶段跑酷指令必须装聋。静默不单独断言：handleSTT 的推送在其返回前
	// 同步完成，任何误推都会被后续 readBossEvents 以 type/kind 不符抓住。
	h.handleSTT("开始游戏")
	h.handleSTT("换一张地图")

	// 玩家每轮报九八七，推进到攻击（seed 固定，路径确定）
	sawAttack := false
	for round := 0; round < 20 && !sawAttack; round++ {
		h.handleSTT("报数 九八七")
		for _, e := range readBossEvents(t, conn) {
			if e.Kind == "attack" {
				sawAttack = true
			}
			if e.Turn == "player" && e.Hits == 3 {
				t.Fatalf("九八七不应全中（seed 7 的 Boss 密数被改？）")
			}
		}
	}
	if !sawAttack {
		t.Fatal("20 轮内未进入攻击，引擎收敛失效")
	}

	// 攻击等待期语音报数不受理（QTE 由本地响度负责）；
	// 若误受理，下一 reads 会读到多余事件导致断言失败
	h.handleSTT("三七八")

	sendWS(t, conn, map[string]any{"type": "boss-action", "action": "block", "blocked": false})
	evs := readBossEvents(t, conn)
	last := evs[len(evs)-1]
	if last.Kind != "battle-ended" || last.Outcome != boss.OutcomeLose || last.Coins != 30 || last.FinalCoins != 30 {
		t.Fatalf("格挡失败应判负、按原额结算，got %+v", evs)
	}
	if p := h.currentPhase(); p != "idle" {
		t.Fatalf("战后应回 idle，got %s", p)
	}
}

func TestSurrenderOverSTT(t *testing.T) {
	h, _ := newBossHub(t)
	conn := dialHub(t, h)
	enterBattle(t, h, conn, "一二三")

	h.handleSTT("我投降了")
	evs := readBossEvents(t, conn)
	if len(evs) != 1 || evs[0].Kind != "battle-ended" || evs[0].Outcome != boss.OutcomeSurrender || evs[0].FinalCoins != 30 {
		t.Fatalf("投降应立即判负原额结算，got %+v", evs)
	}
	if p := h.currentPhase(); p != "idle" {
		t.Fatalf("投降后应回 idle，got %s", p)
	}
}

// game-state 三阶段 + 旧 playing 布尔兼容；语音链路只在进/出跑酷时切换。
func TestPhaseThreeStatesAndCompat(t *testing.T) {
	h, vl := newBossHub(t)
	conn := dialHub(t, h)

	sendWS(t, conn, map[string]any{"type": "game-state", "state": map[string]any{"playing": true, "dist": 5}})
	waitPhase(t, h, "running")
	sendWS(t, conn, map[string]any{"type": "game-state", "state": map[string]any{"phase": "boss", "dist": 300}})
	waitPhase(t, h, "boss")
	sendWS(t, conn, map[string]any{"type": "game-state", "state": map[string]any{"playing": false, "dist": 300}})
	waitPhase(t, h, "idle")
	if len(vl.calls) != 2 || vl.calls[0] != true || vl.calls[1] != false {
		t.Fatalf("语音链路应只在 running↔非running 切换（boss 保持连接），got %v", vl.calls)
	}
	// 未知相位串按旧口径推导，不当合法 phase 存
	sendWS(t, conn, map[string]any{"type": "game-state", "state": map[string]any{"phase": "paused", "playing": true}})
	waitPhase(t, h, "running")
}

// 调试通道与语音通道等价：Boss 战中 /intent 的文本同样进引擎。
func TestIntentEndpointBossAware(t *testing.T) {
	h, _ := newBossHub(t)
	conn := dialHub(t, h)
	enterBattle(t, h, conn, "一二三")

	req := httptest.NewRequest("POST", "/intent", strings.NewReader(`{"text":"投降"}`))
	rec := httptest.NewRecorder()
	h.HandleIntent(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("intent status %d", rec.Code)
	}
	evs := readBossEvents(t, conn)
	if len(evs) != 1 || evs[0].Kind != "battle-ended" || evs[0].Outcome != boss.OutcomeSurrender {
		t.Fatalf("/intent 在 Boss 战应走引擎，got %+v", evs)
	}
}

// 战斗进行中收到旧式 game-state（无 phase、playing=true）不得把相位
// 打回 running 切断 Boss 语音——相位在战期内由服务端持有。
func TestLegacyGameStateDuringBossKeepsVoice(t *testing.T) {
	h, vl := newBossHub(t)
	conn := dialHub(t, h)
	enterBattle(t, h, conn, "四八三")

	sendWS(t, conn, map[string]any{"type": "game-state", "state": map[string]any{"playing": true, "dist": 300}})
	// 等 game-state 落库（Playing=true 且相位被服务端钉在 boss）再继续，
	// 否则测试侧直接调 handleSTT 会与服务器 goroutine 的处理竞态
	deadline := time.Now().Add(2 * time.Second)
	for {
		h.mu.RLock()
		landed := h.state.Playing && h.state.Phase == phaseBoss
		h.mu.RUnlock()
		if landed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("旧式 game-state 未被处理或未被钉在 boss 相位")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(vl.calls) != 0 {
		t.Fatalf("战斗中旧式上报不应拨动语音链路，got %v", vl.calls)
	}

	h.handleSTT("我投降了")
	evs := readBossEvents(t, conn)
	if evs[len(evs)-1].Kind != "battle-ended" {
		t.Fatalf("战斗应正常进行到投降，got %+v", evs)
	}
	waitPhase(t, h, "idle")
}

// 页面重载（新连接顶掉旧连接）即弃局：新页面可开新战斗，旧状态不残留。
func TestReconnectAbandonsBattle(t *testing.T) {
	h, _ := newBossHub(t)
	conn := dialHub(t, h)
	enterBattle(t, h, conn, "四八三")

	// 后台排空旧连接：hub 的 replace 要走 close 握手，测试端不读会卡到超时
	go func() {
		for {
			if _, _, err := conn.Read(context.Background()); err != nil {
				return
			}
		}
	}()
	conn2 := dialHub(t, h) // 顶掉 conn
	waitPhase(t, h, "idle")

	// 旧战斗已弃：新页面进场应是全新战斗（若未弃局会收到 error 事件）
	sendWS(t, conn2, map[string]any{"type": "boss-enter", "coins": 50})
	if evs := readBossEvents(t, conn2); len(evs) != 1 || evs[0].Kind != "awaiting-secret" {
		t.Fatalf("重连后应能开新战斗，got %+v", evs)
	}
}

// Boss 事件转化为语音台词：关键信息逐字直发（exact），氛围走 LLM 事件标记。
func TestBossEventsAreSpoken(t *testing.T) {
	h, vl := newBossHub(t)
	conn := dialHub(t, h)

	sendWS(t, conn, map[string]any{"type": "boss-enter", "coins": 30})
	readBossEvents(t, conn) // awaiting-secret
	h.handleSTT("我的密数是四八三")
	readBossEvents(t, conn) // battle-started
	h.handleSTT("报数 九八七")
	readBossEvents(t, conn) // player + boss guess-result
	h.handleSTT("我投降了")
	readBossEvents(t, conn) // battle-ended

	spoken := strings.Join(vl.spoken, "\n")
	for _, want := range []string{
		"exact:请报出你的三位密数",
		"llm:【BOSS 来了】",
		"exact:听好了，我报：零一二", // Boss 首报恒为 012
		"exact:认输了？明智",
	} {
		if !strings.Contains(spoken, want) {
			t.Errorf("台词缺失 %q\nspoken:\n%s", want, spoken)
		}
	}
	// 玩家九八七对 seed-7 密数零命中——零命中台词有三句随机变体
	zeroHit := false
	for _, v := range []string{"零位", "一个都没中"} {
		if strings.Contains(spoken, "exact:"+v) {
			zeroHit = true
		}
	}
	if !zeroHit {
		t.Errorf("零命中台词缺失\nspoken:\n%s", spoken)
	}
}
