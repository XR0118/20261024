import Phaser from 'phaser'
import { mic } from '../audio/mic'
import { reportGameState, sendBossAction } from '../audio/voice'
import { applyBossEvents, initialBossUi, type BossEvent, type BossUiState } from '../game/bossui'
import { BLOCK_WINDOW_MS, blockedByShout } from '../game/qte'
import { reportRun } from '../player'
import { FONT, neonPanel } from '../game/theme'

const W = 960
const H = 540

// Boss 战场景：渲染 bossui 状态 + 键盘/点击输入。语音输入走
// STT→服务端引擎→boss-events 事件流，场景只消费事件、不做判定，
// 保证无语音（键盘/点击调试通道）时同样可跑通全流程。
export class BossScene extends Phaser.Scene {
  private ui: BossUiState = initialBossUi()
  private buffer = ''
  private run = { coins: 0, dist: 0, deaths: 0 }
  private reported = false
  private lastReport = 0
  private qteStartedAt = -1
  private qteSamples: number[] = []

  private hintText!: Phaser.GameObjects.Text
  private mySecretText!: Phaser.GameObjects.Text
  private rowsText!: Phaser.GameObjects.Text
  private revealText!: Phaser.GameObjects.Text
  private bufferText!: Phaser.GameObjects.Text
  private banner!: Phaser.GameObjects.Text
  private blockOkBtn!: Phaser.GameObjects.Container
  private blockFailBtn!: Phaser.GameObjects.Container
  private declineSwapBtn!: Phaser.GameObjects.Container

  constructor() {
    super('boss')
  }

  init(data: { coins?: number; dist?: number; deaths?: number }) {
    this.ui = initialBossUi()
    this.buffer = ''
    this.reported = false
    this.qteStartedAt = -1
    this.qteSamples = []
    this.run = { coins: data.coins ?? 0, dist: data.dist ?? 0, deaths: data.deaths ?? 0 }
  }

