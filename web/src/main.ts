import Phaser from 'phaser'
import { PlayScene } from './scenes/PlayScene'
import { BootScene } from './scenes/BootScene'
import { mic } from './audio/mic'
import { startVoice } from './audio/voice'

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
  scene: [BootScene, PlayScene],
})

// 调试钩子：chrome 自动化读取玩家状态用
;(window as unknown as { __game: Phaser.Game }).__game = game

// 页面加载即请求麦克风授权；被拒则退化为纯空格键调试模式。
// 授权成功后启动语音上下行（与音量判定共用同一个麦克风流）
void mic.start().then(() => {
  if (mic.state === 'running') void startVoice()
})
