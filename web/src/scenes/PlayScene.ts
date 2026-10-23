import Phaser from 'phaser'
import { levelToPower, mic } from '../audio/mic'
import { reportGameState, voiceListening } from '../audio/voice'

const W = 960
const H = 540
// 自动跑酷：角色恒定向右，声音只负责跳（题眼玩法：喊得越响跳得越高）
const RUN_SPEED = 240

type Level = {
  ground: [number, number, number][]
  platforms: [number, number, number][]
  worldWidth: number
}

// mulberry32：种子化 PRNG，同一 seed 生成同一关卡（L2 由服务端 seed 驱动）
function mulberry32(seed: number) {
  return () => {
    seed |= 0
    seed = (seed + 0x6d2b79f5) | 0
    let t = Math.imul(seed ^ (seed >>> 15), 1 | seed)
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296
  }
}

// 关卡数值围绕跳跃可达性设计：最大跳高 ≈ v²/2g ≈ 240px，滞空水平 ≈ 280px
function makeLevel(seed: number): Level {
  const rand = mulberry32(seed)
  const ground: [number, number, number][] = [[0, 600, 480]]
  const platforms: [number, number, number][] = []
  let x = 600
  while (x < 4600) {
    const gap = 120 + rand() * 200
    const seg = 500 + rand() * 700
    ground.push([x + gap, seg, 480])
    if (rand() > 0.4) platforms.push([x + gap * 0.5, 140 + rand() * 80, 300 + rand() * 100])
    if (rand() > 0.5) platforms.push([x + gap + seg * 0.4, 150 + rand() * 80, 260 + rand() * 120])
    x += gap + seg
  }
  ground.push([x + 200, 800, 480])
  return { ground, platforms, worldWidth: x + 1000 }
}

export class PlayScene extends Phaser.Scene {
  private player!: Phaser.GameObjects.Rectangle
  private playerBody!: Phaser.Physics.Arcade.Body
  private micFill!: Phaser.GameObjects.Rectangle
  private hudText!: Phaser.GameObjects.Text
  private jumpKey!: Phaser.Input.Keyboard.Key
  private deaths = 0
  private finished = false
  private seed = 20261024
  private level!: Level
  private lastReport = 0
  private onGameAction = (ev: Event) => {
    const a = (ev as CustomEvent).detail as { action: string; params: { seed?: number } }
    if (a.action === 'start') this.scene.restart({ deaths: this.deaths })
    if (a.action === 'load_level') {
      this.scene.restart({ deaths: this.deaths, seed: Number(a.params?.seed ?? Math.random() * 1e9) })
    }
  }

  constructor() {
    super('play')
  }

  init(data: { deaths?: number; seed?: number }) {
    this.deaths = data.deaths ?? 0
    this.seed = data.seed ?? this.seed
    // scene.restart 复用 Scene 实例，通关标志必须在此复位
    this.finished = false
  }

  create() {
    this.level = makeLevel(this.seed)

    this.cameras.main.setBackgroundColor('#1a1c2c')
    this.physics.world.setBounds(0, -400, this.level.worldWidth, H + 1000)
    this.cameras.main.setBounds(0, 0, this.level.worldWidth, H)

    const solids: Phaser.GameObjects.Rectangle[] = []
    for (const [x, w, y] of this.level.ground) solids.push(this.solid(x, w, y, 40, 0x4c5f96))
    for (const [x, w, y] of this.level.platforms) solids.push(this.solid(x, w, y, 16, 0x7a6ff0))

    this.player = this.add.rectangle(80, 400, 36, 48, 0xffcd75)
    this.physics.add.existing(this.player)
    this.playerBody = this.player.body as Phaser.Physics.Arcade.Body
    this.playerBody.setCollideWorldBounds(false)
    for (const s of solids) this.physics.add.collider(this.player, s)

    // 终点旗
    this.add.rectangle(this.level.worldWidth - 60, 480 - 90, 8, 180, 0xef7d57)
    this.add
      .text(this.level.worldWidth - 50, 480 - 180, 'FINISH', { color: '#ef7d57', fontSize: '20px' })
      .setOrigin(0.5)

    this.cameras.main.startFollow(this.player, true, 0.1, 0.1)

    this.jumpKey = this.input.keyboard!.addKey(Phaser.Input.Keyboard.KeyCodes.SPACE)

    // 语音意图 → 游戏动作（voice.ts 派发的 DOM 事件）；shutdown 必须摘除防泄漏
    window.addEventListener('game-action', this.onGameAction)
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => {
      window.removeEventListener('game-action', this.onGameAction)
    })

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
    if (mic.state !== 'running') {
      this.add
        .text(W / 2, 80, '麦克风不可用，按住空格键模拟喊声', {
          color: '#ff6b6b',
          fontSize: '18px',
        })
        .setOrigin(0.5)
        .setScrollFactor(0)
    }
  }

  update(time: number) {
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
    const micTag = mic.state === 'running' ? (voiceListening() ? ' 🎙' : ' ⏳') : ' [无麦克风]'
    this.hudText.setText(`${dist} m · 摔死 ${this.deaths} 次${micTag}`)

    if (time - this.lastReport > 500) {
      this.lastReport = time
      reportGameState({ playing: true, dist, deaths: this.deaths, won: false })
    }

    if (this.player.y > H + 160) {
      this.deaths++
      this.scene.restart({ deaths: this.deaths, seed: this.seed })
    }
    if (this.player.x > this.level.worldWidth - 80) {
      this.finished = true
      reportGameState({ playing: false, dist, deaths: this.deaths, won: true })
      this.hudText.setText(`通关！摔死 ${this.deaths} 次`)
    }
  }
}
