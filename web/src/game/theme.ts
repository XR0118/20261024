import Phaser from 'phaser'
import { mulberry32 } from './level'

// 夜空霓虹主题：全局美术的唯一色值来源（零图片素材，改主题只改这里）
export const NEON = {
  skyTop: 0x070617,
  skyBottom: 0x1a1c2c, // 首页底色：场景切换时底边无缝衔接
  horizonGlow: 0x2a1f4d,
  groundFill: 0x141428,
  groundEdge: 0x3ef0c8, // 青色霓虹 = 地面（可踩）
  platFill: 0x1d1b33,
  platEdge: 0x7a6ff0, // 主题紫 = 浮空平台（可踩）
  topLight: 0xffffff,
  hazardFill: 0xef476f,
  hazardStripe: 0xffd6de,
  hazardEdge: 0xff2e63,
  star: 0xffffff,
  starWarm: 0xffe6a7,
} as const

// 夜空：渐变底 + 两层星星，scrollFactor 制造视差纵深（远层慢、近层快）。
// 星空吃场景 seed——同一关永远同一片天。视口固定（首页）span 留默认即可，
// 相机横滚（跑酷）传世界宽度，让星星铺满全程。
export function paintNightSky(
  scene: Phaser.Scene,
  seed: number,
  viewW: number,
  viewH: number,
  span = viewW,
) {
  const g = scene.add.graphics().setScrollFactor(0).setDepth(-10)
  g.fillGradientStyle(NEON.skyTop, NEON.skyTop, NEON.skyBottom, NEON.skyBottom, 1)
  g.fillRect(0, 0, viewW, viewH)
  // 地平线辉光：夜空底部往上晕开一层紫，避免下半屏死黑
  g.fillGradientStyle(NEON.horizonGlow, NEON.horizonGlow, NEON.skyBottom, NEON.skyBottom, 0.45)
  g.fillRect(0, viewH - 220, viewW, 220)

  const rand = mulberry32(seed)
  const layer = (n: number, sf: number, rad: number) => {
    for (let i = 0; i < n; i++) {
      const s = scene.add
        .circle(
          rand() * (viewW + (span - viewW) * sf),
          rand() * (viewH - 180),
          rad * (0.6 + rand() * 0.8),
          rand() > 0.8 ? NEON.starWarm : NEON.star,
        )
        .setScrollFactor(sf)
        .setDepth(-9)
      scene.tweens.add({
        targets: s,
        alpha: { from: 1, to: 0.3 },
        duration: 900 + rand() * 1200,
        yoyo: true,
        repeat: -1,
      })
    }
  }
  layer(26, 0.12, 1.4)
  layer(14, 0.32, 2.2)
}
