package main

import "testing"

// detect 注入的台词会被平台回显为 stt——防回环必须一次性命中，
// 且不能误伤未注入过的真实玩家话语。
func TestEchoGuard(t *testing.T) {
	c := NewLinxClient("aa:bb")
	line := "听好了，我报：三七八！"
	c.guardEcho(line)
	if !c.isEcho(line) {
		t.Fatal("注入台词的回显应被识别")
	}
	if c.isEcho(line) {
		t.Fatal("防回环应一次性：二次不再命中（真人重复话语不受影响）")
	}
	if c.isEcho("我猜四六三") {
		t.Fatal("未注入过的文本不应误判为回显")
	}
}
