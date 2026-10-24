import Phaser from 'phaser'
import { JUMP_THRESHOLD, levelToPower, mic } from '../audio/mic'
import { reportGameState, sendBossEnter, sendRunEnded, voiceListening } from '../audio/voice'
import { makeLevel, type Level } from '../game/level'
import { getPlayer, reportRun } from '../player'
import { applyRunAnimation, buildSkin, urlSkin, SKIN_BODY_H, SKIN_BODY_W } from '../game/skin'
import { NEON, FONT, ensureCoinTexture, ensureSparkTexture, neonPanel, paintNightSky } from '../game/theme'

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
  private player!: Phaser.GameObjects.Container
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
  private skin = ''
  private level!: Level
  private lastReport = 0

  constructor() {
    super('play')
  }

  init(data: { deaths?: number; seed?: number; skin?: string }) {
    this.deaths = data.deaths ?? 0
    this.seed = data.seed ?? this.seed
    this.skin = data.skin ?? ''
    this.finished = false
    this.coins = 0
    // Scene 实例跨 restart 复用，体力必须在这里复位，否则带着上局的空条开局
    this.stamina = 1
  }

  create() {
    this.level = makeLevel(this.seed)

    paintNightSky(this, this.seed, W, H, this.level.worldWidth)
    this.physics.world.setBounds(0, -400, this.level.worldWidth, H + 1000)
    this.cameras.main.setBounds(0, 0, this.level.worldWidth, H)

    const solids: Phaser.GameObjects.Rectangle[] = []
    for (const [x, w, y] of this.level.ground)
      solids.push(this.solid(x, w, y, 40, NEON.groundFill, NEON.groundEdge))
    for (const [x, w, y] of this.level.platforms)
      solids.push(this.solid(x, w, y, 16, NEON.platFill, NEON.platEdge))

    // 造型优先级：URL 调试参数 > BootScene 传入的服务端当前造型
    const skin = urlSkin() ?? (this.skin === 'wave' ? 'wave' : 'bean')
    this.player = buildSkin(this, skin)
    this.player.setPosition(80, 400)
    this.physics.add.existing(this.player)
    this.playerBody = this.player.body as Phaser.Physics.Arcade.Body
    // 碰撞盒钉死为旧占位矩形的 36×48：造型纯视觉，跳跃手感不随皮肤变
    this.playerBody.setSize(SKIN_BODY_W, SKIN_BODY_H)
    this.playerBody.setCollideWorldBounds(false)
    for (const s of solids) this.physics.add.collider(this.player, s)

    this.spawnCoins()
    this.spawnHazards()

    // 终点门：青色霓虹光柱，与可踩地面同一信号色（到这儿就是终点）
    const fx = this.level.worldWidth - 60
    const finishGlow = this.add.graphics()
    finishGlow.fillStyle(NEON.groundEdge, 0.25)
    finishGlow.fillRect(fx - 14, 290, 28, 190)
    finishGlow.fillStyle(NEON.topLight, 0.9)
    finishGlow.fillRect(fx - 2, 290, 4, 190)
    this.add
      .text(fx, 270, 'FINISH', { ...FONT, color: '#3ef0c8', fontSize: '20px', fontStyle: 'bold' })
      .setOrigin(0.5)

    this.cameras.main.startFollow(this.player, true, 0.1, 0.1)

    this.jumpKey = this.input.keyboard!.addKey(Phaser.Input.Keyboard.KeyCodes.SPACE)
    ensureSparkTexture(this)
    ensureCoinTexture(this)
    this.buildHud()
  }

  private spawnCoins() {
    this.coinGroup = this.physics.add.staticGroup()
    for (const [x, y] of this.level.coins) {
      // 币 = 程序生成纹理的 Image：物理体天然居中，拾取销毁单对象即可
      const coin = this.add.image(x, y, 'coin')
      // 伪 3D 翻转：横向压缩循环（高光烘焙在纹理里随转）
      this.tweens.add({
        targets: coin,
        scaleX: 0.15,
        duration: 480,
        yoyo: true,
        repeat: -1,
        ease: 'Sine.InOut',
      })
      this.coinGroup.add(coin)
    }
    this.physics.add.overlap(this.player, this.coinGroup, (_, obj) => {
      const coin = obj as Phaser.GameObjects.Image
      this.coinBurst(coin.x, coin.y)
      coin.destroy()
      this.coins++
      this.coinText.setText(`💰 ${this.coins}`)
    })
  }

  // 拾取反馈：金色火花向上迸发后即焚（粒子发射器自毁，不留场景负担）
  private coinBurst(x: number, y: number) {
    const p = this.add.particles(x, y, 'spark', {
      speed: { min: 60, max: 170 },
      angle: { min: 200, max: 340 },
      gravityY: 500,
      scale: { start: 1.1, end: 0 },
      alpha: { start: 1, end: 0.2 },
      lifespan: 450,
      tint: 0xffd23f,
      emitting: false,
    })
    p.explode(10)
    this.time.delayedCall(600, () => p.destroy())
  }

  // 空中横杆：触碰即局终（红色警示）
  private spawnHazards() {
    const group = this.physics.add.staticGroup()
    for (const [x, y, w, h] of this.level.hazards) {
      // 物理体透明：视觉交给斜纹警示带，碰撞形状不变
      const bar = this.add.rectangle(x + w / 2, y + h / 2, w, h, NEON.hazardFill, 0)
      group.add(bar)
      this.hazardStripes(x, y, w, h)
    }
    this.physics.add.overlap(this.player, group, () => this.endRun('撞上横杆！', 'bar'))
  }

  // 红底斜纹警示带（工程围挡同款）：远看醒目可提前预判，近看纹样清晰
  private hazardStripes(x: number, y: number, w: number, h: number) {
    const g = this.add.graphics()
    g.fillStyle(NEON.hazardFill, 1)
    g.fillRect(x, y, w, h)
    g.fillStyle(NEON.hazardStripe, 0.9)
    const step = 14
    for (let sx = x - h; sx < x + w; sx += step) {
      g.fillPoints(
        [
          { x: sx, y: y + h },
          { x: sx + h, y },
          { x: sx + h + 7, y },
          { x: sx + 7, y: y + h },
        ],
        true,
      )
    }
    g.lineStyle(2, NEON.hazardEdge, 1)
    g.strokeRect(x, y, w, h)
  }

  private solid(x: number, w: number, topY: number, h: number, fill: number, edge: number) {
    const r = this.add.rectangle(x + w / 2, topY + h / 2, w, h, fill)
    this.physics.add.existing(r, true)
    // 霓虹描边：宽而淡的外圈模拟辉光，亮内圈勾边——纯矢量发光效果
    const g = this.add.graphics()
    g.lineStyle(6, edge, 0.25)
    g.strokeRect(x, topY, w, h)
    g.lineStyle(2, edge, 1)
    g.strokeRect(x, topY, w, h)
    // 顶面亮线：可踩面的视觉信号，跳跃落点可辨
    g.fillStyle(NEON.topLight, 0.85)
    g.fillRect(x, topY, w, 3)
    return r
  }

  private buildHud() {
    const pad = 16
    // 左上：距离信息卡
    neonPanel(this, pad - 8, pad - 8, 132, 42).setScrollFactor(0)
    this.hudText = this.add
      .text(pad, pad, '', { ...FONT, color: '#ffffff', fontSize: '17px' })
      .setScrollFactor(0)
    // 右上：声音/体力/金币一体的状态卡
    const px = W - pad - 176
    neonPanel(this, px - 8, pad - 8, 184, 70).setScrollFactor(0)
    this.add
      .rectangle(W - pad - 160, pad + 10, 160, 12, 0x000000)
      .setOrigin(1, 0.5)
      .setScrollFactor(0)
      .setAlpha(0.4)
    this.micFill = this.add
      .rectangle(W - pad - 160, pad + 10, 1, 12, 0x38b764)
      .setOrigin(0, 0.5)
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
      .text(W - pad, pad + 40, '💰 0', { ...FONT, color: '#ffd23f', fontSize: '18px' })
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
    applyRunAnimation(this.player, this.playerBody.velocity.y, grounded, dt)

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
    this.hudText.setText(`${dist} m${micTag}`)

    if (time - this.lastReport > 500) {
      this.lastReport = time
      reportGameState({ playing: true, dist, deaths: this.deaths, won: false })
    }

    // 掉坑即局终：结算入账（距离+金币），回首页重连语音待命，由语音/空格再开
    if (this.player.y > H + 160) {
      this.endRun(`摔进坑里！本局 ${dist} m · 💰 ${this.coins}`, 'pit')
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
    sendBossEnter(this.coins, getPlayer().name)
    this.scene.start('boss', { coins: this.coins, dist, deaths: this.deaths })
  }

  // 局终统一出口（掉坑/撞杆，未到 Boss 战）：结算上报、按准确死因写记忆、回首页
  private endRun(message: string, cause: 'pit' | 'bar') {
    if (this.finished) return
    this.finished = true
    this.deaths++
    const dist = Math.floor((this.player.x - 80) / 50)
    reportGameState({ playing: false, dist, deaths: this.deaths, won: false })
    sendRunEnded(dist, this.coins, getPlayer().name, cause)
    this.reportRun(dist)
    // 结算横幅：居中圆角面板弹入，代替塞在角落 HUD 里的一行字
    const cam = this.cameras.main
    const bx = cam.scrollX + W / 2
    const by = cam.scrollY + H / 2
    const panel = neonPanel(this, bx - 210, by - 46, 420, 92, 0xef476f).setDepth(50)
    const title = this.add
      .text(bx, by - 24, '本局结束', { ...FONT, color: '#ef476f', fontSize: '20px' })
      .setOrigin(0.5)
      .setDepth(51)
    const detail = this.add
      .text(bx, by + 14, message, { ...FONT, color: '#ffe6a7', fontSize: '24px' })
      .setOrigin(0.5)
      .setDepth(51)
    panel.setScale(0.7)
    title.setScale(0.7)
    detail.setScale(0.7)
    this.tweens.add({
      targets: [panel, title, detail],
      scale: 1,
      duration: 220,
      ease: 'Back.Out',
    })
    this.time.delayedCall(2000, () => this.scene.start('boot'))
  }

  // 结算上报：金币累计与最远距离由后端持久化（关闭页面不丢）
  private reportRun(dist: number) {
    void reportRun(dist, this.coins)
  }
}
