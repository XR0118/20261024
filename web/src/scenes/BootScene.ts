import Phaser from 'phaser'
import { mic } from '../audio/mic'
import {
  reportGameState,
  resetTTSTracking,
  sendBossEnter,
  ttsArrived,
  ttsArrivedAndDone,
} from '../audio/voice'
import { fetchWallet, getPlayer, setPlayerName } from '../player'
import { buildSkin, SKIN_PRICE, SkinId, startIdleBreathing } from '../game/skin'

const W = 960
const H = 540

// 字段名与后端 JSON tag（小写下划线）严格一致，否则渲染 undefined
type Row = { id: string; name: string; best_dist: number; total_coins: number; runs: number }

// 初始页：排行榜 + 开始入口。语音指令（game-action）在本页即可触发开局。
export class BootScene extends Phaser.Scene {
  private rows: Row[] = []
  private boardText!: Phaser.GameObjects.Text
  private hintText!: Phaser.GameObjects.Text
  private meText!: Phaser.GameObjects.Text
  private balanceText!: Phaser.GameObjects.Text
  private activeSkin = 'bean'
  private skinCards: { id: string; bg: Phaser.GameObjects.Rectangle; status: Phaser.GameObjects.Text }[] = []
  private lastReport = 0
  private pendingSeed: number | undefined | null = null // null=未挂起；undefined=start；数字=load_level seed
  private prepareAt = 0
  private sensPanel!: Phaser.GameObjects.Container
  private sensFill!: Phaser.GameObjects.Rectangle
  private sensLabel!: Phaser.GameObjects.Text
  private sensBtns: { v: number; t: Phaser.GameObjects.Text }[] = []
  private onGameAction = (ev: Event) => {
    const a = (ev as CustomEvent).detail as { action: string; params: { seed?: number; skin?: string } }
    // 语音换装：更新待命造型并刷新余额（T11 的角色卡会订阅同一状态）
    if (a.action === 'change-skin') {
      this.activeSkin = a.params?.skin === 'wave' ? 'wave' : 'bean'
      this.buildSkinCards()
    void this.refreshWallet()
      return
    }
    if (a.action === 'prepare') {
      this.hintText.setText('🎙 主持人正在发令，马上开跑…')
      this.tweens.add({ targets: this.hintText, alpha: 0.4, duration: 400, yoyo: true, repeat: -1 })
      return
    }
    if (a.action === 'start') {
      this.pendingSeed = undefined
      this.prepareAt = this.time.now
    }
    if (a.action === 'load_level') {
      this.pendingSeed = Number(a.params?.seed ?? Math.random() * 1e9)
      this.prepareAt = this.time.now
    }
  }

  constructor() {
    super('boot')
  }

  create() {
    this.cameras.main.setBackgroundColor('#1a1c2c')

    this.add
      .text(W / 2, 70, '闻声起跳 · LoudLeap', { color: '#ffe6a7', fontSize: '44px', fontStyle: 'bold' })
      .setOrigin(0.5)
    this.add
      .text(W / 2, 118, '喊得越响，跳得越高', { color: '#7a6ff0', fontSize: '18px' })
      .setOrigin(0.5)

    this.add
      .text(W / 2, 168, '🏅 排行榜（最远距离）', { color: '#ffffff', fontSize: '20px' })
      .setOrigin(0.5)
    this.boardText = this.add.text(0, 0, '加载中…', {
      color: '#cfd2e8',
      fontSize: '18px',
      lineSpacing: 8,
    })

    this.hintText = this.add
      .text(W / 2, 448, '喊「你好小智」唤醒，再说「开始游戏」', {
        color: '#ffe6a7',
        fontSize: '20px',
      })
      .setOrigin(0.5)
    this.add
      .text(W / 2, 478, '调试：按空格直接开始', { color: '#566079', fontSize: '15px' })
      .setOrigin(0.5)
    this.add
      .text(W / 2, 502, '⚔ 调试：直接挑战 Boss', { color: '#566079', fontSize: '15px' })
      .setOrigin(0.5)
      .setInteractive({ useHandCursor: true })
      .on('pointerdown', () => this.startBossDebug())

    const me = getPlayer()
    this.meText = this.add
      .text(W - 20, H - 34, '', { color: '#9aa1bd', fontSize: '16px' })
      .setOrigin(1, 0)
      .setInteractive({ useHandCursor: true })
      .on('pointerdown', () => {
        const name = window.prompt('换个响亮的名号（≤12字）：', me.name)
        if (name) {
          setPlayerName(name)
          this.refreshMe()
        }
      })

    this.balanceText = this.add
      .text(W - 20, 30, '余额 …', { color: '#ffd23f', fontSize: '18px' })
      .setOrigin(1, 0)
    this.buildSkinCards()
    void this.refreshWallet()

    this.refreshMe()
    void this.refreshBoard()

    this.add
      .text(20, H - 34, '⚙ 灵敏度', { color: '#9aa1bd', fontSize: '16px' })
      .setInteractive({ useHandCursor: true })
      .on('pointerdown', () => this.sensPanel.setVisible(!this.sensPanel.visible))
    this.buildSensPanel()

    window.addEventListener('game-action', this.onGameAction)
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => {
      window.removeEventListener('game-action', this.onGameAction)
    })

