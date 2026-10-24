package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"
)

// 单机 JSON 文件存储：demo 规模下比引入数据库更可靠（无外部依赖、可直接 cat 检查）。
// 关闭页面/重启服务数据不丢，符合"金币累计"的持久化要求。
type Player struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	BestDist   int       `json:"best_dist"`
	TotalCoins int       `json:"total_coins"`
	Runs       int       `json:"runs"`
	Balance    int       `json:"balance"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Store struct {
	mu      sync.Mutex
	path    string
	players map[string]*Player
}

func LoadStore(path string) (*Store, error) {
	s := &Store{path: path, players: map[string]*Player{}}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &s.players); err != nil {
		return nil, err
	}
	return s, nil
}

// ReportRun 结算一局：最远距离取 max，金币累加，局数 +1。
func (s *Store) ReportRun(id, name string, dist, coins int) Player {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.players[id]
	if !ok {
		p = &Player{ID: id}
		s.players[id] = p
	}
	p.Name = name
	if dist > p.BestDist {
		p.BestDist = dist
	}
	p.TotalCoins += coins
	p.Balance += coins
	p.Runs++
	p.UpdatedAt = time.Now()
	s.saveLocked()
	return *p
}

// Leaderboard 按最远距离排序；距离并列时金币多者在前。
func (s *Store) Leaderboard(limit int) []Player {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Player, 0, len(s.players))
	for _, p := range s.players {
		out = append(out, *p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].BestDist != out[j].BestDist {
			return out[i].BestDist > out[j].BestDist
		}
		return out[i].TotalCoins > out[j].TotalCoins
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (s *Store) saveLocked() {
	data, err := json.MarshalIndent(s.players, "", "  ")
	if err != nil {
		log.Printf("store marshal: %v", err)
		return
	}
	if err := os.WriteFile(s.path, data, 0o644); err != nil {
		log.Printf("store write: %v", err)
	}
}

func leaderboardHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"players": store.Leaderboard(20)})
	}
}

func reportRunHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Dist  int    `json:"dist"`
			Coins int    `json:"coins"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.ID == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Name == "" {
			req.Name = "无名侠"
		}
		p := store.ReportRun(req.ID, req.Name, req.Dist, req.Coins)
		log.Printf("run: %s dist=%d coins=%d (best=%d total=%d)", req.Name, req.Dist, req.Coins, p.BestDist, p.TotalCoins)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(p)
	}
}

// BalanceOf 返回玩家可支配余额；未知玩家按新号对待，从零攒起。
func (s *Store) BalanceOf(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p, ok := s.players[id]; ok {
		return p.Balance
	}
	return 0
}

func walletHandler(store *Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"balance": store.BalanceOf(id)})
	}
}
