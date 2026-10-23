// Package boss 实现猜数字对决的数值引擎（ADR-0003 决策 2）：
// 猜数策略、位数判定、亮位、胜负与结算全部在本地完成，
// 不碰 WS、不碰灵矽——可测试、演示零翻车。
package boss

import (
	"fmt"
	"math/rand"
	"sort"
)

// Phase 对局阶段。合法动作随阶段切换（见各方法）。
type Phase string

const (
	PhaseAwaitingSecret Phase = "awaiting-secret" // 等玩家上报密数
	PhasePlayerTurn     Phase = "player-turn"     // 轮到玩家报数
	PhaseAttack         Phase = "attack"          // Boss 全中，等格挡结果
	PhaseSwap           Phase = "swap"            // 格挡成功，玩家可换数
	PhaseEnded          Phase = "ended"
)

// Outcome 终局结局。结算规则：胜=本局金币翻倍，负与投降=原额。
type Outcome string

const (
	OutcomeWin       Outcome = "win"
	OutcomeLose      Outcome = "lose"
	OutcomeSurrender Outcome = "surrender"
)

// Event 引擎对外发生的全部事实：数据不含台词——挑战者人格的口吻
// 由台词层（T4）基于这些事件生成。
type Event struct {
	Kind       string  `json:"kind"` // battle-started / guess-result / attack / reveal / swap-accepted / swap-declined / battle-ended
	Turn       string  `json:"turn,omitempty"`
	Guess      string  `json:"guess,omitempty"`
	Hits       int     `json:"hits,omitempty"`
	Position   int     `json:"position,omitempty"` // 亮位：第几位（1–3）
	Digit      int     `json:"digit,omitempty"`
	Outcome    Outcome `json:"outcome,omitempty"`
	Coins      int     `json:"coins,omitempty"`       // 本局原额
	FinalCoins int     `json:"final_coins,omitempty"` // 结算额
}

// Battle 一场 Boss 战的完整状态。非并发安全：hub 侧串行调用。
type Battle struct {
	rng          *rand.Rand
	runCoins     int
	playerSecret Secret
	bossSecret   Secret
	candidates   []Secret    // Boss 对玩家密数的剩余候选集（它的全部知识）
	revealed     map[int]int // 已亮出的位（0-based 下标 → 数字）
	phase        Phase
}

// NewBattle 开一场新对决：Boss 密数内部生成（不暴露），runCoins 为本局跑酷原额。
func NewBattle(rng *rand.Rand, runCoins int) *Battle {
	return newBattleWithSecret(rng, genSecret(rng), runCoins)
}

func newBattleWithSecret(rng *rand.Rand, bossSecret Secret, runCoins int) *Battle {
	return &Battle{
		rng:        rng,
		bossSecret: bossSecret,
		runCoins:   runCoins,
		revealed:   map[int]int{},
		phase:      PhaseAwaitingSecret,
	}
}

func (b *Battle) Phase() Phase { return b.phase }

// ReportSecret 玩家上报密数，开战。之后轮到玩家先猜。
func (b *Battle) ReportSecret(s Secret) ([]Event, error) {
	if b.phase != PhaseAwaitingSecret {
		return nil, phaseErr(b.phase)
	}
	if !s.Valid() {
		return nil, fmt.Errorf("密数必须是三个不重复的数字")
	}
	b.playerSecret = s
	b.candidates = allSecrets()
	b.phase = PhasePlayerTurn
	return []Event{{Kind: "battle-started"}}, nil
}

// PlayerGuess 玩家报数。未全中则 Boss 立即回合一报；全中即胜。
func (b *Battle) PlayerGuess(s Secret) ([]Event, error) {
	if b.phase != PhasePlayerTurn {
		return nil, phaseErr(b.phase)
	}
	if !s.Valid() {
		return nil, fmt.Errorf("报数必须是三个不重复的数字")
	}
	h := hits(s, b.bossSecret)
	evs := []Event{{Kind: "guess-result", Turn: "player", Guess: s.String(), Hits: h}}
	if h == 3 {
		return append(evs, b.end(OutcomeWin)...), nil
	}
	return append(evs, b.bossTurn()...), nil
}

// bossTurn Boss 回合：熵贪心选报，按真实命中收紧候选集；全中则转入攻击。
func (b *Battle) bossTurn() []Event {
	guess := pickGuess(b.candidates)
	h := hits(guess, b.playerSecret)
	b.candidates = filterConsistent(b.candidates, guess, h)
	evs := []Event{{Kind: "guess-result", Turn: "boss", Guess: guess.String(), Hits: h}}
	if h == 3 {
		b.phase = PhaseAttack
		return append(evs, Event{Kind: "attack"})
	}
	b.phase = PhasePlayerTurn
	return evs
}

// ReportBlock 上报格挡结果（QTE 由浏览器本地判定）。失败=判负；
// 成功=亮出一位未亮过的位，玩家获得换数机会。
func (b *Battle) ReportBlock(blocked bool) ([]Event, error) {
	if b.phase != PhaseAttack {
		return nil, phaseErr(b.phase)
	}
	if !blocked {
		return b.end(OutcomeLose), nil
	}
	var evs []Event
	if pos, digit, ok := b.revealOne(); ok {
		evs = append(evs, Event{Kind: "reveal", Position: pos, Digit: digit})
	}
	b.phase = PhaseSwap
	return evs, nil
}

