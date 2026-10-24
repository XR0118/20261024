import Phaser from 'phaser'
import { PlayScene } from './scenes/PlayScene'
import { BossScene } from './scenes/BossScene'
import { BootScene } from './scenes/BootScene'
import { mic } from './audio/mic'
import { attachMicUplink, startVoice } from './audio/voice'

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
  scene: [BootScene, PlayScene, BossScene],
})

// 调试钩子：chrome 自动化读取玩家状态用
;(window as unknown as { __game: Phaser.Game }).__game = game

// 页面加载即建语音 WS（下行与 Boss 战动作通道），麦克风授权就绪后再挂上行；
// 被拒则退化为纯键盘调试模式
startVoice()
void mic.start().then(() => {
  if (mic.state === 'running') void attachMicUplink()
})
