package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// MemoryClient 把对局摘要写入灵矽教练智能体的长期记忆（ADR-0003 决策 3）：
// identify_id=设备 MAC 归档，教练复盘时平台 LLM 自动取用。
// 凭据（LINGX_API_KEY / LINGX_MEMORY_ID）未配置时 Enabled=false，
// 写入为无操作；失败只返回错误、由调用方记日志，绝不影响游戏主流程。
type MemoryClient struct {
	BaseURL  string // 灵矽平台 API 地址
	APIKey   string // 智控台 API-KEY
	MemoryID string // 教练智能体的记忆容器 ID
	MAC      string // 归档标识：设备 MAC

	initMu sync.Mutex
	client *http.Client
}

func (m *MemoryClient) Enabled() bool {
	return m.APIKey != "" && m.MemoryID != ""
}

// httpClient 懒初始化加锁：每次局终都起独立 goroutine 写入，
// 相邻两局可能在途并发。
func (m *MemoryClient) httpClient() *http.Client {
	m.initMu.Lock()
	defer m.initMu.Unlock()
	if m.client == nil {
		m.client = &http.Client{Timeout: 5 * time.Second}
	}
	return m.client
}

// WriteRun 写入一段对局摘要（≤10000 字符，接口约束）。
func (m *MemoryClient) WriteRun(ctx context.Context, summary string) error {
	if !m.Enabled() {
		return nil
	}
	body, _ := json.Marshal(map[string]any{"content": summary})
	url := fmt.Sprintf("%s/v1/memories/%s/identifiers/%s/nodes", m.BaseURL, m.MemoryID, m.MAC)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.APIKey)
	resp, err := m.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("memory api status %d", resp.StatusCode)
	}
	return nil
}
