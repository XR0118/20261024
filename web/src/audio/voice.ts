// 语音上下行管线（ADR-0002）：共享麦克风流 → WebCodecs OPUS 编码 → WS 上行；
// WS 下行 TTS OPUS → 解码 → 排队播放。stt 触发的游戏动作以 game-action 事件广播。
import { mic } from './mic'

const UPSTREAM = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`

type Action = { action: string; params: Record<string, unknown> }

let ws: WebSocket | undefined

export function voiceListening(): boolean {
  return !!ws && ws.readyState === WebSocket.OPEN
}

export async function startVoice(): Promise<void> {
  const stream = mic.getStream()
  if (!stream) return

  ws = new WebSocket(UPSTREAM)
  ws.binaryType = 'arraybuffer'
  ws.onmessage = onMessage

  await encoderLoop(stream)
}

function onMessage(ev: MessageEvent) {
  if (typeof ev.data === 'string') {
    const msg = JSON.parse(ev.data) as { type: string; actions?: Action[]; listening?: boolean }
    if (msg.type === 'game-action' && msg.actions) {
      for (const a of msg.actions) {
        window.dispatchEvent(new CustomEvent('game-action', { detail: a }))
      }
    }
    if (msg.type === 'voice-state') {
      window.dispatchEvent(new CustomEvent('voice-state', { detail: { listening: msg.listening } }))
    }
    return
  }
  void decodeAndPlay(ev.data as ArrayBuffer)
}

// ---------------- 上行：PCM → OPUS ----------------

async function encoderLoop(stream: MediaStream) {
  const ctx = mic.getAudioContext()!
  const src = ctx.createMediaStreamSource(stream)

  const encoder = new AudioEncoder({
    output: (chunk) => {
      if (ws?.readyState !== WebSocket.OPEN) return
      const data = new Uint8Array(chunk.byteLength)
      chunk.copyTo(data)
      ws.send(data)
    },
    error: (e: DOMException) => console.error('opus encode', e),
  })
  // Chrome 的 WebCodecs opus 编码器仅支持 48kHz 输出（16k 会 configure 失败导致 codec 永久关闭）；
  // 灵矽侧按 opus 帧自描述采样率解码，16k/48k 由 golang hello 声明兜底
  encoder.configure({ codec: 'opus', sampleRate: 48000, numberOfChannels: 1, bitrate: 24000 })

  await loadWorklet(ctx)
  const node = new AudioWorkletNode(ctx, 'pcm-tap')
  src.connect(node)
  // 不接 destination：tap 只旁路采样，不出声

  node.port.onmessage = (e: MessageEvent) => {
    if (encoder.state !== 'configured') return // codec 挂掉后停发，避免错误刷屏
    const { samples, timestamp } = e.data as { samples: Float32Array; timestamp: number }
    const data = new AudioData({
      format: 'f32',
      sampleRate: ctx.sampleRate,
      numberOfFrames: samples.length,
      numberOfChannels: 1,
      timestamp,
      data: samples,
    })
    encoder.encode(data)
    data.close()
  }
}

// AudioWorklet 源码以 blob 注入，避免额外构建配置
let workletURL: string | undefined
async function loadWorklet(ctx: AudioContext) {
  if (!workletURL) {
    const code = `
class PCMTap extends AudioWorkletProcessor {
  process(inputs) {
    const ch = inputs[0][0]
    if (ch) {
      this.port.postMessage({ samples: new Float32Array(ch), timestamp: currentTime * 1e6 })
    }
    return true
  }
}
registerProcessor('pcm-tap', PCMTap)`
    workletURL = URL.createObjectURL(new Blob([code], { type: 'application/javascript' }))
  }
  await ctx.audioWorklet.addModule(workletURL)
  return true
}

// ---------------- 下行：OPUS → PCM → 播放 ----------------

let decoder: AudioDecoder | undefined
let playCtx: AudioContext | undefined
let lastEnd = 0

async function decodeAndPlay(buf: ArrayBuffer) {
  if (!decoder) {
    playCtx = new AudioContext()
    decoder = new AudioDecoder({
      output: (audio) => {
        const buf = makeBuffer(audio)
        audio.close()
        const t = playCtx!.currentTime
        // 按到达顺序排队，留 120ms 抖动余量
        const when = Math.max(t + 0.12, lastEnd)
        const src = playCtx!.createBufferSource()
        src.buffer = buf
        src.connect(playCtx!.destination)
        src.start(when)
        lastEnd = when + buf.duration
      },
      error: (e) => console.error('opus decode', e),
    })
    decoder.configure({ codec: 'opus', sampleRate: 24000, numberOfChannels: 1 })
  }
  decoder.decode(
    new EncodedAudioChunk({
      type: 'key',
      timestamp: 0,
      data: new Uint8Array(buf),
    }),
  )
}

function makeBuffer(audio: AudioData): AudioBuffer {
  const out = playCtx!.createBuffer(audio.numberOfChannels, audio.numberOfFrames, audio.sampleRate)
  const f32 = new Float32Array(audio.numberOfFrames * audio.numberOfChannels)
  audio.copyTo(f32, { planeIndex: 0, format: 'f32' })
  out.copyToChannel(f32, 0)
  return out
}

// ---------------- 游戏状态上行 ----------------

let lastState = ''
export function reportGameState(state: unknown) {
  if (ws?.readyState !== WebSocket.OPEN) return
  const payload = JSON.stringify({ type: 'game-state', state })
  if (payload === lastState) return // 游戏每帧都报，粗去重降低上行噪音
  lastState = payload
  ws.send(payload)
}
