// 麦克风音量检测：getUserMedia + AnalyserNode 输出 0..1 平滑包络。
// 实时判定必须本地完成，不走灵矽链路（ASR 往返在秒级，见 ADR-0001）。

export const JUMP_THRESHOLD = 0.15
const MIN_JUMP_VELOCITY = 320
const MAX_JUMP_VELOCITY = 820

// 音量包络 → 起跳初速；低于阈值视为没在喊，返回 null
export function levelToPower(level: number): number | null {
  if (level < JUMP_THRESHOLD) return null
  const t = Math.min((level - JUMP_THRESHOLD) / (1 - JUMP_THRESHOLD), 1)
  return MIN_JUMP_VELOCITY + t * (MAX_JUMP_VELOCITY - MIN_JUMP_VELOCITY)
}

type MicState = 'idle' | 'requesting' | 'running' | 'denied'

class Mic {
  private ctx?: AudioContext
  private analyser?: AnalyserNode
  private buf?: Uint8Array
  private envelope = 0
  state: MicState = 'idle'

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

  level(): number {
    if (!this.analyser || !this.buf) return 0
    this.analyser.getByteTimeDomainData(this.buf)
    let sum = 0
    for (let i = 0; i < this.buf.length; i++) {
      const v = (this.buf[i] - 128) / 128
      sum += v * v
    }
    const rms = Math.sqrt(sum / this.buf.length)
    // 喊声 RMS 通常 0.1~0.3，×4 拉到满量程
    const target = Math.min(rms * 4, 1)
    // attack 快、release 慢：起跳跟手，松口不抖
    this.envelope = target > this.envelope ? target : this.envelope * 0.85
    return this.envelope
  }
}

export const mic = new Mic()
