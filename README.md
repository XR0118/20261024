# 闻声起跳 · LoudLeap

2026 七牛第五届 1024 Hackathon · 研发赛道 · 议题 6（灵矽应用：语音类游戏）

一款声控横版跳跃游戏：**喊得越响，跳得越高**。灵矽 AI 是游戏的大脑——
开场主持、按主题动态生成关卡、以及一个会根据你的表现实时嘴炮的 AI Boss。

## 灵矽分层架构（demo 可逐级降级）

| 层 | 能力 | 灵矽链路 |
|----|------|---------|
| L0 | 本地声控跳跃（麦克风音量 → 跳跃力度） | 无，纯本地 |
| L1 | 语音主持 + 结算点评 | 灵矽 ASR/TTS + 第三方应用接入 |
| L2 | 按音乐/主题动态生成地图（LLM 产关卡 JSON） | 第三方应用接入 |
| L3 | AI Boss：实时嘴炮 + 随玩家表现换战术 | 第三方应用接入 |

架构决策详见 [docs/adr/0001-dual-channel-loudleap-architecture.md](docs/adr/0001-dual-channel-loudleap-architecture.md)。

## 快速开始（L0）

```bash
cd web
npm install
npm run dev
# 浏览器打开提示地址，授权麦克风；对着麦克风喊，空格键为调试备选
```

## 开发计划

三天节奏与任务拆解见 [docs/plan.md](docs/plan.md)。

## 目录结构

```
web/      # L0 游戏本体（Vite + TypeScript + Phaser 3）
server/   # golang 后端：灵矽第三方应用接入端点 + 游戏事件 WebSocket 推送（D2）
docs/     # ADR、计划、agent 约定
```
