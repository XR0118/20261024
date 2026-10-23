import Phaser from 'phaser'
import { levelToPower, mic } from '../audio/mic'
import { reportGameState, voiceListening } from '../audio/voice'
import { makeLevel, type Level } from '../game/level'
import { getPlayer } from '../player'

const W = 960
const H = 540
// 自动跑酷：角色恒定向右，声音只负责跳（题眼玩法：喊得越响跳得越高）
const RUN_SPEED = 240

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
    this.coinText = this.add
      .text(W - pad, pad + 26, '💰 0', { color: '#ffd23f', fontSize: '18px' })
      .setOrigin(1, 0)
      .setScrollFactor(0)
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
      this.reportRun(dist)
      this.hudText.setText(`通关！摔死 ${this.deaths} 次 · 💰 ${this.coins}`)
      this.time.delayedCall(2000, () => this.scene.start('boot'))
    }
  }

  // 结算上报：金币累计与最远距离由后端持久化（关闭页面不丢）
  private reportRun(dist: number) {
    const me = getPlayer()
    void fetch('/api/run', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ id: me.id, name: me.name, dist, coins: this.coins }),
    })
  }
}
