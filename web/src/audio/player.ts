import { computed, ref } from 'vue'

import type { AudioClip } from '@/types/protocol'

/**
 * 语音试听的**唯一**播放器（v0.17.0）。
 *
 * 为什么必须收成一个：语音现在有三处能点 —— 语音浮窗里的「本根清单」、
 * 浮窗里的「其他 K 线还有 N 条」、以及**横轴下方的录音条**。三处各建一个
 * `HTMLAudioElement` 会出现两个问题：点录音条的时候浮窗里那条还在响（两个声音叠在
 * 一起，讲解根本听不清），以及「哪条在响」会有三份互不相认的说法（三处高亮不同步）。
 * 这里只留一份状态：谁在放、放到哪儿、出了什么错，谁要用谁读。
 *
 * 队列只做**顺序播放**，不做循环、不做并发：讲解是一段接一段的，
 * 「这一根上 3 条」和「整块盘面 12 条」用同一条队列，只是传进来的数组长度不同。
 *
 * 音频地址由**调用方**拼：盘面上的语音走 `/api/instances/<id>/voice/<file>`，
 * 录像里的走 `/api/recordings/<id>/audio/<file>`，两者不复用（见 README「语音讲解」）。
 * 播放器不认路由，只认「给我一个 url」。
 */

/** 正在响的那条的**文件名**（'' = 没在放）。文件名在实例和录像里都唯一。 */
export const voiceCurrentFile = ref('')
/** 当前这条放到百分之几（0..1）—— 录音条底部那条进度就是它 */
export const voiceProgress = ref(0)
/** 有声音在放（含队列里还有没放的） */
export const voicePlaying = computed(() => voiceCurrentFile.value !== '')
/** 上一次播放失败的说明；换一条播放时自动清空 */
export const voiceError = ref('')

let el: HTMLAudioElement | null = null
let queue: AudioClip[] = []
let urlOf: (clip: AudioClip) => string = () => ''
let raf = 0
/**
 * 每次换曲自增。异步的 play() 被拒时用它判断「这条还是不是当前那条」——
 * 连点两条时，前一条的失败回调不该把后一条的错误提示贴上去。
 */
let token = 0

function ensure(): HTMLAudioElement {
  if (el) return el
  const a = new Audio()
  a.preload = 'auto'
  // 一条放完接着放下一条；队列空了就收尾（保持 error 不变，让界面把话说完）
  a.addEventListener('ended', () => {
    if (!voiceCurrentFile.value) return
    advance()
  })
  a.addEventListener('error', () => {
    if (!voiceCurrentFile.value) return
    voiceError.value = '这条语音取不到（可能刚被清空）'
    advance()
  })
  el = a
  return a
}

// ---------------- 进度 ----------------

function startProgress(): void {
  stopProgress()
  const tick = (): void => {
    const a = el
    if (!a || !voiceCurrentFile.value) return
    const d = a.duration
    // duration 在元数据回来之前是 NaN：那时进度就是 0，别把 NaN 写进样式里
    voiceProgress.value = Number.isFinite(d) && d > 0 ? Math.min(1, Math.max(0, a.currentTime / d)) : 0
    raf = requestAnimationFrame(tick)
  }
  raf = requestAnimationFrame(tick)
}

function stopProgress(): void {
  if (raf) cancelAnimationFrame(raf)
  raf = 0
}

// ---------------- 队列 ----------------

function reset(): void {
  token += 1
  queue = []
  voiceCurrentFile.value = ''
  voiceProgress.value = 0
  stopProgress()
  if (el) {
    el.pause()
    // 去掉 src 才会真的松开这个文件（否则文件删了浏览器还占着句柄）
    el.removeAttribute('src')
  }
}

/** 队列里下一条；空了就收尾（错误提示保留，界面还要显示它）。 */
function advance(): void {
  const clip = queue.shift()
  if (!clip) {
    reset()
    return
  }
  const url = urlOf(clip)
  if (!url) {
    voiceError.value = '这条语音没有可用的地址'
    advance()
    return
  }
  const a = ensure()
  const my = ++token
  voiceError.value = ''
  voiceCurrentFile.value = clip.file
  voiceProgress.value = 0
  a.src = url
  startProgress()
  const started = a.play()
  // 浏览器可能直接拒（没有用户手势、或者文件已经不在了）
  if (started) {
    void started.catch((e: unknown) => {
      if (my !== token) return
      const name = e instanceof Error ? e.name : ''
      voiceError.value =
        name === 'NotAllowedError'
          ? '浏览器拦了自动播放：先点一下页面空白处，再点播放'
          : `这条语音播不了（${name || String(e)}）`
      // 不接着放下一条：用户点的那条没出声，再放别的只会更莫名其妙
      reset()
    })
  }
}

// ---------------- 对外 ----------------

/**
 * 顺序播一组语音。**会先停掉正在放的那条** —— 界面上永远只有一个声音。
 *
 * 传空数组等于 stop：调用方不用先判断。
 */
export function voicePlayList(list: AudioClip[], resolver: (clip: AudioClip) => string): void {
  reset()
  if (list.length === 0) return
  urlOf = resolver
  queue = [...list]
  advance()
}

/** 点某一条：正在放同一条就停，否则只放这一条（不是整组）。 */
export function voiceToggle(clip: AudioClip, resolver: (clip: AudioClip) => string): void {
  if (voiceCurrentFile.value === clip.file) {
    reset()
    return
  }
  voicePlayList([clip], resolver)
}

/** 停下并清空队列。不清 error：错误提示该由看到的人决定什么时候擦掉。 */
export function voiceStop(): void {
  reset()
}
