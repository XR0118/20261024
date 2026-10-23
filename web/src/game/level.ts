// mulberry32：种子化 PRNG，同一 seed 生成同一关卡（换图 = 换 seed）
export function mulberry32(seed: number) {
  return () => {
    seed |= 0
    seed = (seed + 0x6d2b79f5) | 0
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

export type Level = {
  ground: [number, number, number][]
  platforms: [number, number, number][]
  coins: [number, number][]
  // 后半程空中横杆：红杆，触碰即局终。挂在跳跃弧线上部，低跳可从下方钻过
  hazards: [number, number, number, number][] // [x, topY, w, h]
  worldWidth: number
}

// 关卡数值围绕跳跃可达性设计：最大跳高 ≈ v²/2g ≈ 240px，滞空水平 ≈ 280px
export function makeLevel(seed: number): Level {
  const rand = mulberry32(seed)
  const ground: [number, number, number][] = [[0, 600, 480]]
  const platforms: [number, number, number][] = []
  const coins: [number, number][] = []
  const hazards: [number, number, number, number][] = []
  // 币撒在地面段上方的跳跃弧线上，鼓励"为币而喊"
  const coinArc = (cx: number, cy: number, n: number) => {
    for (let i = 0; i < n; i++) {
      coins.push([cx + (i - (n - 1) / 2) * 36, cy - Math.abs(i - (n - 1) / 2) * 14])
    }
  }
  let x = 600
  const halfWidth = 2300 // 障碍物从后半程开始出现
  while (x < 4600) {
    const gap = 120 + rand() * 160
    const seg = 500 + rand() * 700
    ground.push([x + gap, seg, 480])
    if (rand() > 0.4) {
      const py = 300 + rand() * 100
      platforms.push([x + gap * 0.5, 140 + rand() * 80, py])
      coinArc(x + gap * 0.5 + 90, py - 40, 3)
    }
    if (rand() > 0.5) {
      const py = 260 + rand() * 120
      platforms.push([x + gap + seg * 0.4, 150 + rand() * 80, py])
      coinArc(x + gap + seg * 0.4 + 75, py - 40, 3)
    }
    if (rand() > 0.6) coinArc(x + gap + seg * 0.7, 420, 4)
    if (x > halfWidth && rand() > 0.5) {
      // 杆挂 y≈300-340：贴地小跳从下方钻过，高跳/滞空喷气要留神
      hazards.push([x + gap + seg * (0.3 + rand() * 0.4), 300 + rand() * 40, 70 + rand() * 60, 16])
    }
    x += gap + seg
  }
  ground.push([x + 200, 800, 480])
  coinArc(x + 500, 420, 5)
  return { ground, platforms, coins, hazards, worldWidth: x + 1000 }
}
