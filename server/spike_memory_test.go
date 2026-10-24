package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// 记忆检索 spike（#19）：以设备身份 detect+text 问教练历史战绩，
// 观察回复是否引用已写入的记忆节点（identify_id=设备 MAC 的检索键验证）。
// 运行：LINGX_SPIKE=1 go test -run SpikeMemoryRecall -v（需先写好测试节点）。
func TestSpikeMemoryRecall(t *testing.T) {
	if os.Getenv("LINGX_SPIKE") == "" {
		t.Skip("set LINGX_SPIKE=1 to run the live memory recall spike")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	const mac = "98:a3:16:f9:d9:35"
	lc := NewLinxClient(mac)
	if err := lc.ota(ctx); err != nil {
		t.Fatalf("ota: %v", err)
	}
	conn, _, err := websocket.Dial(ctx, lc.wsURL, &websocket.DialOptions{
		HTTPHeader: map[string][]string{
			"Authorization":    {"Bearer " + lc.token},
			"Protocol-Version": {"1"},
			"Device-Id":        {mac},
			"Client-Id":        {mac},
		},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	sessCh := make(chan string, 1)
	go func() {
		for {
			mt, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			if mt == websocket.MessageText {
				var m struct {
					Type      string `json:"type"`
					SessionID string `json:"session_id"`
					State     string `json:"state"`
					Text      string `json:"text"`
				}
				if json.Unmarshal(data, &m) == nil {
					switch {
					case m.Type == "hello" && m.SessionID != "":
						select {
						case sessCh <- m.SessionID:
						default:
						}
					case m.Type == "tts" && m.State == "sentence_start":
						fmt.Printf("SPIKE: 教练说：%s\n", m.Text)
					}
				}
			}
		}
	}()

	send := func(v any) {
		b, _ := json.Marshal(v)
		if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatalf("send: %v", err)
		}
	}
	send(map[string]any{
		"type": "hello", "version": 1, "transport": "websocket",
		"audio_params": map[string]any{"format": "opus", "sample_rate": 16000, "channels": 1, "frame_duration": 60},
	})
	send(map[string]any{"type": "listen", "state": "start", "mode": "auto"})

	var sid string
	select {
	case sid = <-sessCh:
	case <-time.After(10 * time.Second):
		t.Fatal("10s 未收到 hello/session")
	}
	send(map[string]any{"session_id": sid, "type": "listen", "state": "detect", "text": "我的历史战绩怎么样"})
	time.Sleep(25 * time.Second)
}