    this.input.keyboard!.once('keydown-SPACE', () => this.startGame(undefined))
  }

  update(time: number) {
    if (this.sensPanel?.visible) {
      this.sensFill.width = Math.max(1, mic.level() * 160)
    }
    // 开局挂起：必须"播报来过且已放完"才开局——只看"没在播"会在台词
    // 还没生成时误判，开局断连就会掐掉即将到来的倒计时。
    // 播报始终不来（4s）或播得异常长（12s）时兜底放行。
    if (this.pendingSeed !== null) {
      const elapsed = time - this.prepareAt
      if (ttsArrivedAndDone() || elapsed > 12000 || (elapsed > 4000 && !ttsArrived())) {
        const seed = this.pendingSeed
        this.pendingSeed = null
        resetTTSTracking()
        this.startGame(seed ?? undefined)
      }
    }
    if (time - this.lastReport > 500) {
      this.lastReport = time
      reportGameState({ playing: false, dist: 0, deaths: 0, won: false })
    }
  }

  private async refreshBoard() {
    try {
      const resp = await fetch('/api/leaderboard')
      const data = (await resp.json()) as { players: Row[] }
      this.rows = data.players ?? []
    } catch {
      this.rows = []
    }
    const me = getPlayer()
    const lines: string[] = []
    if (this.rows.length === 0) {
      lines.push('虚位以待——成为第一个上榜的人')
    }
    this.rows.forEach((r, i) => {
      const medal = ['🥇', '🥈', '🥉'][i] ?? `${i + 1}.`
      const tag = r.id === me.id ? ' ◂ 你' : ''
      lines.push(`${medal}  ${r.name.padEnd(8, '　')} ${String(r.best_dist).padStart(4, ' ')} m   💰${r.total_coins}${tag}`)
    })
    this.boardText.setText(lines.join('\n')).setPosition(
      W / 2 - this.boardText.width / 2,
      200,
    )
  }

  // 角色卡纯展示：预览+价格/拥有状态。购买与切换只有语音一条路（Spec #23：不设点击）
  private buildSkinCards() {
    // Scene 实例跨 restart 复用，上次会话的卡片引用已销毁，必须先清空
    this.skinCards = []
    const spots: { id: SkinId; cx: number; cy: number }[] = [
      { id: 'bean', cx: 140, cy: 405 },
      { id: 'wave', cx: 820, cy: 405 },
    ]
    for (const spot of spots) {
      const bg = this.add.rectangle(spot.cx, spot.cy, 190, 170, 0x171826).setStrokeStyle(2, 0x3a3d5c)
      const preview = buildSkin(this, spot.id)
      preview.setScale(0.85)
      preview.setPosition(spot.cx, spot.cy - 35)
      startIdleBreathing(this, preview)
      this.add
        .text(spot.cx, spot.cy + 15, spot.id === 'bean' ? '跳跳豆' : '声波精灵', { color: '#ffffff', fontSize: '16px' })
        .setOrigin(0.5)
      const status = this.add.text(spot.cx, spot.cy + 45, '', { fontSize: '14px' }).setOrigin(0.5)
      this.skinCards.push({ id: spot.id, bg, status })
    }
    this.updateSkinCards({ owned: ['bean'], active: this.activeSkin })
  }

  private updateSkinCards(w: { owned: string[]; active: string }) {
    for (const card of this.skinCards) {
      const owned = w.owned.includes(card.id)
      const active = w.active === card.id
      card.bg.setStrokeStyle(2, active ? 0x7a6ff0 : 0x3a3d5c)
      if (active) {
        card.status.setColor('#7a6ff0')
        card.status.setText('● 穿着中')
      } else if (owned) {
        card.status.setColor('#38b764')
        card.status.setText('已拥有')
      } else {
        card.status.setColor('#ffd23f')
        card.status.setText('💰 ' + SKIN_PRICE + ' 解锁')
      }
    }
  }

  private async refreshWallet() {
    const w = await fetchWallet()
    this.activeSkin = w.active
    this.balanceText.setText('余额 💰' + String(w.balance))
    this.updateSkinCards(w)
  }

  private refreshMe() {
    this.meText.setText(`${getPlayer().name}（点击改名）`)
  }

  // 灵敏度面板：不同麦克风/嗓门差异大，玩家自己校准（实时预览条）。
  // Scene 实例跨 restart 复用，上次会话的按钮引用已销毁，必须先清空。
  private buildSensPanel() {
    this.sensBtns = []
    const p = this.add.container(W / 2, H / 2).setVisible(false).setDepth(10)
    p.add(this.add.rectangle(0, 0, 480, 250, 0x0f0e1a, 0.95).setStrokeStyle(2, 0x7a6ff0))
    p.add(
      this.add
        .text(0, -96, '声音灵敏度', { color: '#ffe6a7', fontSize: '22px', fontStyle: 'bold' })
        .setOrigin(0.5),
    )
    this.sensLabel = this.add.text(0, -60, '', { color: '#ffffff', fontSize: '16px' }).setOrigin(0.5)
    p.add(this.sensLabel)
    ;[0.6, 0.8, 1.0, 1.3, 1.6].forEach((v, i) => {
      const t = this.add
        .text(-184 + i * 92, -14, `${v}×`, { color: '#cfd2e8', fontSize: '18px' })
        .setOrigin(0.5)
        .setInteractive({ useHandCursor: true })
        .on('pointerdown', () => {
          mic.setSensitivity(v)
          this.refreshSensPanel()
        })
      this.sensBtns.push({ v, t })
      p.add(t)
    })
    p.add(
      this.add
        .text(0, 24, '喊不动调高 · 轻声就跳调低', { color: '#566079', fontSize: '14px' })
        .setOrigin(0.5),
    )
    p.add(this.add.rectangle(-80, 58, 160, 12, 0x000000).setOrigin(0, 0.5).setAlpha(0.5))
    this.sensFill = this.add.rectangle(-80, 58, 1, 12, 0x38b764).setOrigin(0, 0.5)
    p.add(this.sensFill)
    p.add(
      this.add
        .text(0, 88, '对着麦克风喊一声——绿条能顶到头就对了', { color: '#9aa1bd', fontSize: '13px' })
        .setOrigin(0.5),
    )
    this.sensPanel = p
    this.refreshSensPanel()
  }

  private refreshSensPanel() {
    this.sensLabel.setText(`当前 ${mic.sensitivity.toFixed(1)}×`)
    for (const { v, t } of this.sensBtns) {
      t.setColor(v === mic.sensitivity ? '#ffd23f' : '#cfd2e8')
    }
  }

  private startGame(seed?: number) {
    this.scene.start('play', { seed, skin: this.activeSkin })
  }

  // 调试通道：跳过跑酷直进 Boss 战（演示/联调，金币按固定原额计）
  private startBossDebug() {
    reportGameState({ playing: false, phase: 'boss', dist: 0, deaths: 0, won: false })
    sendBossEnter(20, getPlayer().name)
    this.scene.start('boss', { coins: 20, dist: 0, deaths: 0 })
  }
}
