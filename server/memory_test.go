package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// 假灵矽记忆 API：校验路径（按 memory_id 与设备 MAC 归档）、鉴权头、摘要内容。
func TestMemoryClientWritesRunSummary(t *testing.T) {
	var mu sync.Mutex
	var gotPath, gotAuth, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		gotPath, gotAuth, gotBody = r.URL.Path, r.Header.Get("Authorization"), string(b)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	m := &MemoryClient{BaseURL: srv.URL, APIKey: "k-test", MemoryID: "mem-1", MAC: "98:a3:16:f9:d9:35"}
	if !m.Enabled() {
		t.Fatal("已配置凭据应启用")
	}
	if err := m.WriteRun(context.Background(), "距离 300 米，摔死 2 次，金币 60，Boss 战：胜（金币翻倍）"); err != nil {
		t.Fatalf("写入失败: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if want := "/v1/memories/mem-1/identifiers/98:a3:16:f9:d9:35/nodes"; gotPath != want {
		t.Fatalf("path = %s, want %s", gotPath, want)
	}
	if gotAuth != "Bearer k-test" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
	if !strings.Contains(gotBody, "300 米") || !strings.Contains(gotBody, "金币翻倍") {
		t.Fatalf("content 应携带对局摘要字段: %s", gotBody)
	}
}

// 未配置凭据（demo 常态）：无操作直接成功，不报错不阻塞。
func TestMemoryClientDisabledWithoutConfig(t *testing.T) {
	m := &MemoryClient{}
	if m.Enabled() {
		t.Fatal("未配置不应启用")
	}
	if err := m.WriteRun(context.Background(), "任何摘要"); err != nil {
		t.Fatalf("未启用应为无操作: %v", err)
	}
}

// 灵矽侧故障只返回错误，由调用方记日志——不影响游戏主流程。
func TestMemoryClientServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	m := &MemoryClient{BaseURL: srv.URL, APIKey: "k", MemoryID: "m", MAC: "aa"}
	done := make(chan error, 1)
	go func() { done <- m.WriteRun(context.Background(), "x") }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("5xx 应返回错误")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("写入应有时限，不能挂死")
	}
}
