/**
 * 麦克风录音：枚举设备 → 按一下开始 / 再按一下停 → 编成 WAV。
 *
 * 为什么自己编 WAV 而不用 MediaRecorder 直接产出的 webm/opus：
 * 录像要能长期保存、随手用系统播放器打开听，WAV 是最没有前提的格式；
 * 而且转一次 16kHz 单声道能把它压到 32KB/s，一段一分钟的讲解才 2MB。
 *
 * 为什么列表要「显式」列设备：`enumerateDevices` 在没授权之前拿到的 label 是空的，
 * 所以必须先 getUserMedia 要一次权限（拿到就立刻关掉），设备名才显示得出来。
 *
 * 两个给界面用的附加产物：
 *   - `readWave()`  录制中的**实时波形**（AnalyserNode 的时域数据），HUD 画滚动折线。
 * 录完之后那条静态波形不在这里算：音频已经写进服务端了，从 URL 取回来解码一次
 * 更可靠（录制端和回放端用的是同一份代码，见 audio/waveform.ts）。
 */

export interface MicDevice {
  id: string
  label: string
}

export function micSupported(): boolean {
  if (typeof MediaRecorder === 'undefined') return false
  return typeof navigator.mediaDevices !== 'undefined' && 'getUserMedia' in navigator.mediaDevices
}

/** 列出可用的麦克风。没授权时 label 是空串，这里给一个「麦克风 N」的兜底名。 */
export async function listMics(): Promise<MicDevice[]> {
  if (!navigator.mediaDevices?.enumerateDevices) return []
  const all = await navigator.mediaDevices.enumerateDevices()
  return all
    .filter((d) => d.kind === 'audioinput')
    .map((d, i) => ({ id: d.deviceId, label: d.label || `麦克风 ${i + 1}` }))
}

/**
 * 要一次麦克风权限。**只为把设备名读出来**：不开这一下，设备下拉里全是"麦克风 1/2"，
 * 用户根本不知道哪个是他桌上的那个。
 */
export async function requestMicPermission(): Promise<boolean> {
  if (!micSupported()) return false
  try {
    const stream = await navigator.mediaDevices.getUserMedia({ audio: true })
    stream.getTracks().forEach((t) => t.stop())
    return true
  } catch {
    return false
  }
}

function pickMime(): string {
  const candidates = ['audio/webm;codecs=opus', 'audio/webm', 'audio/ogg;codecs=opus', 'audio/mp4']
  for (const m of candidates) {
    if (typeof MediaRecorder !== 'undefined' && MediaRecorder.isTypeSupported(m)) return m
  }
  return ''
}

/** 一段录音的成品：WAV 字节 + 时长。波形另外算（见 audio/waveform.ts）。 */
export interface ClipResult {
  wav: Uint8Array
  durationMs: number
}

/** 一次录音（按一下开始、再按一下停）的抓取器。 */
export class ClipRecorder {
  private stream: MediaStream | null = null
  private rec: MediaRecorder | null = null
  private chunks: Blob[] = []
  private startedAt = 0
  private active = false

  // 实时波形：AnalyserNode 挂在同一条 stream 上，只读不写，不影响录制本身
  private actx: AudioContext | null = null
  private analyser: AnalyserNode | null = null
  private waveBuf: Float32Array | null = null

  get recording(): boolean {
    return this.active
  }

  /** 开始抓。deviceId 为空 = 系统默认设备。 */
  async start(deviceId: string): Promise<void> {
    if (this.active) return
    if (!micSupported()) throw new Error('这个浏览器不支持录音（需要 getUserMedia + MediaRecorder）')
    const audio: MediaTrackConstraints = {}
    if (deviceId && deviceId !== 'default') audio.deviceId = { exact: deviceId }
    // 人声讲解：开降噪和自动增益，关掉回声消除（它不是通话，不该被对方的静音门限吃掉）
    audio.echoCancellation = false
    audio.noiseSuppression = true
    audio.autoGainControl = true

    this.stream = await navigator.mediaDevices.getUserMedia({ audio })
    const mime = pickMime()
    this.rec = new MediaRecorder(this.stream, mime ? { mimeType: mime } : undefined)
    this.chunks = []
    this.rec.ondataavailable = (ev) => {
      if (ev.data.size > 0) this.chunks.push(ev.data)
    }
    this.rec.start(120)
    this.startedAt = performance.now()
    this.active = true
    this.startAnalyser()
  }

  /**
   * 读一帧时域波形（-1..1），给 HUD 画实时折线。
   *
   * 返回的是**内部复用的缓冲区**，调用方应当立刻画掉，不要存起来 ——
   * 每秒 60 次分配一个新 Float32Array 只为了画 200 个点，没必要。
   */
  readWave(): Float32Array | null {
    if (!this.analyser || !this.waveBuf) return null
    this.analyser.getFloatTimeDomainData(this.waveBuf as Float32Array<ArrayBuffer>)
    return this.waveBuf
  }

