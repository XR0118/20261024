// BossScene 的纯状态层：服务端 boss-events → UI 状态。
// 场景类只负责渲染与输入，状态演进集中在这里做表驱动测试。

export type BossEvent = {
  kind: string
  turn?: string
  guess?: string
  hits?: number
  position?: number
  digit?: number
  outcome?: string
  coins?: number
  final_coins?: number
  message?: string
}

export type GuessRow = { who: '你' | 'Boss'; guess: string; hits: number }

export type BossStage = 'awaiting-secret' | 'player-turn' | 'attack' | 'swap' | 'ended'

export type BossUiState = {
  stage: BossStage
  hint: string
  rows: GuessRow[]
  revealed: Record<number, number> // 亮位：第几位（1–3）→ 数字
  ended?: { outcome: string; coins: number; finalCoins: number }
}

export function initialBossUi(): BossUiState {
  return { stage: 'awaiting-secret', hint: '等挑战者发话…', rows: [], revealed: {} }
}

// applyBossEvents 纯 reducer：一次动作可能带来多个事件，按序归并。
export function applyBossEvents(st: BossUiState, evs: BossEvent[]): BossUiState {
  let next = shallowCopy(st)
  for (const e of evs) next = applyOne(next, e)
  return next
}

function shallowCopy(st: BossUiState): BossUiState {
  return { ...st, rows: [...st.rows], revealed: { ...st.revealed } }
}

function applyOne(st: BossUiState, e: BossEvent): BossUiState {
  switch (e.kind) {
    case 'awaiting-secret':
      return { ...st, stage: 'awaiting-secret', hint: e.message ?? '报出你的三位密数' }
    case 'battle-started':
      return { ...st, stage: 'player-turn', hint: '开战！你先猜' }
    case 'guess-result': {
      const row: GuessRow = {
        who: e.turn === 'player' ? '你' : 'Boss',
        guess: e.guess ?? '',
        hits: e.hits ?? 0,
      }
      return { ...st, rows: [...st.rows, row] }
    }
    case 'attack':
      return { ...st, stage: 'attack', hint: 'Boss 全中——攻击！格挡它！' }
    case 'reveal':
      if (!e.position || e.digit === undefined) return st
      return {
        ...st,
        stage: 'swap',
        revealed: { ...st.revealed, [e.position]: e.digit },
        hint: '格挡成功！亮位到手——报新密数换掉，或点「放弃换数」保留',
      }
    case 'swap-prompt':
      return { ...st, hint: e.message ?? '报出你的新密数' }
    case 'swap-accepted':
      return { ...st, stage: 'player-turn', hint: '已换数，Boss 记忆清零，你先猜' }
    case 'swap-declined':
      return { ...st, stage: 'player-turn', hint: '保留密数，继续' }
    case 'battle-ended':
      return {
        ...st,
        stage: 'ended',
        hint: endedHint(e.outcome),
        ended: { outcome: e.outcome ?? '', coins: e.coins ?? 0, finalCoins: e.final_coins ?? 0 },
      }
    case 'error':
      // 非法报数等提示不改阶段，只顶掉提示文案
      return { ...st, hint: e.message ?? '没听清，再来一次' }
    default:
      return st
  }
}

function endedHint(outcome?: string): string {
  switch (outcome) {
    case 'win':
      return '你赢了！金币翻倍！'
    case 'lose':
      return '格挡失败……本局按原额结算'
    case 'surrender':
      return '已投降，按原额结算'
    default:
      return '战斗结束'
  }
}
