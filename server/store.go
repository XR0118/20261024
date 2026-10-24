package main

import (
	"encoding/json"
	"fmt"
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
	Skins      []string  `json:"skins,omitempty"`
	ActiveSkin string    `json:"active_skin,omitempty"`
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
		_ = json.NewEncoder(w).Encode(store.WalletOf(id))
	}
}

// Wallet 玩家的商店视图：语音购买与首页展示共用
type Wallet struct {
	Owned      []string `json:"owned"`
	Active     string   `json:"active"`
	Balance    int      `json:"balance"`
	TotalCoins int      `json:"total_coins"`
}

// WalletOf 新玩家（或旧档案）默认 bean、零余额。免费默认造型从缺省推导，
// 不写入 JSON——旧 data.json 无 skins 字段也能自然升级
func (s *Store) WalletOf(id string) Wallet {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.players[id]
	if !ok || p == nil {
		return Wallet{Owned: []string{skinBean}, Active: skinBean}
	}
	w := Wallet{Balance: p.Balance, TotalCoins: p.TotalCoins}
	w.Active = p.ActiveSkin
	if w.Active == "" {
		w.Active = skinBean
	}
	w.Owned = append(w.Owned, skinBean)
	for _, sk := range p.Skins {
		if sk != skinBean {
			w.Owned = append(w.Owned, sk)
		}
	}
	return w
}

func hasSkin(list []string, skin string) bool {
	for _, s := range list {
		if s == skin {
			return true
		}
	}
	return false
}

// BuySkin 扣费购买并穿上：只减余额（累计获得不动）。免费默认/重复购买/余额不足都拒绝
func (s *Store) BuySkin(id, skin string, price int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.players[id]
	if !ok {
		return fmt.Errorf("unknown player %s", id)
	}
	if skin != skinBean && skin != skinWave {
		return fmt.Errorf("unknown skin %s", skin)
	}
	if skin == skinBean || hasSkin(p.Skins, skin) {
		return fmt.Errorf("skin %s already owned", skin)
	}
	if p.Balance < price {
		return fmt.Errorf("balance %d < price %d", p.Balance, price)
	}
	p.Balance -= price
	p.Skins = append(p.Skins, skin)
	p.ActiveSkin = skin
	p.UpdatedAt = time.Now()
	s.saveLocked()
	return nil
}

// SwitchSkin 换上已拥有的造型（免费默认视为人人拥有）
func (s *Store) SwitchSkin(id, skin string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.players[id]
	if !ok {
		return fmt.Errorf("unknown player %s", id)
	}
	if skin != skinBean && !hasSkin(p.Skins, skin) {
		return fmt.Errorf("skin %s not owned", skin)
	}
	p.ActiveSkin = skin
	p.UpdatedAt = time.Now()
	s.saveLocked()
	return nil
}
