import { describe, expect, it } from 'vitest'
import { makeLevel } from './level'

// 横杆区域含 40px 缓冲（币半径 12 + 视觉余量），与生成器内的互斥口径一致
const OVERLAP_PAD = 40

describe('makeLevel 横杆与金币互斥', () => {
  it('任意 seed 下金币都不落在横杆区域', () => {
    for (let seed = 1; seed <= 200; seed++) {
      const lv = makeLevel(seed)
      for (const [cx, cy] of lv.coins) {
        for (const [hx, hy, hw, hh] of lv.hazards) {
          const overlapped =
            cx > hx - OVERLAP_PAD && cx < hx + hw + OVERLAP_PAD && cy > hy - OVERLAP_PAD && cy < hy + hh + OVERLAP_PAD
          expect(overlapped, `seed=${seed} coin(${cx},${cy}) 落入横杆(${hx},${hy},${hw},${hh})缓冲区`).toBe(false)
        }
      }
    }
  })
})
