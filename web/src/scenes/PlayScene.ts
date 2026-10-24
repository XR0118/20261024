import Phaser from 'phaser'
import { JUMP_THRESHOLD, levelToPower, mic } from '../audio/mic'
import { reportGameState, sendBossEnter, sendRunEnded, voiceListening } from '../audio/voice'
import { makeLevel, type Level } from '../game/level'
import { reportRun } from '../player'

const W = 960
const H = 540
// 自动跑酷：角色恒定向右，声音只负责跳（题眼玩法：喊得越响跳得越高）
const RUN_SPEED = 240
// 空中喷气：norm=0.5 时恰好悬停（与重力 1400 平衡），越喊升得越快
const THRUST_ACC = 2800
// 体力：空中喷气每秒 -0.55（满体力约 1.8s 强滞空），落地每秒 +0.65 恢复
const STAMINA_DRAIN = 0.55
const STAMINA_RECOVER = 0.65
// 起跳重触发抑制：防止落地瞬间被同一声的余响再次弹起
const JUMP_COOLDOWN_MS = 250

export class PlayScene extends Phaser.Scene {
  private player!: Phaser.GameObjects.Rectangle
  private playerBody!: Phaser.Physics.Arcade.Body
  private micFill!: Phaser.GameObjects.Rectangle
  private hudText!: Phaser.GameObjects.Text
  private jumpKey!: Phaser.Input.Keyboard.Key
  private deaths = 0
  private coins = 0
  private coinText!: Phaser.GameObjects.Text
  private coinGroup!: Phaser.Physics.Arcade.StaticGroup
  private stamina = 1
  private staminaBar!: Phaser.GameObjects.Rectangle
  private lastJumpAt = 0
  private finished = false
  private seed = 20261024
  private level!: Level
  private lastReport = 0

  constructor() {
    super('play')
  }

  init(data: { deaths?: number; seed?: number }) {
    this.deaths = data.deaths ?? 0
    this.seed = data.seed ?? this.seed
    this.finished = false
    this.coins = 0
    // Scene 实例跨 restart 复用，体力必须在这里复位，否则带着上局的空条开局
    this.stamina = 1
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

    this.spawnCoins()
    this.spawnHazards()

    // 终点旗
    this.add.rectangle(this.level.worldWidth - 60, 480 - 90, 8, 180, 0xef7d57)
    this.add
      .text(this.level.worldWidth - 50, 480 - 180, 'FINISH', { color: '#ef7d57', fontSize: '20px' })
      .setOrigin(0.5)

    this.cameras.main.startFollow(this.player, true, 0.1, 0.1)

    this.jumpKey = this.input.keyboard!.addKey(Phaser.Input.Keyboard.KeyCodes.SPACE)
    this.buildHud()
  }

  private spawnCoins() {
    this.coinGroup = this.physics.add.staticGroup()
    for (const [x, y] of this.level.coins) {
      const c = this.add.circle(x, y, 11, 0xffd23f)
      c.setStrokeStyle(2, 0xb8860b)
      this.coinGroup.add(c)
    }
    this.physics.add.overlap(this.player, this.coinGroup, (_, obj) => {
      obj.destroy()
      this.coins++
      this.coinText.setText(`💰 ${this.coins}`)
    })
  }

  // 空中横杆：触碰即局终（红色警示）
  private spawnHazards() {
    const group = this.physics.add.staticGroup()
    for (const [x, y, w, h] of this.level.hazards) {
      const bar = this.add.rectangle(x + w / 2, y + h / 2, w, h, 0xef476f)
      bar.setStrokeStyle(2, 0xb3001b)
      group.add(bar)
    }
    this.physics.add.overlap(this.player, group, () => this.endRun('撞上横杆！'))
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
    // 体力条（麦克风波形下方）：满黄，低量转红
    this.add
      .rectangle(W - pad - 160, pad + 30, 160, 10, 0x000000)
      .setOrigin(1, 0.5)
      .setScrollFactor(0)
      .setAlpha(0.4)
    this.staminaBar = this.add
      .rectangle(W - pad - 160, pad + 30, 160, 10, 0xffd23f)
      .setOrigin(0, 0.5)
      .setScrollFactor(0)
    this.coinText = this.add
      .text(W - pad, pad + 26, '💰 0', { color: '#ffd23f', fontSize: '18px' })
      .setOrigin(1, 0)
      .setScrollFactor(0)
  }

