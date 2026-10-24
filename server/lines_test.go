package main

import (
	"strings"
	"testing"

	"loudleap/server/boss"
)

// 事件全集都有台词；关键信息必须 exact（text_response 直发），氛围事件走 LLM。
func TestChallengerLineCoverage(t *testing.T) {
	cases := []struct {
		name  string
		ev    boss.Event
		exact bool
	}{
		{"开局", boss.Event{Kind: boss.KindBattleStarted}, false},
		{"玩家判定", boss.Event{Kind: boss.KindGuessResult, Turn: boss.TurnPlayer, Guess: "456", Hits: 1}, true},
		{"Boss 报数", boss.Event{Kind: boss.KindGuessResult, Turn: boss.TurnBoss, Guess: "378", Hits: 1}, true},
		{"攻击", boss.Event{Kind: boss.KindAttack}, true},
		{"亮位", boss.Event{Kind: boss.KindReveal, Position: 2, Digit: 8}, true},
		{"换数接受", boss.Event{Kind: boss.KindSwapAccepted}, false},
		{"换数放弃", boss.Event{Kind: boss.KindSwapDeclined}, false},
		{"玩家胜", boss.Event{Kind: boss.KindBattleEnded, Outcome: boss.OutcomeWin, Coins: 30, FinalCoins: 60}, true},
		{"格挡失败", boss.Event{Kind: boss.KindBattleEnded, Outcome: boss.OutcomeLose}, true},
		{"投降", boss.Event{Kind: boss.KindBattleEnded, Outcome: boss.OutcomeSurrender}, true},
		{"带提示的合成事件直用原文", boss.Event{Kind: "awaiting-secret", Message: "请报出你的三位密数"}, true},
		{"错误直用原文", boss.Event{Kind: "error", Message: "报数要报三个不重复的数字"}, true},
	}
	for _, c := range cases {
		line, exact, ok := challengerLine(c.ev)
		if !ok || line == "" {
			t.Errorf("%s: 应有台词，got %q ok=%v", c.name, line, ok)
			continue
		}
		if exact != c.exact {
			t.Errorf("%s: exact=%v, want %v（%q）", c.name, exact, c.exact, line)
		}
	}
}

// 报数必须逐位中文读法，TTS 才不会读成「三百七十八」。
func TestChallengerLineSpeaksDigitsVerbatim(t *testing.T) {
	line, _, ok := challengerLine(boss.Event{Kind: boss.KindGuessResult, Turn: boss.TurnBoss, Guess: "378", Hits: 1})
	if !ok {
		t.Fatal("Boss 报数应有台词")
	}
	if !strings.Contains(line, "三七八") || strings.Contains(line, "378") {
		t.Fatalf("Boss 报数须逐位中文（三七八），got %q", line)
	}
}

// 台词必须携带游戏事实：命中数、亮位的位置与数字。
func TestChallengerLineCarriesGameFacts(t *testing.T) {
	rline, _, _ := challengerLine(boss.Event{Kind: boss.KindReveal, Position: 2, Digit: 8})
	if !strings.Contains(rline, "第二位") || !strings.Contains(rline, "八") {
		t.Fatalf("亮位台词须含中文位置与数字，got %q", rline)
	}
	pline0, _, _ := challengerLine(boss.Event{Kind: boss.KindGuessResult, Turn: boss.TurnPlayer, Hits: 0})
	pline2, _, _ := challengerLine(boss.Event{Kind: boss.KindGuessResult, Turn: boss.TurnPlayer, Hits: 2})
	if !strings.Contains(pline0, "零位") && !strings.Contains(pline0, "一个都没中") {
		t.Fatalf("零命中台词须表达零命中（三句随机变体之一），got %q", pline0)
	}
	if !strings.Contains(pline2, "两") {
		t.Fatalf("两命中台词须含「两」，got %q", pline2)
	}
}

// 氛围事件给平台 LLM 的是【】事件标记（对齐 prompt-persona 词汇），不是成品台词。
func TestAtmosphereLinesAreEventMarkers(t *testing.T) {
	marker, _, ok := challengerLine(boss.Event{Kind: boss.KindBattleStarted})
	if !ok || !strings.HasPrefix(marker, "【") {
		t.Fatalf("开局应发事件标记给 LLM，got %q", marker)
	}
}
