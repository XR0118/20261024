import { describe, expect, it } from 'vitest'
import { JUMP_THRESHOLD, levelToPower, scaleLevel } from './mic'

describe('scaleLevel', () => {
  it('灵敏度系数缩放响度，封顶 1', () => {
    expect(scaleLevel(0.1, 1)).toBeCloseTo(0.4)
    expect(scaleLevel(0.1, 1.6)).toBeCloseTo(0.64)
    expect(scaleLevel(0.3, 2)).toBe(1)
  })
})

describe('levelToPower', () => {
  it('低于阈值不算喊，返回 null', () => {
    expect(levelToPower(0)).toBeNull()
    expect(levelToPower(JUMP_THRESHOLD - 0.001)).toBeNull()
  })

  it('阈值处给最小初速，1.0 封顶，超出不再增长', () => {
    expect(levelToPower(JUMP_THRESHOLD)).toBe(380)
    expect(levelToPower(1)).toBe(960)
    expect(levelToPower(1.5)).toBe(960)
  })

  it('区间内线性映射', () => {
    const mid = levelToPower((JUMP_THRESHOLD + 1) / 2)
    expect(mid).toBeGreaterThan(380)
    expect(mid).toBeLessThan(960)
  })
})
