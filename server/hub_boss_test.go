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

// voiceLink 的假实现：只记录 SetPlaying 调用，验证三阶段联动。
type fakeVoiceLink struct {
	calls []bool
}

func (f *fakeVoiceLink) SetPlaying(p bool)                       { f.calls = append(f.calls, p) }
func (f *fakeVoiceLink) SendAudio(context.Context, []byte) error { return nil }

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
		if h.EffPhase() == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("phase 未变为 %s，got %s", want, h.EffPhase())
}

func enterBattle(t *testing.T, h *GameHub, conn *websocket.Conn, secret string) {
	t.Helper()
	sendWS(t, conn, map[string]any{"type": "boss-enter", "coins": 30})
	if evs := readBossEvents(t, conn); len(evs) != 1 || evs[0].Kind != "awaiting-secret" {
		t.Fatalf("进场应先索要密数，got %+v", evs)
	}
	h.handleSTT("我的密数是" + secret)
	if evs := readBossEvents(t, conn); len(evs) != 1 || evs[0].Kind != "battle-started" {
		t.Fatalf("上报密数应开战，got %+v", evs)
	}
}

// 端到端：WS 进场 → STT 密数/报数 → 攻击 → 格挡失败判负原额 → 回 idle。
func TestBossBattleLifecycleOverWS(t *testing.T) {
	h, _ := newBossHub(t)
	conn := dialHub(t, h)

	enterBattle(t, h, conn, "四八三")
	if p := h.EffPhase(); p != "boss" {
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
	if p := h.EffPhase(); p != "idle" {
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
	if p := h.EffPhase(); p != "idle" {
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
