// Boss 阶段语音输入的解析与路由：中文数字口语变体 → 密数，关键词 → 意图。
// 纯函数，不碰 WS。
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

// RouteInput 统一路由 Boss 阶段的 STT 文本：先数字解析、再关键词；
// 携带数字但不构成合法报数时返回可直接 TTS 的中文拒绝。
func RouteInput(text string) (Input, error) {
	s := stripSeparators(stripLeadWords(strings.TrimSpace(text)))
	if s == "" {
		return Input{}, nil
	}
	if !isGuessAttempt(s) {
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

// isGuessAttempt 判断语句是否构成一次报数尝试。
// 中文「一」常作量词（换一个数字/来一局），孤立的中文数字不能算；
// 只有阿拉伯数字、「十/百」量词或连续两位以上中文数字才算数。
func isGuessAttempt(s string) bool {
	run, maxRun := 0, 0
	for _, r := range s {
		switch {
		case isArabicDigit(r):
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

// 全角空格是中文 STT 的高频分隔（逐字报数「三　七　八」），漏掉会被当闲聊。
var separators = strings.NewReplacer(
	" ", "", "　", "", "\u00a0", "", "-", "", "—", "",
	"、", "", ",", "", "，", "", "·", "",
)

func stripSeparators(s string) string { return separators.Replace(s) }

// 解析层只管凑齐三位数字；重复与否由用途定——报数允许重复（引擎
// GuessValid），密数/换数在引擎侧按 Valid 拒绝并给出各自提示。
var errWrongCount = fmt.Errorf("报数要报三位数字，一个一个报，比如三七八")

func secretFromDigits(ds []int) (Input, error) {
	if len(ds) != 3 {
		return Input{}, errWrongCount
	}
	return Input{Guess: Secret{ds[0], ds[1], ds[2]}}, nil
}

// keywords 关键词意图。「放弃换数」同时命中换数词表，放弃语义须优先判定。
func keywords(s string) Input {
	for _, kw := range []string{"投降", "认输", "我输了", "不玩了", "不想玩"} {
		if strings.Contains(s, kw) {
			return Input{Intent: IntentSurrender}
		}
	}
	for _, kw := range []string{"不换", "别换", "不用了", "放弃", "算了", "就这个"} {
		if strings.Contains(s, kw) {
			return Input{Intent: IntentDeclineSwap}
		}
	}
	for _, kw := range []string{"换数", "换个数", "换一个数", "换个数字", "换一个数字", "重新报", "来一个数"} {
		if strings.Contains(s, kw) {
			return Input{Intent: IntentSwap}
		}
	}
	return Input{}
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

// leadWords 报数/密数上报语句的口语前缀。
var leadWords = []string{
	"我的密数是", "密数是", "密数", "答案是", "答案",
	"我猜", "我报", "报数", "换个说法", "报", "猜", "是",
}

var zhDigits = map[rune]int{
	'零': 0, '〇': 0, '一': 1, '二': 2, '两': 2, '三': 3, '四': 4,
	'五': 5, '六': 6, '七': 7, '八': 8, '九': 9,
	'幺': 1, // 电话/军语逐字读法（幺三八=138）
}

func isArabicDigit(r rune) bool {
	return r >= '0' && r <= '9' || r >= '０' && r <= '９' // 半角与全角
}

func digitVal(r rune) (int, bool) {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0'), true
	case r >= '０' && r <= '９':
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
