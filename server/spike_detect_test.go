package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// 一次性 spike（#16）：实连灵矽验证两件事——
//  1. listen detect 仅带 text（无 text_response）是否触发平台 LLM 回复
//  2. detect 带 text_response 是否不经 LLM 直发 TTS
// 运行：LINGX_SPIKE=1 go test -run SpikeDetect -v ；结论记录进 issue 后删除本文件。
func TestSpikeDetect(t *testing.T) {
	if os.Getenv("LINGX_SPIKE") == "" {
		t.Skip("set LINGX_SPIKE=1 to run the live lingxi spike")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
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
	var ttsFrames int64
	go func() {
		for {
			mt, data, err := conn.Read(context.Background())
			if err != nil {
				return
			}
			switch mt {
			case websocket.MessageBinary:
				n := atomic.AddInt64(&ttsFrames, 1)
				if n == 1 {
					fmt.Printf("SPIKE: 收到首个二进制 TTS 帧（%dB）\n", len(data))
				}
			case websocket.MessageText:
				fmt.Printf("SPIKE: text msg: %s\n", data)
				var m struct {
					Type      string `json:"type"`
					SessionID string `json:"session_id"`
				}
				if json.Unmarshal(data, &m) == nil && m.Type == "hello" && m.SessionID != "" {
					select {
					case sessCh <- m.SessionID:
					default:
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
		fmt.Printf("SPIKE: session=%s\n", sid)
	case <-time.After(10 * time.Second):
		t.Fatal("10s 未收到 hello/session")
	}

	// 实验 1：detect 仅带 text（事件标记），观察是否触发 LLM 回复（期待 llm/stt/tts 之类下行）
	fmt.Println("SPIKE: === 实验1 detect 仅带 text ===")
	send(map[string]any{
		"session_id": sid, "type": "listen", "state": "detect",
		"text": "【系统自检】请回一句话确认你能听见",
	})
	time.Sleep(10 * time.Second)
	f1 := atomic.LoadInt64(&ttsFrames)
	fmt.Printf("SPIKE: 实验1 结束，累计 TTS 帧 %d\n", f1)

	// 实验 2：detect 带 text_response，观察是否直发 TTS（期待二进制帧、且无 llm 介入痕迹）
	fmt.Println("SPIKE: === 实验2 detect 带 text_response ===")
	send(map[string]any{
		"session_id": sid, "type": "listen", "state": "detect",
		"text": "boss_event", "text_response": "这是直发台词测试，挑战者向你问好。",
	})
	time.Sleep(10 * time.Second)
	f2 := atomic.LoadInt64(&ttsFrames)
	fmt.Printf("SPIKE: 实验2 结束，累计 TTS 帧 %d（新增 %d）\n", f2, f2-f1)
}
