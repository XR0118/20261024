import { describe, expect, it } from 'vitest'
import { JUMP_THRESHOLD, levelToPower } from './mic'

describe('levelToPower', () => {
  it('低于阈值不算喊，返回 null', () => {
    expect(levelToPower(0)).toBeNull()
    expect(levelToPower(JUMP_THRESHOLD - 0.001)).toBeNull()
  })

  it('阈值处给最小初速，1.0 封顶，超出不再增长', () => {
    expect(levelToPower(JUMP_THRESHOLD)).toBe(320)
    expect(levelToPower(1)).toBe(820)
    expect(levelToPower(1.5)).toBe(820)
  })

  it('区间内线性映射', () => {
    const mid = levelToPower((JUMP_THRESHOLD + 1) / 2)
    expect(mid).toBeGreaterThan(320)
    expect(mid).toBeLessThan(820)
  })
})
