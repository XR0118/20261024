import { describe, expect, it } from 'vitest'
import { BLOCK_WINDOW_MS, SHOUT_THRESHOLD, blockedByShout } from './qte'

describe('blockedByShout', () => {
  it('窗口内任一采样达到喊声阈值即格挡成功', () => {
    expect(blockedByShout([0.1, 0.2, SHOUT_THRESHOLD])).toBe(true)
    expect(blockedByShout([0.1, 0.9, 0.1])).toBe(true)
  })

  it('全程低于阈值=格挡失败（说话音量不够，必须喊）', () => {
    expect(blockedByShout([0.1, 0.2, 0.3])).toBe(false)
    expect(blockedByShout([])).toBe(false) // 无麦克风/无采样
  })

  it('阈值略高于跳跃阈值：格挡要求更卖力', () => {
    expect(SHOUT_THRESHOLD).toBeGreaterThan(0.3)
    expect(BLOCK_WINDOW_MS).toBeGreaterThan(1500)
    expect(BLOCK_WINDOW_MS).toBeLessThan(3500)
  })
})
