package main

import (
	"strings"
	"testing"
)

// 商店状态机三分支：够钱自动购买穿上 / 余额不足播差额 / 已拥有切换
func TestShopDecide(t *testing.T) {
	shop := SkinShop{Price: 20}

	got := shop.Decide([]string{"bean"}, "bean", 25)
	if got.Kind != kindBought || got.Skin != "wave" || got.BalanceAfter != 5 {
		t.Fatalf("want bought wave balance-after 5, got %+v", got)
	}

	got = shop.Decide([]string{"bean"}, "bean", 8)
	if got.Kind != kindShort || got.Skin != "wave" || got.Short != 12 {
		t.Fatalf("want short of 12, got %+v", got)
	}

	got = shop.Decide([]string{"bean", "wave"}, "bean", 0)
	if got.Kind != kindSwitched || got.Skin != "wave" {
		t.Fatalf("want switched to wave, got %+v", got)
	}

	got = shop.Decide([]string{"bean", "wave"}, "wave", 0)
	if got.Kind != kindSwitched || got.Skin != "bean" {
		t.Fatalf("want switched to bean, got %+v", got)
	}
}

// 文案按分支携带关键信息：买了什么/还剩多少、差多少、换上了谁
func TestShopLine(t *testing.T) {
	shop := SkinShop{Price: 20}
	cases := []struct {
		outcome ShopOutcome
		must    []string
	}{
		{shop.Decide([]string{"bean"}, "bean", 25), []string{"声波精灵", "5"}},
		{shop.Decide([]string{"bean"}, "bean", 8), []string{"差", "12"}},
		{shop.Decide([]string{"bean", "wave"}, "wave", 0), []string{"跳跳豆"}},
	}
	for _, c := range cases {
		line := shopLine(c.outcome)
		for _, want := range c.must {
			if !strings.Contains(line, want) {
				t.Errorf("line %q missing %q", line, want)
			}
		}
	}
}
