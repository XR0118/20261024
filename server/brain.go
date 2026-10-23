package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

// Brain 生成台词与游戏动作。LLM 未配置时退化为规则模板，
// 保证演示永远有台词（灵矽链路不能开天窗）。
type Brain struct {
	cfg BrainConfig
}

type BrainConfig struct {
	BaseURL string // OpenAI 兼容端点，如公司内部网关
	APIKey  string
	Model   string
}

func NewBrain(cfg BrainConfig) *Brain {
	return &Brain{cfg: cfg}
}

// Generate 返回：语音台词 + 游戏动作列表（推给网页）。
func (b *Brain) Generate(ctx context.Context, msg string, st GameState) (string, []map[string]any, error) {
	intent := classify(msg)
	if b.cfg.BaseURL == "" || b.cfg.APIKey == "" {
		return ruleReply(intent, msg, st), gameActions(intent), nil
	}
	reply, err := b.llmReply(ctx, intent, msg, st)
	if err != nil {
		return "", nil, err
	}
	return reply, gameActions(intent), nil
}

type intent int

const (
	intentOther intent = iota
	intentStart
	intentNewLevel
	intentReport
)

func classify(msg string) intent {
	m := strings.ToLower(msg)
	switch {
	case strings.Contains(m, "开始") || strings.Contains(m, "start") || strings.Contains(m, "来一局"):
		return intentStart
	case strings.Contains(m, "地图") || strings.Contains(m, "关卡") || strings.Contains(m, "换一"):
		return intentNewLevel
	case strings.Contains(m, "成绩") || strings.Contains(m, "表现") || strings.Contains(m, "总结") || strings.Contains(m, "点评"):
		return intentReport
	}
	return intentOther
}

func gameActions(it intent) []map[string]any {
	switch it {
	case intentStart:
		return []map[string]any{{"action": "start", "params": map[string]any{}}}
	case intentNewLevel:
		return []map[string]any{{"action": "load_level", "params": map[string]any{"seed": rand.Int63()}}}
	}
	return nil
}

func ruleReply(it intent, msg string, st GameState) string {
	switch it {
	case intentStart:
		return pick([]string{
			"好嘞，准备好了吗？喊得越响跳得越高，出发！",
			"三、二、一，开跑！记得大声喊出来哦！",
		})
	case intentNewLevel:
		return pick([]string{
			"新地图已加载，这关的坑更深，嗓子先热一下吧！",
			"换张地图，给你来点新鲜的！",
		})
	case intentReport:
		return fmt.Sprintf("目前跑出 %d 米，摔死了 %d 次。%s", st.Dist, st.Deaths, verdict(st))
	default:
		return pick([]string{
			"我在听呢。想开始就喊『开始』，想换图就喊『换地图』！",
			"大声点！这游戏靠嗓子吃饭的。",
		})
	}
}

func verdict(st GameState) string {
	switch {
	case st.Deaths == 0 && st.Dist > 0:
		return "一次都没摔，你是人形静音键吗？太稳了！"
	case st.Deaths >= 5:
		return "摔得有点惨，但气势不能输！"
	default:
		return "表现不错，继续！"
	}
}

func pick(ss []string) string {
	return ss[rand.Intn(len(ss))]
}

const systemPrompt = `你是横版声控跳跃游戏「闻声起跳」的主持人，性格活泼毒舌。
玩家用麦克风喊声控制角色跳跃。当前游戏状态：%s。
根据玩家的话回复一两句中文台词（会被合成语音播放），简短有梗，不要 markdown。`

func (b *Brain) llmReply(ctx context.Context, it intent, msg string, st GameState) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	body, _ := json.Marshal(map[string]any{
		"model": b.cfg.Model,
		"messages": []map[string]string{
			{"role": "system", "content": fmt.Sprintf(systemPrompt, fmt.Sprintf("进行中=%v 距离=%d米 摔死=%d次 通关=%v", st.Playing, st.Dist, st.Deaths, st.Won))},
			{"role": "user", "content": msg},
		},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, b.cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+b.cfg.APIKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm status %d", resp.StatusCode)
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("llm empty choices")
	}
	return strings.TrimSpace(out.Choices[0].Message.Content), nil
}
