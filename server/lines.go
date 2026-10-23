// 挑战者台词层（#16）：引擎事件 → 语音台词。
// 风格对齐 docs/prompt-persona.md 的挑战者人格（傲慢、戏精、嘴硬）。
// 分两条通道（spike 实测结论；对 ADR-0003 决策 2 的显式收窄见 ADR 修订备注）：
//   - exact=true：本地模板 + text_response 逐字直发——数字/胜负零失真、
//     延迟最低（关键信息事件）；
//   - exact=false：返回【】事件标记，detect+text 交平台 LLM 人格发挥
//     （纯氛围事件，ADR-0003 决策 2 的原设计路径）。
package main

import (
	"fmt"

	"loudleap/server/boss"
)

var digitCn = [...]string{"零", "一", "二", "三", "四", "五", "六", "七", "八", "九"}

var hitsCn = [...]string{"零", "一", "两", "三"}

// speakDigits 报数逐位转中文（378→三七八）——整读会被 TTS 念成「三百七十八」，
// 对猜数字玩法是致命的报数失真。
func speakDigits(s string) string {
	out := ""
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out += digitCn[r-'0']
		}
	}
	return out
}

// challengerLine 事件 → 台词或事件标记。ok=false 表示无话可说（不播）。
func challengerLine(e boss.Event) (line string, exact bool, ok bool) {
	switch e.Kind {
	case boss.KindBattleStarted:
		return "【BOSS 来了】玩家跑完全程，密数已定，Boss 战开始，你先猜", false, true
	case boss.KindGuessResult:
		if e.Turn == boss.TurnBoss {
			return fmt.Sprintf("听好了，我报：%s！", speakDigits(e.Guess)), true, true
		}
		if e.Hits < 0 || e.Hits >= len(hitsCn) {
			break // 越界值不应让台词层 panic
		}
		if e.Hits == 0 {
			return pick([]string{
				"零位！哈哈哈，你这直觉和你的嗓门一样，毫无准头！",
				"零位？我在数字里泡大的时候你还在学数数！",
				"一个都没中！你的运气被嗓子借走了吧？",
			}), true, true
		}
		return fmt.Sprintf("哼，猜中%s位，也就这样了。", hitsCn[e.Hits]), true, true
	case boss.KindAttack:
		return "全中！哈哈哈——接我这招！！", true, true
	case boss.KindReveal:
		if e.Position < 1 || e.Position > 3 || e.Digit < 0 || e.Digit > 9 {
			break
		}
		return fmt.Sprintf("算你走运！看好了，我的第%s位是%s。", digitCn[e.Position], digitCn[e.Digit]), true, true
	case boss.KindSwapAccepted:
		return "【换数】玩家换好了新密数，Boss 战继续，你先猜", false, true
	case boss.KindSwapDeclined:
		return "【换数】玩家放弃了换数机会，Boss 战继续", false, true
	case boss.KindBattleEnded:
		switch e.Outcome {
		case boss.OutcomeWin:
			return "不可能……我居然输了？！金币翻倍，算你狠！", true, true
		case boss.OutcomeLose:
			return "哈哈哈哈！格挡失败，回家练练嗓子再来吧！", true, true
		case boss.OutcomeSurrender:
			return "认输了？明智的选择。走吧，别回头。", true, true
		}
	}
	// hub 合成的功能性事件（awaiting-secret/swap-prompt/error）自带可播文案
	if e.Message != "" {
		return e.Message, true, true
	}
	return "", false, false
}
