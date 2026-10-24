package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReportRunAndLeaderboard(t *testing.T) {
	s, err := LoadStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}

	s.ReportRun("a", "阿福", 100, 3)
	s.ReportRun("a", "阿福", 80, 2) // 距离取 max，金币累加
	s.ReportRun("b", "小Bar", 120, 1)

	top, second := s.Leaderboard(10)[0], s.Leaderboard(10)[1]
	if top.ID != "b" || top.BestDist != 120 {
		t.Fatalf("小Bar(120) 应排第一，got %+v", top)
	}
	if second.ID != "a" || second.BestDist != 100 || second.TotalCoins != 5 || second.Runs != 2 {
		t.Fatalf("阿福应为 best=100 coins=5 runs=2，got %+v", second)
	}
}

func TestStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s1, _ := LoadStore(path)
	s1.ReportRun("p", "持久", 42, 7)

	// 重新加载（模拟服务重启/页面关闭后）
	s2, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	got := s2.Leaderboard(10)
	if len(got) != 1 || got[0].TotalCoins != 7 {
		t.Fatalf("数据应持久化，got %+v", got)
	}
}

func TestReportRunCreditsBalance(t *testing.T) {
	s, err := LoadStore(filepath.Join(t.TempDir(), "data.json"))
	if err != nil {
		t.Fatal(err)
	}
	s.ReportRun("a", "阿福", 100, 3)
	s.ReportRun("a", "阿福", 80, 2)

	if got := s.WalletOf("a").Balance; got != 5 {
		t.Fatalf("入账应同加余额：期望 5，got %d", got)
	}
}

func TestLoadLegacyDataHasZeroBalance(t *testing.T) {
	// 旧版 data.json 无 balance 字段：升级后应可加载且余额为 0（共识：存量不折算）
	path := filepath.Join(t.TempDir(), "data.json")
	const legacy = `{"old": {"id": "old", "name": "老玩家", "best_dist": 300, "total_coins": 999, "runs": 10}}`
	if err := os.WriteFile(path, []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadStore(path)
	if err != nil {
		t.Fatalf("旧数据应可加载: %v", err)
	}
	if got := s.WalletOf("old").Balance; got != 0 {
		t.Fatalf("存量不折算，余额应为 0，got %d", got)
	}
}

// 造型商店持久化：默认 bean、购买扣费只减余额、切换与已购跨重启保留
func TestStoreSkins(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, _ := LoadStore(path)

	st := s.WalletOf("p")
	if st.Active != "bean" || st.Balance != 0 || len(st.Owned) != 1 || st.Owned[0] != "bean" {
		t.Fatalf("want default bean/balance 0, got %+v", st)
	}

	s.ReportRun("p", "阿福", 10, 30)
	if err := s.BuySkin("p", "wave", 20); err != nil {
		t.Fatalf("buy: %v", err)
	}
	if st := s.WalletOf("p"); st.Balance != 10 || st.TotalCoins != 30 || st.Active != "wave" {
		t.Fatalf("buy should deduct balance only and wear it: %+v", st)
	}
	if err := s.BuySkin("p", "wave", 20); err == nil {
		t.Fatal("repeat buy should fail")
	}
	if err := s.BuySkin("p", "bean", 20); err == nil {
		t.Fatal("buying free default should fail")
	}

	if err := s.SwitchSkin("p", "bean"); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if err := s.SwitchSkin("q", "wave"); err == nil {
		t.Fatal("switching to unowned should fail")
	}

	s2, _ := LoadStore(path)
	st2 := s2.WalletOf("p")
	if st2.Active != "bean" || st2.Balance != 10 || len(st2.Owned) != 2 {
		t.Fatalf("want bean/10/2 skins after reload, got %+v", st2)
	}
}
