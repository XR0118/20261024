package boss

import (
	"math/rand"
	"reflect"
	"testing"
)

// 位数判定语义（spec 裁决）：只算数字与位置都对（bulls），0–3。
func TestHits(t *testing.T) {
	cases := []struct {
		name          string
		guess, secret Secret
		want          int
	}{
		{"全对", Secret{1, 2, 3}, Secret{1, 2, 3}, 3},
		{"只有中间位对", Secret{1, 2, 3}, Secret{3, 2, 1}, 1},
		{"数字都在但位置全错", Secret{1, 2, 3}, Secret{2, 1, 4}, 0},
		{"全错", Secret{1, 2, 3}, Secret{4, 5, 6}, 0},
		{"首尾对", Secret{1, 2, 3}, Secret{1, 9, 3}, 2},
		{"前导零参与判定", Secret{0, 1, 2}, Secret{0, 8, 9}, 1},
	}
	for _, c := range cases {
		if got := hits(c.guess, c.secret); got != c.want {
			t.Errorf("%s: hits(%v, %v) = %d, want %d", c.name, c.guess, c.secret, got, c.want)
		}
	}
}

func TestSecretValid(t *testing.T) {
	cases := []struct {
		s    Secret
		want bool
	}{
		{Secret{3, 7, 8}, true},
		{Secret{0, 1, 2}, true},  // 玩家上报口径允许前导零
		{Secret{1, 1, 2}, false}, // 数字重复
		{Secret{9, 0, 9}, false},
		{Secret{10, 2, 3}, false}, // 非个位数字
		{Secret{-1, 2, 3}, false},
	}
	for _, c := range cases {
		if got := c.s.Valid(); got != c.want {
			t.Errorf("Secret%v.Valid() = %v, want %v", c.s, got, c.want)
		}
	}
}

func TestSecretString(t *testing.T) {
	if got := (Secret{3, 7, 8}).String(); got != "378" {
		t.Errorf("String() = %q, want %q", got, "378")
	}
	if got := (Secret{0, 1, 2}).String(); got != "012" {
		t.Errorf("String() = %q, want %q（前导零保留）", got, "012")
	}
}

// Boss 密数生成：不重复三位数、首位非零、同 seed 可复现。
func TestGenSecret(t *testing.T) {
	for seed := int64(0); seed < 300; seed++ {
		s := genSecret(rand.New(rand.NewSource(seed)))
		if !s.Valid() {
			t.Fatalf("seed %d: 生成了非法密数 %v", seed, s)
		}
		if s[0] == 0 {
			t.Fatalf("seed %d: 密数首位为 0（%v），Boss 报数口语不自然", seed, s)
		}
	}
	if genSecret(rand.New(rand.NewSource(7))) != genSecret(rand.New(rand.NewSource(7))) {
		t.Fatal("同一 seed 应生成同一密数")
	}
}

// ---- 对局生命周期 ----

func newTestBattle(t *testing.T, bossSecret, playerSecret Secret, coins int) *Battle {
	t.Helper()
	b := newBattleWithSecret(rand.New(rand.NewSource(1)), bossSecret, coins)
	if _, err := b.ReportSecret(playerSecret); err != nil {
		t.Fatalf("上报密数: %v", err)
	}
	return b
}

func TestReportSecret(t *testing.T) {
	b := newBattleWithSecret(rand.New(rand.NewSource(1)), Secret{4, 8, 3}, 30)
	if b.Phase() != PhaseAwaitingSecret {
		t.Fatalf("开局应等待密数上报，got %s", b.Phase())
	}
	if _, err := b.ReportSecret(Secret{1, 1, 2}); err == nil {
		t.Fatal("数字重复的密数应被拒绝")
	}
	evs, err := b.ReportSecret(Secret{9, 5, 0})
	if err != nil {
		t.Fatalf("合法密数被拒: %v", err)
	}
	if len(evs) != 1 || evs[0].Kind != "battle-started" {
		t.Fatalf("应发出且仅发出 battle-started，got %+v", evs)
	}
	if b.Phase() != PhasePlayerTurn {
		t.Fatalf("上报密数后轮到玩家报数，got %s", b.Phase())
	}
	if _, err := b.ReportSecret(Secret{1, 2, 3}); err == nil {
		t.Fatal("战斗已开始，再次上报密数应被拒绝")
	}
}

