// 浏览器本地身份：id + 昵称存 localStorage；排行榜与金币累计都挂在这个 id 上
const KEY = 'loudleap_player'

export type Player = { id: string; name: string }

function randomName(): string {
  const adj = ['洪亮', '嘹亮', '震耳', '金嗓', '高音', '破音', '回声', '天籁']
  const noun = ['歌手', '青蛙', '大鹅', '狮子', '喇叭', '小智', '喊侠', '之声']
  return `${adj[Math.floor(Math.random() * adj.length)]}${noun[Math.floor(Math.random() * noun.length)]}`
}

export function getPlayer(): Player {
  let raw = localStorage.getItem(KEY)
  if (!raw) {
    raw = JSON.stringify({ id: crypto.randomUUID(), name: randomName() })
    localStorage.setItem(KEY, raw)
  }
  return JSON.parse(raw) as Player
}

export function setPlayerName(name: string): Player {
  const p = getPlayer()
  p.name = name.trim().slice(0, 12) || p.name
  localStorage.setItem(KEY, JSON.stringify(p))
  return p
}

// 局终结算入账（金币累计与最远距离由后端持久化，关闭页面不丢）
export function reportRun(dist: number, coins: number): Promise<Response> {
  const me = getPlayer()
  return fetch('/api/run', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ id: me.id, name: me.name, dist, coins }),
  })
}


// 钱包查询：失败返回 0——余额是展示性信息，不应阻塞首页
export type Wallet = { owned: string[]; active: string; balance: number; total_coins: number }
export async function fetchWallet(): Promise<Wallet> {
  const me = getPlayer()
  const res = await fetch('/api/wallet?id=' + encodeURIComponent(me.id))
  if (!res.ok) return { owned: ['bean'], active: 'bean', balance: 0, total_coins: 0 }
  return (await res.json()) as Wallet
}
