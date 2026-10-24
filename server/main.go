package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
)

// 部署形态：灵矽云平台需要公网可达的回调地址，本地开发用内网穿透把
// 这个端口暴露出去，智控台里把智能体配置为第三方应用接入模式。
func main() {
	addr := flag.String("addr", ":8080", "HTTP 监听地址")
	mac := flag.String("linx-mac", "98:a3:16:f9:d9:35", "灵矽设备 MAC（智控台预注册绑定的那台）")
	flag.Parse()

	brain := NewBrain(BrainConfig{
		BaseURL: os.Getenv("LLM_BASE_URL"),
		APIKey:  os.Getenv("LLM_API_KEY"),
		Model:   os.Getenv("LLM_MODEL"),
	})
	hub := NewGameHub(brain)

	lc := NewLinxClient(*mac)
	hub.SetLinxClient(lc)
	go lc.Run(context.Background())

	// 教练复盘的记忆写入（ADR-0003 决策 3）：凭据经 env 注入，
	// 未配置时无操作——demo 与本地开发不依赖灵矽记忆 API。
	hub.mem = &MemoryClient{
		BaseURL:  "https://xrobo.qiniu.com",
		APIKey:   os.Getenv("LINGX_API_KEY"),
		MemoryID: os.Getenv("LINGX_MEMORY_ID"),
		MAC:      *mac,
	}
	if hub.mem.Enabled() {
		log.Printf("memory: 教练记忆写入已启用（memory_id=%s）", hub.mem.MemoryID)
	} else {
		log.Printf("memory: 未配置 LINGX_API_KEY / LINGX_MEMORY_ID，复盘记忆不写入")
	}

	store, err := LoadStore("data.json")
	if err != nil {
		log.Fatalf("load store: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/leaderboard", leaderboardHandler(store))
	mux.HandleFunc("POST /api/run", reportRunHandler(store))
	mux.HandleFunc("GET /ws", hub.HandleBrowserWS)
	mux.HandleFunc("POST /api/chat/stream", hub.HandleLinxChat)
	mux.HandleFunc("POST /intent", hub.HandleIntent)
	mux.HandleFunc("GET /game/ws", hub.HandleGameWS)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	log.Printf("loudleap server listening on %s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mux))
}