func TestPlayerGuessJudgedAndBossTurnFollows(t *testing.T) {
	b := newTestBattle(t, Secret{4, 8, 3}, Secret{9, 5, 0}, 30)
	if _, err := b.PlayerGuess(Secret{1, 2, 2}); err == nil {
		t.Fatal("数字重复的报数应被拒绝")
	}
	evs, err := b.PlayerGuess(Secret{4, 5, 6}) // 对 483：首位命中
	if err != nil {
		t.Fatalf("合法报数被拒: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("玩家报数后应紧跟 Boss 回合，got %+v", evs)
	}
	p := evs[0]
	if p.Kind != "guess-result" || p.Turn != "player" || p.Guess != "456" || p.Hits != 1 {
		t.Fatalf("玩家判定事件不符：%+v", p)
	}
	g := evs[1]
	// 候选集为全部 720 个时，任意不重复报数的分割完全对称，
	// 熵贪心并列后取数值序 → 首报恒为 012
	if g.Kind != "guess-result" || g.Turn != "boss" || g.Guess != "012" {
		t.Fatalf("Boss 首报应确定性地选 012，got %+v", g)
	}
	if g.Hits != 0 { // 012 对 950：无一位数字与位置都对
		t.Fatalf("Boss 首报命中数应可复算，got %+v", g)
	}
	if b.Phase() != PhasePlayerTurn {
		t.Fatalf("Boss 回合后轮到玩家，got %s", b.Phase())
	}
}

func TestPlayerFullHitWinsWithDoubledCoins(t *testing.T) {
	b := newTestBattle(t, Secret{4, 8, 3}, Secret{1, 2, 5}, 30)
	evs, err := b.PlayerGuess(Secret{4, 8, 3})
	if err != nil {
		t.Fatalf("全中报数被拒: %v", err)
	}
	if len(evs) != 2 {
		t.Fatalf("应有玩家判定 + 终局两事件，got %+v", evs)
	}
	if evs[0].Hits != 3 {
		t.Fatalf("玩家应全中，got %+v", evs[0])
	}
	e := evs[1]
	if e.Kind != "battle-ended" || e.Outcome != OutcomeWin || e.Coins != 30 || e.FinalCoins != 60 {
		t.Fatalf("胜局结算应为金币翻倍 30→60，got %+v", e)
	}
	if b.Phase() != PhaseEnded {
		t.Fatalf("终局后阶段应为 ended，got %s", b.Phase())
	}
	if _, err := b.PlayerGuess(Secret{7, 8, 9}); err == nil {
		t.Fatal("终局后继续报数应被拒绝")
	}
}

// ---- 攻击-格挡-亮位-换数 ----

// driveToAttack：玩家每轮固定报 987（对注入的 Boss 密数 483 恒命中 1 位，不会全中），
// 推进到 Boss 全中发动攻击为止。
func driveToAttack(t *testing.T, b *Battle) {
	t.Helper()
	for round := 0; round < 20; round++ {
		if b.Phase() != PhasePlayerTurn {
			t.Fatalf("第 %d 轮开始时阶段异常：%s", round, b.Phase())
		}
		evs, err := b.PlayerGuess(Secret{9, 8, 7})
		if err != nil {
			t.Fatalf("第 %d 轮报数: %v", round, err)
		}
		for _, e := range evs {
			if e.Kind == "attack" {
				return
			}
		}
	}
	t.Fatal("20 轮内 Boss 未全中（熵贪心收敛失效）")
}

func TestBlockFailLosesWithOriginalCoins(t *testing.T) {
	b := newTestBattle(t, Secret{4, 8, 3}, Secret{9, 5, 0}, 30)
	driveToAttack(t, b)
	if b.Phase() != PhaseAttack {
		t.Fatalf("Boss 全中后应进入攻击阶段，got %s", b.Phase())
	}
	if _, err := b.PlayerGuess(Secret{9, 8, 7}); err == nil {
		t.Fatal("攻击等待阶段不接受报数")
	}
	evs, err := b.ReportBlock(false)
	if err != nil {
		t.Fatalf("上报格挡失败: %v", err)
	}
	if len(evs) != 1 || evs[0].Kind != "battle-ended" || evs[0].Outcome != OutcomeLose {
		t.Fatalf("格挡失败应判负收局，got %+v", evs)
	}
	if evs[0].Coins != 30 || evs[0].FinalCoins != 30 {
		t.Fatalf("负局按原额结算，got %+v", evs[0])
	}
}

func TestBlockSuccessRevealsAndSwapResetsBoss(t *testing.T) {
	b := newTestBattle(t, Secret{4, 8, 3}, Secret{9, 5, 0}, 30)
	driveToAttack(t, b)
	evs, err := b.ReportBlock(true)
	if err != nil {
		t.Fatalf("上报格挡成功: %v", err)
	}
	if len(evs) != 1 || evs[0].Kind != "reveal" {
		t.Fatalf("格挡成功应亮位，got %+v", evs)
	}
	rv := evs[0]
	if rv.Position < 1 || rv.Position > 3 || rv.Digit != (Secret{4, 8, 3})[rv.Position-1] {
		t.Fatalf("亮位应与 Boss 密数一致：第 %d 位=%d", rv.Position, rv.Digit)
	}
	if b.Phase() != PhaseSwap {
		t.Fatalf("格挡成功后应进入换数抉择，got %s", b.Phase())
	}
	if _, err := b.ReportBlock(true); err == nil {
		t.Fatal("换数阶段不接受再次上报格挡")
	}
	evs, err = b.SwapSecret(Secret{6, 7, 9})
	if err != nil || len(evs) != 1 || evs[0].Kind != "swap-accepted" {
		t.Fatalf("换数应被接受，got %+v (%v)", evs, err)
	}
	if b.Phase() != PhasePlayerTurn {
		t.Fatalf("换数后轮到玩家，got %s", b.Phase())
	}
	// Boss 知识清零的证据：候选集回到全集，首报重新从 012 开始
	evs, err = b.PlayerGuess(Secret{9, 8, 7}) // 对 483 恒 1 位，安全
	if err != nil {
		t.Fatalf("换数后报数: %v", err)
	}
	if evs[1].Guess != "012" {
		t.Fatalf("换数后 Boss 应从零知识重新开始（首报 012），got %+v", evs[1])
	}
	if evs[1].Hits != 0 { // 012 对新密数 679：零命中
		t.Fatalf("Boss 首报命中应可复算，got %+v", evs[1])
	}
}

func TestDeclineSwapBossFullHitsAgain(t *testing.T) {
	b := newTestBattle(t, Secret{4, 8, 3}, Secret{9, 5, 0}, 30)
	driveToAttack(t, b)
	if _, err := b.ReportBlock(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.DeclineSwap(); err != nil {
		t.Fatalf("放弃换数: %v", err)
	}
	if b.Phase() != PhasePlayerTurn {
		t.Fatalf("放弃换数后轮到玩家，got %s", b.Phase())
	}
	// 不换数 = Boss 候选集只剩旧密数，下回合立即再全中
	evs, err := b.PlayerGuess(Secret{9, 8, 7})
	if err != nil {
		t.Fatal(err)
	}
	if evs[1].Guess != "950" || evs[1].Hits != 3 {
		t.Fatalf("放弃换数后 Boss 应立即再报出玩家密数，got %+v", evs[1])
	}
	if evs[len(evs)-1].Kind != "attack" {
		t.Fatalf("应再次进入攻击，got %+v", evs)
	}
}

func TestRevealExhaustedAfterThreeBlocks(t *testing.T) {
	b := newTestBattle(t, Secret{4, 8, 3}, Secret{9, 5, 0}, 30)
	secrets := []Secret{{1, 2, 5}, {6, 7, 9}, {2, 3, 5}}
	reveals := 0
	for i := 0; i < 3; i++ {
		driveToAttack(t, b)
		evs, err := b.ReportBlock(true)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if e.Kind == "reveal" {
				reveals++
			}
		}
		if _, err := b.SwapSecret(secrets[i]); err != nil {
			t.Fatal(err)
		}
	}
	if reveals != 3 {
		t.Fatalf("三次格挡成功应各亮一位，got %d", reveals)
	}
	// 三位全亮后再格挡成功：无位可亮，事件里不应出现 reveal
	driveToAttack(t, b)
	evs, err := b.ReportBlock(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == "reveal" {
			t.Fatalf("密数三位已全亮，不应再亮位：%+v", evs)
		}
	}
	if b.Phase() != PhaseSwap {
		t.Fatalf("仍应进入换数抉择，got %s", b.Phase())
	}
}

func TestSurrenderEndsWithOriginalCoins(t *testing.T) {
	b := newTestBattle(t, Secret{4, 8, 3}, Secret{9, 5, 0}, 30)
	evs, err := b.Surrender()
	if err != nil {
		t.Fatalf("投降: %v", err)
	}
	if len(evs) != 1 || evs[0].Kind != "battle-ended" || evs[0].Outcome != OutcomeSurrender {
		t.Fatalf("投降应立即判负收局，got %+v", evs)
	}
	if evs[0].Coins != 30 || evs[0].FinalCoins != 30 {
		t.Fatalf("投降按原额结算，got %+v", evs[0])
	}
	if _, err := b.Surrender(); err == nil {
		t.Fatal("终局后不可再投降")
	}
}

// ---- 熵贪心收敛与确定性 ----

// 玩家每轮固定报 789（不会误全中），Boss 必须在 8 报内全中发动攻击。
func TestBossConvergesWithinEightTurns(t *testing.T) {
	attacks := 0
	for seed := int64(0); seed < 500; seed++ {
		b := NewBattle(rand.New(rand.NewSource(seed)), 10)
		if _, err := b.ReportSecret(Secret{1, 2, 3}); err != nil {
			t.Fatal(err)
		}
		turns := 0
		for b.Phase() == PhasePlayerTurn {
			evs, err := b.PlayerGuess(Secret{7, 8, 9})
			if err != nil {
				t.Fatalf("seed %d: %v", seed, err)
			}
			sawAttack := false
			for _, e := range evs {
				if e.Kind == "guess-result" && e.Turn == "boss" {
					turns++
				}
				if e.Kind == "attack" {
					sawAttack = true
				}
			}
			if sawAttack {
				attacks++
				if turns > 8 {
					t.Fatalf("seed %d: Boss 第 %d 报才全中，超出阈值 8", seed, turns)
				}
				break
			}
			if turns > 15 {
				t.Fatalf("seed %d: 15 报仍未收敛，熵贪心失效", seed)
			}
		}
		// 玩家 789 恰好全中 Boss 密数的种子以胜利收场，自然跳过
	}
	if attacks < 400 {
		t.Fatalf("有效攻击样本过少（%d/500），收敛结论不可信", attacks)
	}
}

// 同 seed + 同动作脚本 → 完全一致的事件流（含亮位选择的随机性）。
func TestDeterministicGivenSameSeedAndActions(t *testing.T) {
	run := func() []Event {
		b := NewBattle(rand.New(rand.NewSource(99)), 25)
		var evs []Event
		es, _ := b.ReportSecret(Secret{4, 5, 6})
		evs = append(evs, es...)
		for i := 0; i < 8 && b.Phase() != PhaseEnded; i++ {
			switch b.Phase() {
			case PhasePlayerTurn:
				es, _ = b.PlayerGuess(Secret{7, 8, 9})
			case PhaseAttack:
				es, _ = b.ReportBlock(true)
			case PhaseSwap:
				es, _ = b.SwapSecret(Secret{3, 0, 6})
			default:
				es = nil
			}
			evs = append(evs, es...)
		}
		return evs
	}
	if a, b := run(), run(); !reflect.DeepEqual(a, b) {
		t.Fatalf("同 seed 同动作序列应产生完全一致的事件流：\n%+v\nvs\n%+v", a, b)
	}
}
