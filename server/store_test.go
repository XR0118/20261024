package main

import (
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
