import Phaser from 'phaser'
import { levelToPower, mic } from '../audio/mic'

const W = 960
const H = 540

// 固定关卡布局（先丑后对）。L2 上线后由灵矽 LLM 生成的关卡 JSON 替换。
// [x, width, topY]
const GROUND: ReadonlyArray<readonly [number, number, number]> = [
  [0, 1400, 480],
  [1560, 1200, 480],
  [2900, 900, 480],
  [3940, 1500, 480],
]
const PLATFORMS: ReadonlyArray<readonly [number, number, number]> = [
  [700, 180, 360],
  [1150, 160, 280],
  [1750, 200, 340],
  [2350, 160, 260],
  [3050, 180, 360],
  [3550, 160, 280],
]
const WORLD_W = 5440
// 自动跑酷：角色恒定向右，声音只负责跳（题眼玩法：喊得越响跳得越高）
const RUN_SPEED = 240

export class PlayScene extends Phaser.Scene {
  private player!: Phaser.GameObjects.Rectangle
  private playerBody!: Phaser.Physics.Arcade.Body
  private micFill!: Phaser.GameObjects.Rectangle
  private hudText!: Phaser.GameObjects.Text
  private jumpKey!: Phaser.Input.Keyboard.Key
  private deaths = 0
  private finished = false

  constructor() {
    super('play')
  }

  init(data: { deaths?: number }) {
    this.deaths = data.deaths ?? 0
    // scene.restart 复用 Scene 实例，通关标志必须在此复位
    this.finished = false
  }

  create() {
    this.cameras.main.setBackgroundColor('#1a1c2c')
    this.physics.world.setBounds(0, -400, WORLD_W, H + 400 + 600)
    this.cameras.main.setBounds(0, 0, WORLD_W, H)

    const solids: Phaser.GameObjects.Rectangle[] = []
    for (const [x, w, y] of GROUND) solids.push(this.solid(x, w, y, 40, 0x4c5f96))
    for (const [x, w, y] of PLATFORMS) solids.push(this.solid(x, w, y, 16, 0x7a6ff0))

    this.player = this.add.rectangle(80, 400, 36, 48, 0xffcd75)
    this.physics.add.existing(this.player)
    this.playerBody = this.player.body as Phaser.Physics.Arcade.Body
    this.playerBody.setCollideWorldBounds(false)
    for (const s of solids) this.physics.add.collider(this.player, s)

    // 终点旗
    this.add.rectangle(WORLD_W - 60, 480 - 90, 8, 180, 0xef7d57)
    this.add
      .text(WORLD_W - 50, 480 - 180, 'FINISH', { color: '#ef7d57', fontSize: '20px' })
      .setOrigin(0.5)

    this.cameras.main.startFollow(this.player, true, 0.1, 0.1)

    this.jumpKey = this.input.keyboard!.addKey(Phaser.Input.Keyboard.KeyCodes.SPACE)

    this.buildHud()
  }

  private solid(x: number, w: number, topY: number, h: number, color: number) {
    const r = this.add.rectangle(x + w / 2, topY + h / 2, w, h, color)
    this.physics.add.existing(r, true)
    return r
  }

  private buildHud() {
    const pad = 16
    this.add
      .rectangle(W - pad - 160, pad + 10, 160, 12, 0x000000)
      .setOrigin(1, 0.5)
      .setScrollFactor(0)
      .setAlpha(0.4)
    this.micFill = this.add
      .rectangle(W - pad - 160, pad + 10, 1, 12, 0x38b764)
      .setOrigin(0, 0.5)
      .setScrollFactor(0)
    this.hudText = this.add
      .text(pad, pad, '', { color: '#ffffff', fontSize: '16px' })
      .setScrollFactor(0)
    if (mic.state === 'denied' || mic.state === 'requesting') {
      this.add
        .text(W / 2, 80, '麦克风不可用，按住空格键模拟喊声', {
          color: '#ff6b6b',
          fontSize: '18px',
        })
        .setOrigin(0.5)
        .setScrollFactor(0)
    }
  }

  update() {
    if (this.finished) {
      this.playerBody.setVelocity(0, 0)
      return
    }

    let level = mic.level()
    // 空格键作为调试备选，与真实喊声取较大者
    if (this.jumpKey.isDown) level = Math.max(level, 0.55)

    this.micFill.width = Math.max(1, level * 160)

    // 每帧重设横向速度：撞平台侧面被吃掉的 vx 立即恢复，卡墙即提示该跳了
    this.playerBody.setVelocityX(RUN_SPEED)

    const power = levelToPower(level)
    if (power !== null && this.playerBody.blocked.down) {
      this.playerBody.setVelocityY(-power)
    }

    const dist = Math.max(0, Math.floor((this.player.x - 80) / 50))
    this.hudText.setText(
      `${dist} m · 摔死 ${this.deaths} 次 · ${mic.state === 'running' ? '' : '[无麦克风]'}`,
    )

    if (this.player.y > H + 160) {
      this.deaths++
      this.scene.restart({ deaths: this.deaths })
    }
    if (this.player.x > WORLD_W - 80) {
      this.finished = true
      this.hudText.setText(`通关！摔死 ${this.deaths} 次`)
    }
  }
}
