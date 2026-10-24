import Phaser from 'phaser'

// 造型 = GLOSSARY「角色」：纯视觉皮肤。碰撞盒固定为旧占位矩形的 36×48，跳跃手感不随造型变
export type SkinId = 'bean' | 'wave'
export const SKIN_IDS: SkinId[] = ['bean', 'wave']
export const SKIN_PRICE = 20
export const SKIN_BODY_W = 36
export const SKIN_BODY_H = 48

// 本地指定造型（T9 验收通道：?skin=wave；T10 起以服务端当前造型为准）
export function localSkin(): SkinId {
  return new URLSearchParams(location.search).get('skin') === 'wave' ? 'wave' : 'bean'
}

export function buildSkin(scene: Phaser.Scene, id: SkinId): Phaser.GameObjects.Container {
  const parts = id === 'bean' ? beanParts(scene) : waveParts(scene)
  const skin = scene.add.container(0, 0, parts)
  if (id === 'wave') {
    const arcs = parts[parts.length - 1] as Phaser.GameObjects.Graphics
    // 音波环脉动：让声波常在的观感落地
    scene.tweens.add({ targets: arcs, alpha: { from: 0.45, to: 1 }, duration: 700, yoyo: true, repeat: -1, ease: 'Sine.InOut' })
  }
  return skin
}

// 跳跳豆：圆角胶囊身+大眼+腮红——免费的默认造型
function beanParts(scene: Phaser.Scene): Phaser.GameObjects.GameObject[] {
  const body = scene.add.graphics()
  body.fillStyle(0xffcd75, 1)
  body.fillRoundedRect(-18, -24, 36, 48, 16)
  body.lineStyle(3, 0xe8a33d, 1)
  body.strokeRoundedRect(-18, -24, 36, 48, 16)
  return [
    body,
    scene.add.circle(-6, -8, 5, 0xffffff),
    scene.add.circle(7, -8, 5, 0xffffff),
    scene.add.circle(-4.5, -8, 2.5, 0x1a1c2c), // 瞳孔偏右：看向前进方向
    scene.add.circle(8.5, -8, 2.5, 0x1a1c2c),
    scene.add.circle(-11, 3, 3.5, 0xff8fa3),
    scene.add.circle(12, 3, 3.5, 0xff8fa3),
  ]
}

// 声波精灵：渐变圆身+朝前三道音波弧——付费造型，定价 SKIN_PRICE
function waveParts(scene: Phaser.Scene): Phaser.GameObjects.GameObject[] {
  const body = scene.add.graphics()
  body.fillStyle(0x5b4bc4, 0.45)
  body.fillCircle(0, 0, 26)
  body.fillStyle(0x7a6ff0, 0.6)
  body.fillCircle(0, 0, 22)
  body.fillStyle(0x9d8cff, 1)
  body.fillCircle(0, 0, 18)
  const arcs = scene.add.graphics()
  arcs.lineStyle(3, 0x00e5ff, 0.9)
  for (const [r, from, to] of [
    [22, -0.5, 0.5],
    [28, -0.38, 0.38],
    [34, -0.26, 0.26],
  ]) {
    arcs.beginPath()
    arcs.arc(0, 0, r, from, to)
    arcs.strokePath()
  }
  return [
    body,
    scene.add.circle(-6, -6, 5, 0xffffff),
    scene.add.circle(7, -6, 5, 0xffffff),
    scene.add.circle(-4.5, -6, 2.5, 0x1a1c2c),
    scene.add.circle(8.5, -6, 2.5, 0x1a1c2c),
    arcs,
  ]
}

// 跑动动画：贴地压扁、空中随垂直速度拉伸（squash & stretch）。只动视觉 scale，物理体不受影响
export function applyRunAnimation(skin: Phaser.GameObjects.Container, vy: number, grounded: boolean, dt: number) {
  const targetY = grounded ? 0.88 : Math.min(1.18, 1 + Math.min(Math.abs(vy) / 900, 0.18))
  const k = Math.min(1, dt * 12)
  skin.scaleY += (targetY - skin.scaleY) * k
  skin.scaleX += (1 / skin.scaleY - skin.scaleX) * k
}

// 待命呼吸：首页预览用；tween 挂在场景上，切场景即清理
export function startIdleBreathing(scene: Phaser.Scene, skin: Phaser.GameObjects.Container) {
  scene.tweens.add({
    targets: skin,
    scaleY: { from: 1, to: 1.05 },
    scaleX: { from: 1, to: 0.97 },
    duration: 1200,
    yoyo: true,
    repeat: -1,
    ease: 'Sine.InOut',
  })
}