  create() {
    this.cameras.main.setBackgroundColor('#12101e')
    // 圆角面板底板先建垫底，文字按原布局叠上：密数+情报 / 报数记录 / 中央提示+输入
    neonPanel(this, 12, 92, 330, 56)
    neonPanel(this, 12, 156, 560, 200)
    neonPanel(this, W / 2 - 190, 366, 380, 80)
    this.add
      .text(W / 2, 44, '⚔ 挑战者 · 猜数字对决', {
        ...FONT,
        color: '#ef476f',
        fontSize: '30px',
        fontStyle: 'bold',
      })
      .setOrigin(0.5)
    this.add
      .text(W / 2, 76, '报三个不重复数字 · 数字与位置都对才算命中', {
        ...FONT,
        color: '#7a6ff0',
        fontSize: '15px',
      })
      .setOrigin(0.5)

    this.mySecretText = this.add.text(24, 100, '我的密数：待上报', { ...FONT, color: '#ffd23f', fontSize: '16px' })
    this.revealText = this.add.text(24, 124, '亮位情报：暂无', { ...FONT, color: '#38b764', fontSize: '16px' })
    this.rowsText = this.add.text(24, 152, '', { ...FONT, color: '#cfd2e8', fontSize: '17px', lineSpacing: 6 })
    this.hintText = this.add
      .text(W / 2, 384, this.ui.hint, { ...FONT, color: '#ffe6a7', fontSize: '20px' })
      .setOrigin(0.5)
    this.bufferText = this.add
      .text(W / 2, 418, '－ － －', { ...FONT, color: '#ffffff', fontSize: '26px', fontStyle: 'bold' })
      .setOrigin(0.5)

    this.banner = this.add
      .text(W / 2, H / 2 - 40, '', { ...FONT, color: '#ffd23f', fontSize: '42px', fontStyle: 'bold' })
      .setOrigin(0.5)
      .setDepth(5)

    this.buildKeypad()
    this.buildStageButtons()

    window.addEventListener('boss-events', this.onEvents)
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => {
      window.removeEventListener('boss-events', this.onEvents)
    })
    this.input.keyboard!.on('keydown', (ev: KeyboardEvent) => this.onKey(ev.key))
    // 调试钩子：chrome 自动化/键盘外设备直接发 Boss 动作用（与 __game 同一先例）
    ;(window as unknown as { __bossAction: typeof sendBossAction }).__bossAction = sendBossAction
    this.redraw()
  }

  update(time: number) {
    if (this.ui.stage === 'ended') return
    this.updateQTE(time)
    if (time - this.lastReport > 500) {
      this.lastReport = time
      reportGameState({
        playing: false,
        phase: 'boss',
        dist: this.run.dist,
        deaths: this.run.deaths,
        won: false,
      })
    }
  }

  // 攻击-格挡 QTE：窗口内逐帧采样本地响度（无麦克风恒为 0，只能靠点击兜底）
  private updateQTE(time: number) {
    if (this.ui.stage !== 'attack' || this.qteStartedAt < 0) return
    this.qteSamples.push(mic.level())
    const remain = BLOCK_WINDOW_MS - (time - this.qteStartedAt)
    if (remain > 0) {
      this.hintText.setText(`喊——！！ ${(remain / 1000).toFixed(1)}s`)
      return
    }
    const blocked = blockedByShout(this.qteSamples)
    this.qteStartedAt = -1
    this.qteSamples = []
    this.hintText.setText(blocked ? '喊中格挡！' : '没喊出来……')
    sendBossAction('block', { blocked })
  }

  // 数字键盘是纯键盘外的调试兜底通道，不面板化（面板会与紧凑行距重叠）
  private buildKeypad() {
    const keys = ['1', '2', '3', '4', '5', '6', '7', '8', '9', '⌫', '0', 'OK']
    keys.forEach((k, i) => {
      const x = W / 2 - 132 + (i % 3) * 132
      const y = 468 + Math.floor(i / 3) * 22
      this.add
        .text(x, y, k, { ...FONT, color: '#9aa1bd', fontSize: '17px' })
        .setOrigin(0.5)
        .setInteractive({ useHandCursor: true })
        .on('pointerdown', () => this.onKey(k === '⌫' ? 'Backspace' : k === 'OK' ? 'Enter' : k))
    })
  }

  // 阶段按钮：攻击等待期给点击格挡兜底（喊声 QTE 接入前的过渡），
  // 换数抉择给放弃键；投降常驻
  private buildStageButtons() {
    const mk = (label: string, x: number, action: () => void) =>
      this.addButton(label, x, 60, action, '#ef476f')

    this.blockOkBtn = mk('格挡成功', W - 260, () => sendBossAction('block', { blocked: true }))
    this.blockFailBtn = mk('格挡失败', W - 140, () => sendBossAction('block', { blocked: false }))
    this.declineSwapBtn = mk('放弃换数', W - 260, () => sendBossAction('decline-swap'))
    this.addButton('投降', W - 90, 60, () => sendBossAction('surrender'), '#566079')
  }

  // 按钮统一为圆角底板+文字的容器：阶段按钮整组 show/hide，底板跟随
  private addButton(
    label: string,
    x: number,
    y: number,
    onClick: () => void,
    color = '#ffffff',
  ) {
    const w = Math.max(56, label.length * 17 + 26)
    const c = this.add.container(x, y)
    c.add(neonPanel(this, -w / 2, -16, w, 32))
    c.add(
      this.add
        .text(0, 0, label, { ...FONT, color, fontSize: '17px', fontStyle: 'bold' })
        .setOrigin(0.5)
        .setInteractive({ useHandCursor: true })
        .on('pointerdown', onClick),
    )
    return c
  }

  private onKey(key: string) {
    if (this.ui.stage === 'ended') return
    if (key >= '0' && key <= '9' && this.buffer.length < 3) {
      this.buffer += key
    } else if (key === 'Backspace') {
      this.buffer = this.buffer.slice(0, -1)
    } else if (key === 'Enter') {
      this.submitBuffer()
      return
    }
    this.redrawBuffer()
  }

  // 提交三位数字：按当前阶段选择动作——报密数/报数/换数在服务端同一引擎汇合
  private submitBuffer() {
    if (this.buffer.length !== 3) {
      // 本地校验的提示也走 reducer，避免绕过状态层被下个事件覆盖
      this.ui = applyBossEvents(this.ui, [{ kind: 'error', message: '要凑满三位数字再报' }])
      this.redraw()
      return
    }
    const action =
      this.ui.stage === 'awaiting-secret' ? 'secret' : this.ui.stage === 'swap' ? 'swap' : 'guess'
    sendBossAction(action, { digits: this.buffer })
    this.buffer = ''
    this.redrawBuffer()
  }

  private onEvents = (ev: Event) => {
    const evs = (ev as CustomEvent).detail as BossEvent[]
    this.ui = applyBossEvents(this.ui, evs)
    if (evs.some((e) => e.kind === 'attack')) {
      this.qteStartedAt = this.time.now
      this.qteSamples = []
    }
    this.redraw()
    if (this.ui.stage === 'ended') this.finishBattle()
  }

  private redraw() {
    this.hintText.setText(this.ui.hint)
    this.mySecretText.setText(this.ui.mySecret ? `我的密数：${this.ui.mySecret}` : '我的密数：待上报')
    const lines = this.ui.rows.map(
      (r) => `${r.who === '你' ? '你 ' : 'BOSS'} ${r.guess} → ${r.hits} 位命中`,
    )
    this.rowsText.setText(lines.length > 0 ? lines.join('\n') : '（还没有报数）')

    const entries = Object.entries(this.ui.revealed).sort(([a], [b]) => Number(a) - Number(b))
    this.revealText.setText(
      entries.length > 0
        ? `亮位情报：${entries.map(([p, d]) => `第${p}位=${d}`).join('  ')}`
        : '亮位情报：暂无',
    )

    this.blockOkBtn.setVisible(this.ui.stage === 'attack')
    this.blockFailBtn.setVisible(this.ui.stage === 'attack')
    this.declineSwapBtn.setVisible(this.ui.stage === 'swap')
    this.redrawBuffer()
  }

  private redrawBuffer() {
    const shown = (this.buffer.padEnd(3, '－')).split('').join(' ')
    this.bufferText.setText(this.ui.stage === 'ended' ? '' : shown)
  }

  // 战斗结束一次性结算：最终金币（胜=翻倍）入账后回首页待命
  private finishBattle() {
    if (this.reported || !this.ui.ended) return
    this.reported = true
    const { finalCoins, outcome } = this.ui.ended
    const win = outcome === 'win'
    const panel = neonPanel(this, W / 2 - 230, H / 2 - 72, 460, 144, win ? 0x38b764 : 0xef476f)
      .setDepth(4)
      .setScale(0.7)
    this.banner.setText(win ? '胜！金币翻倍' : '负·原额结算')
    this.banner.setDepth(5).setScale(0.7)
    const sub = this.add
      .text(W / 2, H / 2 + 8, `最终 💰 ${finalCoins}`, { ...FONT, color: '#ffe6a7', fontSize: '22px' })
      .setOrigin(0.5)
      .setDepth(5)
      .setScale(0.7)
    this.tweens.add({ targets: [panel, this.banner, sub], scale: 1, duration: 220, ease: 'Back.Out' })
    void reportRun(this.run.dist, finalCoins)
    this.time.delayedCall(3200, () => this.scene.start('boot'))
  }
}
