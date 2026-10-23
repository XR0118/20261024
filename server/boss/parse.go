// Boss 阶段语音输入的解析与路由：中文数字口语变体 → Secret，
// 关键词 → 意图。纯函数，不碰 WS——hub（T3）在 Boss 阶段先调这里，
// 落空才轮到跑酷指令的关键词路由。
package boss

import (
	"fmt"
	"strings"
)

// Intent Boss 阶段关键词意图。
type Intent int

const (
	IntentNone        Intent = iota
	IntentSurrender          // 投降
	IntentSwap               // 要求换数
	IntentDeclineSwap        // 放弃换数机会
)

// Input Boss 阶段一条 STT 文本的路由结果：Guess 与 Intent 至多一个非零。
type Input struct {
	Guess  Secret
	Intent Intent
}

// RouteInput Boss 阶段语音输入的统一路由（spec 顺序：先数字解析、再关键词）：
//   - 携带数字且构成三位不重复报数 → Guess
//   - 携带数字但非法（位数不对/数字重复）→ 错误（中文，可直接 TTS）
//   - 不带数字 → 关键词意图；都不是则零值放行（闲聊交给上层人格）
//
// 中文里「一」常作量词（换一个数字/来一局），因此报数尝试的判定
// 区分阿拉伯数字、连续中文数字与「十/百」量词，单个中文数字不成句不算。
func RouteInput(text string) (Input, error) {
	s := stripSeparators(stripLeadWords(strings.TrimSpace(text)))
	if s == "" {
		return Input{}, nil
	}
	if !numberAttempt(s) {
		return keywords(s), nil
	}
	if strings.ContainsAny(s, "百十") {
		if n, ok := parsePositional(s); ok {
			if n < 100 {
				return Input{}, errWrongCount // 「十二」之类不足三位，不转成前导零报数
			}
			return secretFromDigits([]int{n / 100, n / 10 % 10, n % 10})
		}
		// 位置读法失败但确带数字（如「3十8」）：退回逐位提取
	}
	var ds []int
	for _, r := range s {
		if v, ok := digitVal(r); ok {
			ds = append(ds, v)
		}
	}
	if len(ds) == 0 {
		return keywords(s), nil // 「我十分想认输」：位置读法失败且无数字，放行走关键词
	}
	return secretFromDigits(ds)
}

// numberAttempt 判断语句是否构成一次报数尝试：
// 阿拉伯数字或「十/百」出现即算；中文数字需连续两位以上（或独字成句）。
func numberAttempt(s string) bool {
	run, maxRun := 0, 0
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= '０' && r <= '９':
			return true
		case r == '百' || r == '十':
			return true
		default:
			if _, ok := zhDigits[r]; ok {
				run++
				if run > maxRun {
					maxRun = run
				}
				continue
			}
			run = 0
		}
	}
	if maxRun >= 2 {
		return true
	}
	return maxRun == 1 && len([]rune(s)) == 1
}

var separators = strings.NewReplacer(
	" ", "", "-", "", "—", "", "、", "", ",", "", "，", "", "·", "",
)

func stripSeparators(s string) string { return separators.Replace(s) }

var errWrongCount = fmt.Errorf("报数要报三个不重复的数字，一个一个报，比如三七八")

func secretFromDigits(ds []int) (Input, error) {
	if len(ds) != 3 {
		return Input{}, errWrongCount
	}
	s := Secret{ds[0], ds[1], ds[2]}
	if !s.Valid() {
		return Input{}, fmt.Errorf("三个数字不能重复，重新报")
	}
	return Input{Guess: s}, nil
}

// keywords 关键词意图。先查投降，再查放弃换数（「放弃换数」同时命中
// 换数，放弃语义优先），最后换数。
func keywords(s string) Input {
	for _, kw := range []string{"投降", "认输", "我输了", "不玩了"} {
		if strings.Contains(s, kw) {
			return Input{Intent: IntentSurrender}
		}
	}
	for _, kw := range []string{"不换", "不用了", "放弃", "算了", "就这个"} {
		if strings.Contains(s, kw) {
			return Input{Intent: IntentDeclineSwap}
		}
	}
	for _, kw := range []string{"换数", "换个数", "换一个数", "换个数字", "换一个数字", "重新报"} {
		if strings.Contains(s, kw) {
			return Input{Intent: IntentSwap}
		}
	}
	return Input{}
}

// stripLeadWords 剥掉「我猜/报数/密数是」这类口语前缀，循环到剥不动为止。
var leadWords = []string{
	"我的密数是", "密数是", "密数", "答案是", "答案",
	"我猜", "我报", "报数", "换个说法", "报", "猜", "是",
}

func stripLeadWords(s string) string {
	changed := true
	for changed {
		changed = false
		for _, w := range leadWords {
			if t := strings.TrimSpace(strings.TrimPrefix(s, w)); t != s {
				s, changed = t, true
				break
			}
		}
	}
	return s
}

var zhDigits = map[rune]int{
	'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
	'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
}

func digitVal(r rune) (int, bool) {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0'), true
	case r >= '０' && r <= '９': // 全角
		return int(r - '０'), true
	default:
		v, ok := zhDigits[r]
		return v, ok
	}
}

// parsePositional 位置读法：[一-九]百（[一-九]十[一-九]? | 零[一-九] | [一-九]）?
// 覆盖「三百七十八/三百零八/一百二十三/三百五（省十）」；任何不匹配返回 false。
func parsePositional(s string) (int, bool) {
	rs := []rune(s)
	i := 0
	read := func() (int, bool) {
		if i < len(rs) {
			if v, ok := digitVal(rs[i]); ok {
				i++
				return v, true
			}
		}
		return 0, false
	}

	h, ok := read()
	if !ok || h == 0 || i >= len(rs) || rs[i] != '百' {
		return 0, false
	}
	i++

	tens, units := 0, 0
	if i < len(rs) {
		if rs[i] == '零' {
			i++
			u, ok := read()
			if !ok || i != len(rs) {
				return 0, false
			}
			units = u
		} else {
			t, ok := read()
			if !ok || t == 0 {
				return 0, false
			}
			if i < len(rs) && rs[i] == '十' {
				i++
				tens = t * 10
				if u, ok := read(); ok {
					units = u
				}
			} else {
				tens = t * 10 // 三百五：省“十”的口语，裸数字当十位
			}
			if i != len(rs) {
				return 0, false
			}
		}
	}
	return h*100 + tens + units, true
}