func (b *Battle) revealOne() (position, digit int, ok bool) {
	var unrevealed []int
	for i := 0; i < 3; i++ {
		if _, seen := b.revealed[i]; !seen {
			unrevealed = append(unrevealed, i)
		}
	}
	if len(unrevealed) == 0 {
		return 0, 0, false
	}
	i := unrevealed[b.rng.Intn(len(unrevealed))]
	b.revealed[i] = b.bossSecret[i]
	return i + 1, b.bossSecret[i], true
}

// SwapSecret 换数：Boss 候选集重置——它对旧密数积累的知识清零。
func (b *Battle) SwapSecret(s Secret) ([]Event, error) {
	if b.phase != PhaseSwap {
		return nil, phaseErr(b.phase)
	}
	if !s.Valid() {
		return nil, fmt.Errorf("新密数必须是三个不重复的数字")
	}
	b.playerSecret = s
	b.candidates = allSecrets()
	b.phase = PhasePlayerTurn
	return []Event{{Kind: "swap-accepted"}}, nil
}

// DeclineSwap 放弃换数：候选集不变（只剩旧密数本身），下回合 Boss 必再全中。
func (b *Battle) DeclineSwap() ([]Event, error) {
	if b.phase != PhaseSwap {
		return nil, phaseErr(b.phase)
	}
	b.phase = PhasePlayerTurn
	return []Event{{Kind: "swap-declined"}}, nil
}

// Surrender 投降：Boss 战立即判负。
func (b *Battle) Surrender() ([]Event, error) {
	if b.phase == PhaseEnded {
		return nil, phaseErr(b.phase)
	}
	return b.end(OutcomeSurrender), nil
}

func (b *Battle) end(o Outcome) []Event {
	b.phase = PhaseEnded
	final := b.runCoins
	if o == OutcomeWin {
		final *= 2
	}
	return []Event{{Kind: "battle-ended", Outcome: o, Coins: b.runCoins, FinalCoins: final}}
}

func phaseErr(p Phase) error {
	return fmt.Errorf("当前阶段（%s）不接受这个动作", p)
}

// pickGuess 信息熵贪心：选让命中数分布最均衡的报数（整数化的熵最大化——
// 逐位比较降序桶大小，越均衡越优）；并列取数值序，保证确定性可复现。
func pickGuess(cands []Secret) Secret {
	best := cands[0]
	bestBuckets := hitBuckets(cands, best)
	for _, g := range cands[1:] {
		bk := hitBuckets(cands, g)
		if moreInformative(bk, bestBuckets) {
			best, bestBuckets = g, bk
		}
	}
	return best
}

func hitBuckets(cands []Secret, g Secret) [4]int {
	var bk [4]int
	for _, c := range cands {
		bk[hits(g, c)]++
	}
	return bk
}

// moreInformative：降序桶逐位比较，小者胜（最大桶越小 = 分割越均衡 = 期望信息量越大）。
func moreInformative(a, b [4]int) bool {
	as, bs := a, b
	sort.Sort(sort.Reverse(sort.IntSlice(as[:])))
	sort.Sort(sort.Reverse(sort.IntSlice(bs[:])))
	for i := range as {
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return false
}

// filterConsistent 只保留与已观察到的（报数, 命中）一致的候选——真密数必在其中。
func filterConsistent(cands []Secret, guess Secret, h int) []Secret {
	out := make([]Secret, 0, len(cands))
	for _, c := range cands {
		if hits(guess, c) == h {
			out = append(out, c)
		}
	}
	return out
}

func allSecrets() []Secret {
	out := make([]Secret, 0, 720)
	for a := 0; a < 10; a++ {
		for b := 0; b < 10; b++ {
			for c := 0; c < 10; c++ {
				if s := (Secret{a, b, c}); s.Valid() {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// Secret 三位不重复数字。允许前导零（玩家上报口径；Boss 生成时首位非零）。
type Secret [3]int

func (s Secret) String() string { return fmt.Sprintf("%d%d%d", s[0], s[1], s[2]) }

// Valid 校验三个不重复的个位数字。
func (s Secret) Valid() bool {
	var seen [10]bool
	for _, d := range s {
		if d < 0 || d > 9 || seen[d] {
			return false
		}
		seen[d] = true
	}
	return true
}

// hits 位数判定：只算数字与位置都对（bulls），0–3（spec 2026-10-23 裁决）。
func hits(guess, secret Secret) int {
	n := 0
	for i := range guess {
		if guess[i] == secret[i] {
			n++
		}
	}
	return n
}

// genSecret 生成 Boss 密数：不重复三位数、首位非零（报数口语自然）。
func genSecret(rng *rand.Rand) Secret {
	for {
		s := Secret{1 + rng.Intn(9), rng.Intn(10), rng.Intn(10)}
		if s.Valid() {
			return s
		}
	}
}