  private startAnalyser(): void {
    if (!this.stream) return
    try {
      const Ctx =
        window.AudioContext ||
        (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
      if (!Ctx) return
      this.actx = new Ctx()
      const src = this.actx.createMediaStreamSource(this.stream)
      const an = this.actx.createAnalyser()
      an.fftSize = 1024
      an.smoothingTimeConstant = 0.5
      src.connect(an)
      this.analyser = an
      this.waveBuf = new Float32Array(an.fftSize)
      // 有些浏览器建出来的 AudioContext 是 suspended（自动播放策略），
      // 不 resume 的话波形是一条直线 —— 不影响录音，只影响好看。
      void this.actx.resume()
    } catch {
      // 画不出波形不影响录音，安静降级
      this.analyser = null
      this.waveBuf = null
    }
  }

  /**
   * 结束并编码成 WAV。
   *
   * 太短的录音（<250ms）直接丢：按一下空格马上又按一下产生的「噗」既没有信息，
   * 还会白占一条 9 条上限里的名额。
   */
  async stop(): Promise<ClipResult | null> {
    if (!this.active || !this.rec) {
      this.cleanup()
      return null
    }
    const rec = this.rec
    const elapsed = performance.now() - this.startedAt
    const blob = await new Promise<Blob>((resolve) => {
      rec.onstop = () => resolve(new Blob(this.chunks, { type: rec.mimeType || 'audio/webm' }))
      rec.stop()
    })
    this.cleanup()
    if (elapsed < 250 || blob.size === 0) return null
    const out = await blobToWav(blob)
    if (out.durationMs < 250) return null
    return out
  }

  cancel(): void {
    if (this.rec && this.rec.state !== 'inactive') {
      this.rec.onstop = null
      this.rec.stop()
    }
    this.cleanup()
  }

  private cleanup(): void {
    this.active = false
    this.rec = null
    this.stream?.getTracks().forEach((t) => t.stop())
    this.stream = null
    this.chunks = []
    this.analyser = null
    this.waveBuf = null
    if (this.actx) {
      void this.actx.close().catch(() => {})
      this.actx = null
    }
  }
}

const TARGET_RATE = 16000

/** 任意浏览器能解码的音频 → 16kHz / 16bit / 单声道 WAV。 */
export async function blobToWav(blob: Blob): Promise<ClipResult> {
  const bytes = await blob.arrayBuffer()
  const Ctx = window.AudioContext || (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
  const ctx = new Ctx()
  let audio: AudioBuffer
  try {
    audio = await ctx.decodeAudioData(bytes)
  } finally {
    void ctx.close()
  }

  const srcLen = audio.length
  const ratio = audio.sampleRate / TARGET_RATE
  const outLen = Math.max(1, Math.floor(srcLen / ratio))
  const chans: Float32Array[] = []
  for (let c = 0; c < audio.numberOfChannels; c++) chans.push(audio.getChannelData(c))

  const pcm = new Int16Array(outLen)
  for (let i = 0; i < outLen; i++) {
    const pos = i * ratio
    const i0 = Math.floor(pos)
    const i1 = Math.min(srcLen - 1, i0 + 1)
    const frac = pos - i0
    let sum = 0
    for (const ch of chans) {
      const a = ch[i0] ?? 0
      const b = ch[i1] ?? a
      sum += a + (b - a) * frac
    }
    let v = sum / chans.length
    v = v < -1 ? -1 : v > 1 ? 1 : v
    pcm[i] = v < 0 ? v * 0x8000 : v * 0x7fff
  }
  return { wav: encodeWav(pcm, TARGET_RATE), durationMs: Math.round(audio.duration * 1000) }
}

function encodeWav(pcm: Int16Array, rate: number): Uint8Array {
  const buf = new ArrayBuffer(44 + pcm.length * 2)
  const view = new DataView(buf)
  const str = (off: number, s: string) => {
    for (let i = 0; i < s.length; i++) view.setUint8(off + i, s.charCodeAt(i))
  }
  str(0, 'RIFF')
  view.setUint32(4, 36 + pcm.length * 2, true)
  str(8, 'WAVE')
  str(12, 'fmt ')
  view.setUint32(16, 16, true)
  view.setUint16(20, 1, true) // PCM
  view.setUint16(22, 1, true) // 单声道
  view.setUint32(24, rate, true)
  view.setUint32(28, rate * 2, true)
  view.setUint16(32, 2, true)
  view.setUint16(34, 16, true)
  str(36, 'data')
  view.setUint32(40, pcm.length * 2, true)
  for (let i = 0; i < pcm.length; i++) view.setInt16(44 + i * 2, pcm[i], true)
  return new Uint8Array(buf)
}

/** Uint8Array → base64。分段拼，直接 apply 几 MB 的参数会爆栈。 */
export function toBase64(bytes: Uint8Array): string {
  let s = ''
  const chunk = 0x8000
  for (let i = 0; i < bytes.length; i += chunk) {
    s += String.fromCharCode(...bytes.subarray(i, i + chunk))
  }
  return btoa(s)
}
