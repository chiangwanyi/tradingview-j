/**
 * 录音的波形（包络）：从 URL 取音频、解码、分桶取峰值。
 *
 * 为什么不在录完的那一刻顺手算好、跟着消息一起传上来：
 *   1. 回放端拿到的录音**本来就只有 URL**（音频不进 JSON），两条路各写一份
 *      迟早会不一致；
 *   2. 服务端算的话要给 Go 加一条解码链，与「零第三方依赖」冲突。
 * 所以统一走「取回来解码一次」—— 16kHz 单声道 30 秒也就 1MB，本地服务，
 * 一次算完缓存在内存里，翻列表时不会重复解码。
 *
 * 画的是**响度包络**而不是逐点示波器：96 个桶足够看出「哪里在说话、哪段是静音、
 * 有没有真的录到东西」，而一行 200px 宽的地方画 16000 个采样只会得到一片黑。
 */

const BUCKETS = 96

/** PCM（Int16）→ 96 个 0..1 的峰值。 */
export function peaksOf(pcm: Int16Array, buckets = BUCKETS): number[] {
  const out = new Array<number>(buckets).fill(0)
  const n = pcm.length
  if (n === 0) return out
  for (let b = 0; b < buckets; b++) {
    const start = Math.floor((b * n) / buckets)
    const end = Math.max(start + 1, Math.floor(((b + 1) * n) / buckets))
    let max = 0
    for (let i = start; i < end && i < n; i++) {
      const v = Math.abs(pcm[i]) / 32768
      if (v > max) max = v
    }
    out[b] = max
  }
  return out
}

/** 已经算过的波形（key = 音频 URL）。音频写完之后不会再变，可以放心缓存。 */
const cache = new Map<string, number[]>()
/** 正在算的（同一个 URL 被列表里两处同时要时不重复 fetch）。 */
const inflight = new Map<string, Promise<number[] | null>>()

export function cachedPeaks(url: string): number[] | null {
  return cache.get(url) ?? null
}

/**
 * 取一段录音的包络。失败返回 null（调用方画一条平线就行，不要弹错 ——
 * 波形只是"顺带看看"，不该因为算不出波形就把播放也拦住）。
 */
export async function clipPeaks(url: string): Promise<number[] | null> {
  if (!url) return null
  const hit = cache.get(url)
  if (hit) return hit
  const running = inflight.get(url)
  if (running) return running

  const job = (async () => {
    try {
      const resp = await fetch(url)
      if (!resp.ok) return null
      const bytes = await resp.arrayBuffer()
      const Ctx =
        window.AudioContext ||
        (window as unknown as { webkitAudioContext: typeof AudioContext }).webkitAudioContext
      if (!Ctx) return null
      const ctx = new Ctx()
      let audio: AudioBuffer
      try {
        audio = await ctx.decodeAudioData(bytes)
      } finally {
        void ctx.close()
      }
      const ch = audio.getChannelData(0)
      const pcm = new Int16Array(ch.length)
      for (let i = 0; i < ch.length; i++) {
        const v = ch[i] < -1 ? -1 : ch[i] > 1 ? 1 : ch[i]
        pcm[i] = v < 0 ? v * 0x8000 : v * 0x7fff
      }
      const peaks = peaksOf(pcm)
      cache.set(url, peaks)
      return peaks
    } catch {
      return null
    } finally {
      inflight.delete(url)
    }
  })()
  inflight.set(url, job)
  return job
}
