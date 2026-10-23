import Phaser from 'phaser'
import { PlayScene } from './scenes/PlayScene'
import { mic } from './audio/mic'

const game = new Phaser.Game({
  type: Phaser.AUTO,
  parent: 'app',
  width: 960,
  height: 540,
  physics: {
    default: 'arcade',
    arcade: { gravity: { x: 0, y: 1400 }, debug: false },
  },
  scale: { mode: Phaser.Scale.FIT, autoCenter: Phaser.Scale.CENTER_BOTH },
  scene: [PlayScene],
})

// 调试钩子：chrome 自动化读取玩家状态用
;(window as unknown as { __game: Phaser.Game }).__game = game

// 页面加载即请求麦克风授权；被拒则退化为纯空格键调试模式
void mic.start()
