package boss

import "testing"

// 口语变体 → 同一密数：逐位读法、阿拉伯、带量词的位置读法、混写、分隔符、前缀动词。
func TestRouteInputParsesGuessVariants(t *testing.T) {
	cases := []struct{ text, want string }{
		{"三七八", "378"},
		{"378", "378"},
		{"三百七十八", "378"},
		{"3七8", "378"},
		{"0 1 2", "012"},
		{"零一二", "012"}, // 前导零：玩家口径
		{"两三四", "234"}, // 两=2
		{"三-七-八", "378"},
		{"报数 三七八", "378"},
		{"我猜378", "378"},
		{"密数是四八三", "483"}, // 密数上报与报数共用同一解析
		{"三百零八", "308"},
		{"一百二十三", "123"},
		{"三百五", "350"},   // 口语省“十”：三百五=350
		{"幺三八", "138"},   // 电话/军语逐字读法
		{"三　七　八", "378"}, // 全角空格分隔（中文 STT 高频）
		{"五五五", "555"},   // 报数允许重复数字（密数才要求不重复）
		{"113", "113"},
	}
	for _, c := range cases {
		in, err := RouteInput(c.text)
		if err != nil {
			t.Errorf("RouteInput(%q) 意外报错: %v", c.text, err)
			continue
		}
		if in.Guess.String() != c.want || in.Intent != IntentNone {
			t.Errorf("RouteInput(%q) = %+v, want guess %s 且无意图", c.text, in, c.want)
		}
	}
}

// 携带数字但位数不对：拒绝（错误可直接 TTS），不落入关键词。
func TestRouteInputRejectsInvalidGuesses(t *testing.T) {
	cases := []string{"九九", "十二", "3", "1234"}
	for _, text := range cases {
		in, err := RouteInput(text)
		if err == nil {
			t.Errorf("RouteInput(%q) 应拒绝非法报数，got %+v", text, in)
		}
		if in.Guess != (Secret{}) || in.Intent != IntentNone {
			t.Errorf("RouteInput(%q) 拒绝时不应携带结果，got %+v", text, in)
		}
	}
}

// 不带数字的语句：零值放行（闲聊交给上层），不误报非法报数。
func TestRouteInputPassesThroughNonNumberStatements(t *testing.T) {
	for _, text := range []string{"今天天气怎么样", "你好小智"} {
		in, err := RouteInput(text)
		if err != nil || in != (Input{}) {
			t.Errorf("RouteInput(%q) = (%+v, %v), want 零值放行", text, in, err)
		}
	}
}

// 「十分」含「十」但不是报数——位置读法解析失败且无数字时须放行走关键词。
func TestPositionalProseFallsToIntent(t *testing.T) {
	in, err := RouteInput("我十分想认输")
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if in.Intent != IntentSurrender {
		t.Fatalf("应识别为投降，got %+v", in)
	}
}

func TestRouteInputIntents(t *testing.T) {
	cases := []struct {
		text string
		want Intent
	}{
		{"投降", IntentSurrender},
		{"我认输了", IntentSurrender},
		{"我输了", IntentSurrender},
		{"不想玩了", IntentSurrender}, // 口语“想”字隔断，靠“不想玩”命中
		{"我不想玩了", IntentSurrender},
		{"换个数", IntentSwap},
		{"换一个数字", IntentSwap},
		{"重新报", IntentSwap},
		{"重新来一个数", IntentSwap},
		{"再来一个数", IntentSwap},
		{"不换了", IntentDeclineSwap},
		{"别换了", IntentDeclineSwap},
		{"算了", IntentDeclineSwap},
		{"放弃", IntentDeclineSwap},
		{"放弃换数", IntentDeclineSwap}, // 同时含“换数”，放弃优先
	}
	for _, c := range cases {
		in, err := RouteInput(c.text)
		if err != nil || in.Intent != c.want || in.Guess != (Secret{}) {
			t.Errorf("RouteInput(%q) = (%+v, %v), want intent %d", c.text, in, err, c.want)
		}
	}
}

// Boss 阶段对跑酷指令装聋：开始/换图/成绩在这里必须落空（路由顺序由上层 hub 保证先走本函数）。
func TestBossStageIgnoresRunIntents(t *testing.T) {
	for _, text := range []string{"开始游戏", "来一局", "换一张地图", "看看我的成绩"} {
		in, err := RouteInput(text)
		if err != nil || in != (Input{}) {
			t.Errorf("RouteInput(%q) = (%+v, %v), Boss 阶段应忽略跑酷指令", text, in, err)
		}
	}
}
