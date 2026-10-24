package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// 按灵矽第三方接入协议验证 SSE 流：session_id 原样回带、
// chat 消息带 answer、以 message_end 收尾。
func TestHandleLinxChatSSE(t *testing.T) {
	hub := NewGameHub(NewBrain(BrainConfig{}))

	req := httptest.NewRequest("POST", "/api/chat/stream", strings.NewReader(
		`{"auth":{"device_id":"98:a3:16:f9:d9:35"},"type":"chat","details":{"msg":"开始游戏","session_id":"sess_1","timestamp":1}}`))
	rec := httptest.NewRecorder()
	hub.HandleLinxChat(rec, req)

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q, want text/event-stream", ct)
	}

	var events []map[string]any
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var ev map[string]any
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
			t.Fatalf("bad sse payload %q: %v", line, err)
		}
		events = append(events, ev)
	}

	if len(events) < 2 {
		t.Fatalf("want >= 2 events, got %d: %v", len(events), events)
	}
	for _, ev := range events {
		if ev["session_id"] != "sess_1" {
			t.Fatalf("session_id not echoed: %v", ev)
		}
	}
	first := events[0]
	if first["event"] != "message" || first["type"] != "chat" {
		t.Fatalf("first event should be chat message, got %v", first)
	}
	if last := events[len(events)-1]; last["event"] != "message_end" {
		t.Fatalf("must end with message_end, got %v", last)
	}
	if answer := first["details"].(map[string]any)["answer"].(string); answer == "" {
		t.Fatal("empty answer")
	}
}

func TestClassify(t *testing.T) {
	cases := map[string]intent{
		"开始":      intentStart,
		"来一局吧":    intentStart,
		"换一张地图":   intentNewLevel,
		"看看我的成绩":  intentReport,
		"换个造型":     intentChangeSkin,
		"今天天气怎么样": intentOther,
	}
	for msg, want := range cases {
		if got := classify(msg); got != want {
			t.Errorf("classify(%q) = %v, want %v", msg, got, want)
		}
	}
}
