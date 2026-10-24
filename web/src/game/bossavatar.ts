import Phaser from 'phaser'
import { NEON } from './theme'

// 破音大鹅：程序矢量 Boss 形象（夜空霓虹风，零图片素材）。
// root 挂场景定位，mouth/body 留给场景做攻击/受击特效。
export type BossAvatar = {
  root: Phaser.GameObjects.Container
  body: Phaser.GameObjects.Arc
  mouth: Phaser.GameObjects.Ellipse
}

export function buildBossAvatar(scene: Phaser.Scene, x: number, y: number): BossAvatar {
  const c = scene.add.container(x, y)
  const glow = scene.add.circle(0, 0, 64, NEON.platEdge, 0.16)
  const body = scene.add.circle(0, 0, 52, 0x241d3f)
  body.setStrokeStyle(3, NEON.platEdge)
  const tuft = scene.add.triangle(0, -50, -8, 10, 8, 10, 0, -12, NEON.starWarm)
  const eyeL = scene.add.circle(-17, -12, 9, 0xffffff)
  const pupilL = scene.add.circle(-15, -11, 4, 0x1a1c2c)
  const eyeR = scene.add.circle(17, -12, 9, 0xffffff)
  const pupilR = scene.add.circle(19, -11, 4, 0x1a1c2c)
  const browL = scene.add.rectangle(-17, -26, 20, 4, NEON.hazardFill).setAngle(18)
  const browR = scene.add.rectangle(17, -26, 20, 4, NEON.hazardFill).setAngle(-18)
  const mouth = scene.add.ellipse(0, 14, 34, 16, 0xef7d57)
  c.add([glow, body, tuft, eyeL, pupilL, eyeR, pupilR, browL, browR, mouth])
  // 待命悬浮：上下缓漂，让画面不显得静止
  scene.tweens.add({ targets: c, y: '+=10', duration: 1400, yoyo: true, repeat: -1, ease: 'Sine.InOut' })
  return { root: c, body, mouth }
}
