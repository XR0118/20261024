// 麦克风音量检测：getUserMedia + AnalyserNode 输出 0..1 平滑包络。
// 实时判定必须本地完成，不走灵矽链路（ASR 往返在秒级，见 ADR-0001）。

// 阈值偏高：呼吸/环境噪声不应触发跳跃，只有"喊"才算指令
export const JUMP_THRESHOLD = 0.3
const MIN_JUMP_VELOCITY = 380
// 960 初速对应跳高 ≈328px、滞空水平 ≈329px，覆盖最深 280px 的坑
const MAX_JUMP_VELOCITY = 960

// 音量包络 → 起跳初速；低于阈值视为没在喊，返回 null
export function levelToPower(level: number): number | null {
  if (level < JUMP_THRESHOLD) return null
  const t = Math.min((level - JUMP_THRESHOLD) / (1 - JUMP_THRESHOLD), 1)
  return MIN_JUMP_VELOCITY + t * (MAX_JUMP_VELOCITY - MIN_JUMP_VELOCITY)
}

const SENS_KEY = 'loudleap_sens'

// scaleLevel 响度 → 灵敏度缩放后的判定值：不同麦克风/房间/嗓门差异
// 很大（喊不动调高、轻声就跳调低），系数作用于 level() 输出——
// 跳跃/喷气/QTE 格挡/音量条全链路同步缩放。
export function scaleLevel(rms: number, sensitivity: number): number {
  return Math.min(rms * 4 * sensitivity, 1)
}

function loadSensitivity(): number {
  if (typeof localStorage === 'undefined') return 1 // 测试环境（node）无 localStorage
  const v = Number(localStorage.getItem(SENS_KEY))
  if (!Number.isFinite(v) || v < 0.4 || v > 2.5) return 1
  return v
}

type MicState = 'idle' | 'requesting' | 'running' | 'denied'

class Mic {
  private ctx?: AudioContext
  private analyser?: AnalyserNode
  private buf?: Uint8Array
  private envelope = 0
  private stream?: MediaStream
  state: MicState = 'idle'
  sensitivity = loadSensitivity()

  setSensitivity(v: number) {
    this.sensitivity = Math.min(2.5, Math.max(0.4, v))
    if (typeof localStorage !== 'undefined') {
      localStorage.setItem(SENS_KEY, String(this.sensitivity))
    }
  }

  async start(): Promise<void> {
    try {
      this.state = 'requesting'
      // 降噪/AGC 会削掉喊声峰值，判定必须拿原始信号
      const stream = await navigator.mediaDevices.getUserMedia({
        audio: {
          echoCancellation: false,
          noiseSuppression: false,
          autoGainControl: false,
        },
      })
      this.stream = stream
      this.ctx = new AudioContext()
      const src = this.ctx.createMediaStreamSource(stream)
      this.analyser = this.ctx.createAnalyser()
      this.analyser.fftSize = 1024
      src.connect(this.analyser)
      this.buf = new Uint8Array(this.analyser.fftSize)
      this.state = 'running'
    } catch {
      this.state = 'denied'
    }
  }

  // 麦克风单源（ADR-0002）：音量判定与语音上行共用这一个流
  getStream(): MediaStream | undefined {
    return this.stream
  }

  getAudioContext(): AudioContext | undefined {
    return this.ctx
  }

  level(): number {
    if (!this.analyser || !this.buf) return 0
    this.analyser.getByteTimeDomainData(this.buf)
    let sum = 0
    for (let i = 0; i < this.buf.length; i++) {
      const v = (this.buf[i] - 128) / 128
      sum += v * v
    }
    const rms = Math.sqrt(sum / this.buf.length)
    const target = scaleLevel(rms, this.sensitivity)
    // attack 快、release 较快：余响尽快衰减，避免落地时残留包络再次触发起跳
    this.envelope = target > this.envelope ? target : this.envelope * 0.7
    return this.envelope
  }
}

export const mic = new Mic()