  update(time: number, delta: number) {
    if (this.finished) {
      this.playerBody.setVelocity(0, 0)
      return
    }
    const dt = delta / 1000

    let level = mic.level()
    // 空格 = 满档（最大跳/满推力），调试通道与真实喊声取较大者
    if (this.jumpKey.isDown) level = 1

    this.micFill.width = Math.max(1, level * 160)

    // 每帧重设横向速度：撞平台侧面被吃掉的 vx 立即恢复，卡墙即提示该跳了
    this.playerBody.setVelocityX(RUN_SPEED)

    const grounded = this.playerBody.blocked.down

    // 地面起跳（带冷却）；空中喊声 = 喷气推力（越响升得越快），消耗体力
    const power = levelToPower(level)
    if (grounded) {
      this.playerBody.setAccelerationY(0)
      this.stamina = Math.min(1, this.stamina + dt * STAMINA_RECOVER)
      if (power !== null && time - this.lastJumpAt > JUMP_COOLDOWN_MS) {
        this.lastJumpAt = time
        this.playerBody.setVelocityY(-power)
      }
    } else if (level >= JUMP_THRESHOLD && this.stamina > 0) {
      const norm = Math.min((level - JUMP_THRESHOLD) / (1 - JUMP_THRESHOLD), 1)
      this.playerBody.setAccelerationY(-THRUST_ACC * norm)
      this.stamina = Math.max(0, this.stamina - dt * STAMINA_DRAIN)
    } else {
      this.playerBody.setAccelerationY(0)
    }
    this.playerBody.setMaxVelocity(600, 520)

    // 飞行上限：角色不许飞出屏幕（顶住后清零升速，继续喊只会白耗体力）
    const CEILING = 60
    if (this.player.y < CEILING) {
      this.player.y = CEILING
      if (this.playerBody.velocity.y < 0) this.playerBody.setVelocityY(0)
    }

    this.staminaBar.width = Math.max(1, this.stamina * 160)
    this.staminaBar.fillColor = this.stamina < 0.3 ? 0xef476f : 0xffd23f

    const dist = Math.max(0, Math.floor((this.player.x - 80) / 50))
    const micTag = mic.state === 'running' ? (voiceListening() ? ' 🎙' : ' ⏳') : ' [无麦克风]'
    this.hudText.setText(`${dist} m · 摔死 ${this.deaths} 次${micTag}`)

    if (time - this.lastReport > 500) {
      this.lastReport = time
      reportGameState({ playing: true, dist, deaths: this.deaths, won: false })
    }

    // 掉坑即局终：结算入账（距离+金币），回首页重连语音待命，由语音/空格再开
    if (this.player.y > H + 160) {
      this.endRun(`摔进坑里！本局 ${dist} m · 💰 ${this.coins}`)
      return
    }
    if (this.player.x > this.level.worldWidth - 80) {
      this.enterBoss(dist)
    }
  }

  // 冲线=进入 Boss 战（「通关」概念消灭）：结算挪到战斗结束按结果一次性上报
  private enterBoss(dist: number) {
    if (this.finished) return
    this.finished = true
    reportGameState({ playing: false, phase: 'boss', dist, deaths: this.deaths, won: false })
    sendBossEnter(this.coins)
    this.scene.start('boss', { coins: this.coins, dist, deaths: this.deaths })
  }

  // 局终统一出口：结算上报（金币入账）、记忆片段通知、展示结果、回首页
  private endRun(message: string, won = false) {
    if (this.finished) return
    this.finished = true
    const dist = Math.floor((this.player.x - 80) / 50)
    reportGameState({ playing: false, dist, deaths: this.deaths, won })
    sendRunEnded(dist, this.deaths, this.coins)
    this.reportRun(dist)
    this.hudText.setText(message)
    this.time.delayedCall(2000, () => this.scene.start('boot'))
  }

  // 结算上报：金币累计与最远距离由后端持久化（关闭页面不丢）
  private reportRun(dist: number) {
    void reportRun(dist, this.coins)
  }
}
