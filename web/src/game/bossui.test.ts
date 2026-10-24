import { describe, expect, it } from 'vitest'
import { applyBossEvents, initialBossUi } from './bossui'

describe('applyBossEvents reducer', () => {
  it('进场索要密数', () => {
    const st = applyBossEvents(initialBossUi(), [{ kind: 'awaiting-secret', message: '请报出你的三位密数' }])
    expect(st.stage).toBe('awaiting-secret')
    expect(st.hint).toContain('密数')
  })

  it('开战后玩家先猜，报数历史按时间序记录双方回合', () => {
    let st = applyBossEvents(initialBossUi(), [
      { kind: 'awaiting-secret' },
      { kind: 'battle-started' },
      { kind: 'secret-confirmed', guess: '123' },
    ])
    expect(st.stage).toBe('player-turn')
    expect(st.mySecret).toBe('123') // 语音/键盘上报后回显核对
    st = applyBossEvents(st, [
      { kind: 'guess-result', turn: 'player', guess: '456', hits: 1 },
      { kind: 'guess-result', turn: 'boss', guess: '012', hits: 0 },
    ])
    expect(st.rows).toEqual([
      { who: '你', guess: '456', hits: 1 },
      { who: 'Boss', guess: '012', hits: 0 },
    ])
    expect(st.stage).toBe('player-turn') // Boss 未全中，轮到玩家
  })

  it('攻击事件进入攻击阶段，亮位进情报表', () => {
    let st = applyBossEvents(initialBossUi(), [
      { kind: 'battle-started' },
      { kind: 'guess-result', turn: 'boss', guess: '483', hits: 3 },
      { kind: 'attack' },
    ])
    expect(st.stage).toBe('attack')
    st = applyBossEvents(st, [{ kind: 'reveal', position: 2, digit: 8 }])
    expect(st.revealed).toEqual({ 2: 8 })
    expect(st.stage).toBe('swap')
    expect(st.hint).toContain('换')
  })

  it('换数接受/放弃都回到玩家回合', () => {
    const attack = applyBossEvents(initialBossUi(), [{ kind: 'battle-started' }, { kind: 'attack' }])
    expect(applyBossEvents(attack, [{ kind: 'swap-accepted' }]).stage).toBe('player-turn')
    expect(applyBossEvents(attack, [{ kind: 'swap-declined' }]).stage).toBe('player-turn')
  })

  it('终局记录结局与最终金币（胜=翻倍）', () => {
    const st = applyBossEvents(initialBossUi(), [
      { kind: 'battle-ended', outcome: 'win', coins: 30, final_coins: 60 },
    ])
    expect(st.stage).toBe('ended')
    expect(st.ended).toEqual({ outcome: 'win', coins: 30, finalCoins: 60 })
  })

  it('非法报数的错误提示直接展示（可 TTS 的原文）', () => {
    const st = applyBossEvents(initialBossUi(), [
      { kind: 'error', message: '三个数字不能重复，重新报' },
    ])
    expect(st.hint).toContain('不能重复')
    expect(st.stage).toBe('awaiting-secret') // 错误不改变阶段
  })

  it('不修改原状态（reducer 纯度）', () => {
    const st0 = initialBossUi()
    applyBossEvents(st0, [{ kind: 'battle-started' }])
    expect(st0.stage).toBe('awaiting-secret')
    expect(st0.rows).toEqual([])
  })
})
