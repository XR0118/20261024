// 语音上下行管线（ADR-0002）：共享麦克风流 → WebCodecs OPUS 编码 → WS 上行；
// WS 下行 TTS OPUS → 解码 → 排队播放。stt 触发的游戏动作以 game-action 事件广播。
import { mic } from './mic'

const UPSTREAM = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`

type Action = { action: string; params: Record<string, unknown> }

let ws: WebSocket | undefined

export function voiceListening(): boolean {
  return !!ws && ws.readyState === WebSocket.OPEN
}

// startVoice 立即建 WS（下行与 Boss 战动作通道不等麦克风——授权弹窗
// 挂起或被拒时键盘照样可玩）；上行编码由 attachMicUplink 就绪后挂上。
export function startVoice(): void {
  if (ws) return
  ws = new WebSocket(UPSTREAM)
  ws.binaryType = 'arraybuffer'
  ws.onmessage = onMessage
}

export async function attachMicUplink(): Promise<void> {
  const stream = mic.getStream()
  if (!stream || uplinkStarted) return
  uplinkStarted = true
  await encoderLoop(stream)
}

let uplinkStarted = false

let ttsRemotePlaying = false // 平台侧 TTS 进行中（tts-state 事件）
let sawTTS = false // 本次对话平台确实下发过播报（区分"没开始"与"已结束"）

function onMessage(ev: MessageEvent) {
  if (typeof ev.data === 'string') {
    const msg = JSON.parse(ev.data) as {
      type: string
      actions?: Action[]
      listening?: boolean
      playing?: boolean
      events?: unknown[]
    }
    if (msg.type === 'game-action' && msg.actions) {
      for (const a of msg.actions) {
        window.dispatchEvent(new CustomEvent('game-action', { detail: a }))
      }
    }
    if (msg.type === 'boss-events' && msg.events) {
      window.dispatchEvent(new CustomEvent('boss-events', { detail: msg.events }))
    }
    if (msg.type === 'voice-state') {
      window.dispatchEvent(new CustomEvent('voice-state', { detail: { listening: msg.listening } }))
    }
    if (msg.type === 'tts-state') {
      ttsRemotePlaying = !!msg.playing
      if (ttsRemotePlaying) sawTTS = true
    }
    return
  }
  sawTTS = true
  void decodeAndPlay(ev.data as ArrayBuffer)
}

// "播报完毕" = 平台 TTS 已停 && 本地播放队列排空（jitter 起播缓冲的尾音也放完）。
// 只有浏览器知道本地队列何时结束，这个判定不能放在 golang 侧。
export function ttsDone(): boolean {
  if (ttsRemotePlaying) return false
  if (playCtx && lastEnd > playCtx.currentTime + 0.05) return false
  return true
}

// 播报"来过且已放完"。仅 ttsDone() 不够：台词还没生成时它也为 true，
// 若此时开局会掐掉即将到来的播报。
export function ttsArrivedAndDone(): boolean {
  return sawTTS && ttsDone()
}

// 播报是否已开始下发（用于"迟迟不来"的兜底判断）
export function ttsArrived(): boolean {
  return sawTTS
}

// 每轮对话重置"来过"标记（开局等待的判定窗口）
export function resetTTSTracking() {
  sawTTS = false
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

// Boss 战动作上行：进场（携本局金币原额与玩家名——记忆摘要按名区分试玩者）
// 与显式动作（键盘/点击调试通道，与语音通道在服务端汇入同一引擎）
export function sendBossEnter(coins: number, name: string) {
  sendJSON({ type: 'boss-enter', coins, name })
}

export function sendBossAction(action: string, extra: Record<string, unknown> = {}) {
  sendJSON({ type: 'boss-action', action, ...extra })
}

// 途中局终（掉坑/撞杆，未到 Boss 战）通知服务端按准确死因写记忆片段
export function sendRunEnded(dist: number, coins: number, name: string, cause: 'pit' | 'bar') {
  sendJSON({ type: 'run-ended', dist, coins, name, cause })
}

function sendJSON(payload: Record<string, unknown>) {
  if (ws?.readyState !== WebSocket.OPEN) return
  ws.send(JSON.stringify(payload))
}
