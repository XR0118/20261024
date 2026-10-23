import Phaser from 'phaser'
import { reportGameState } from '../audio/voice'
import { getPlayer, setPlayerName } from '../player'

const W = 960
const H = 540

type Row = { ID: string; Name: string; BestDist: number; TotalCoins: number; Runs: number }

// 初始页：排行榜 + 开始入口。语音指令（game-action）在本页即可触发开局。
export class BootScene extends Phaser.Scene {
  private rows: Row[] = []
  private boardText!: Phaser.GameObjects.Text
  private meText!: Phaser.GameObjects.Text
  private lastReport = 0
  private onGameAction = (ev: Event) => {
    const a = (ev as CustomEvent).detail as { action: string; params: { seed?: number } }
    if (a.action === 'start') this.startGame(undefined)
    if (a.action === 'load_level') this.startGame(Number(a.params?.seed ?? Math.random() * 1e9))
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

    this.add
      .text(W / 2, 448, '喊「你好小智」唤醒，再说「开始游戏」', {
        color: '#ffe6a7',
        fontSize: '20px',
      })
      .setOrigin(0.5)
    this.add
      .text(W / 2, 478, '调试：按空格直接开始', { color: '#566079', fontSize: '15px' })
      .setOrigin(0.5)

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

    this.refreshMe()
    void this.refreshBoard()

    window.addEventListener('game-action', this.onGameAction)
    this.events.once(Phaser.Scenes.Events.SHUTDOWN, () => {
      window.removeEventListener('game-action', this.onGameAction)
    })

    this.input.keyboard!.once('keydown-SPACE', () => this.startGame(undefined))
  }

  update(time: number) {
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
      const tag = r.ID === me.id ? ' ◂ 你' : ''
      lines.push(`${medal}  ${r.Name.padEnd(8, '　')} ${String(r.BestDist).padStart(4, ' ')} m   💰${r.TotalCoins}${tag}`)
    })
    this.boardText.setText(lines.join('\n')).setPosition(
      W / 2 - this.boardText.width / 2,
      200,
    )
  }

  private refreshMe() {
    this.meText.setText(`${getPlayer().name}（点击改名）`)
  }

  private startGame(seed?: number) {
    this.scene.start('play', { seed })
  }
}
