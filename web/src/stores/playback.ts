import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { useReplayStore } from '@/stores/replay'
import { applyInstanceEvent } from '@/stores/instance-reducer'
import type { AudioClip, InstanceDetail, Recording, RecordingStep } from '@/types/protocol'

/**
 * 回放：把一条录像「演」到画布上。
 *
 * 三个刻意的决定：
 *
 *  1. **不另起一套画布**。回放时把 `replay.instance` 换成「录像演出来的那一份」，
 *     画布、指标、挂单线、成交三角全都照常画 —— 因为它们本来就只认 InstanceDetail。
 *     同时 `playbackActive` 会关掉所有写操作，回放是纯只读的。
 *  2. **和实时共用 reducer**（stores/instance-reducer.ts）。录像里存的是当时的原始广播，
 *     按顺序重新应用一遍即可；机器人当时做的操作和人做的走的是同一段代码，所以
 *     回放里看到的「机器人动作」和演示时一模一样。
 *  3. **语音是挂在步上的**。每步的录音按 K 线时间归属，播放到那一步自动顺序播；
 *     空格键则是在当前这一步里一条条放 —— 逐条听、逐条核对，比自动播更好用。
 *
 * 每次改步都是「从 Base 重算一遍」而不是增量回退：一条录像最多几百步、几百个事件，
 * 重算的代价可以忽略，但换来的是「任意跳步都不会错位」。
 */

const BASE_STEP_MS = 900

export type PlaybackSpeed = 1 | 2 | 4

export const usePlaybackStore = defineStore('playback', () => {
  const replay = useReplayStore()

  const recording = ref<Recording | null>(null)
  /** 当前第几步（1-based，和录像里的 Step 一致） */
  const step = ref(1)
  const playing = ref(false)
  const speed = ref<PlaybackSpeed>(1)
  /** 走到有语音的步骤时，是否自动顺序播放 */
  const autoAudio = ref(true)
  const audioPlaying = ref(false)
  const audioLabel = ref('')
  /** 进入回放之前所在的实例：退出时回到它 */
  const restoreInstanceId = ref('')

  let timer = 0
  let audioEl: HTMLAudioElement | null = null
  let queue: AudioClip[] = []
  let queueDone: (() => void) | null = null
  let spaceIndex = 0

  const active = computed(() => recording.value !== null)
  const steps = computed<RecordingStep[]>(() => recording.value?.steps ?? [])
  const total = computed(() => steps.value.length)
  const currentStep = computed<RecordingStep | null>(() => steps.value[step.value - 1] ?? null)
  const atEnd = computed(() => step.value >= total.value)

  /** 这一步挂的录音（按录制顺序） */
  const clips = computed<AudioClip[]>(() => {
    const rec = recording.value
    const cur = currentStep.value
    if (!rec || !cur) return []
    return (rec.audios ?? []).filter((a) => a.time === cur.time || a.step === cur.step)
  })

  /** 整条录像有语音的步号集合（界面上给那几步加个小喇叭） */
  const audioSteps = computed<Set<number>>(() => {
    const set = new Set<number>()
    for (const a of recording.value?.audios ?? []) {
      set.add(a.step)
      // 时间才是真正的存储键：步号只是冗余，两边都对一下更稳
      const idx = steps.value.findIndex((s) => s.time === a.time)
      if (idx >= 0) set.add(idx + 1)
    }
    return set
  })

  function audioUrl(clip: AudioClip): string {
    const rec = recording.value
    if (!rec) return ''
    return `/api/recordings/${encodeURIComponent(rec.id)}/audio/${encodeURIComponent(clip.file)}`
  }

  // ---------------- 状态重建 ----------------

  /**
   * 从 Base 重算到第 upto 步。
   *
   * 深拷贝用 JSON 而不是 structuredClone：录像存在 Pinia 的 ref 里，已经是 Proxy，
   * structuredClone 对 Proxy 会抛 DataCloneError。这里的结构都是纯 JSON，够用。
   */
  function buildState(rec: Recording, upto: number): InstanceDetail {
    const base = JSON.parse(JSON.stringify(rec.base)) as Recording['base']
    const inst: InstanceDetail = {
      id: `rec:${rec.id}`,
      symbol: rec.symbol,
      folder: rec.folder,
      anchor: rec.anchor,
      cursor: base.cursor,
      tick: rec.tick,
      tickValue: rec.tickValue,
      bars: base.bars ?? [],
      hiddenBars: 0,
      hasMore: false,
      annotations: base.annotations ?? [],
      orders: base.orders ?? [],
      trades: base.trades ?? [],
      closedLots: base.closedLots ?? [],
      position: base.position,
      realizedPnl: base.realizedPnl,
      unrealizedPnl: base.unrealizedPnl,
      operator: '',
      members: [],
      bot: false,
      seq: 0,
      events: [],
      notes: [`回放：${rec.name}`],
      createdBy: rec.createdBy,
      createdAt: rec.createdAt,
      recording: null,
    }
    const limit = Math.min(Math.max(upto, 1), rec.steps.length)
    for (let i = 0; i < limit; i++) {
      for (const ev of rec.steps[i].events ?? []) {
        applyInstanceEvent(inst, ev.action, ev.payload)
      }
    }
    return inst
  }

  /** 跳到第 n 步（1-based）。暂停状态下手动跳步用这个。 */
  function goto(n: number): void {
    const rec = recording.value
    if (!rec) return
    const target = Math.min(Math.max(1, Math.round(n)), rec.steps.length)
    step.value = target
    spaceIndex = 0
    replay.instance = buildState(rec, target)
  }

  // ---------------- 打开 / 关闭 ----------------

  /** 打开一条录像开始回放。会记住当前实例，退出时自动回去。 */
  function open(rec: Recording | null): void {
    if (!rec || !rec.steps?.length) {
      replay.noteNotice('这条录像没有内容（可能只录了一个空盘面）')
      return
    }
    stopTimer()
    stopAudio()
    if (!replay.playbackActive) {
      restoreInstanceId.value = replay.instance?.id ?? ''
    }
    recording.value = rec
    step.value = 1
    playing.value = false
    replay.playbackActive = true
    replay.selectedAnnotation = null
    replay.tool = 'select'
    replay.log = []
    replay.you = null
    replay.instance = buildState(rec, 1)
  }

  /**
   * 退出回放。restore=true 时回到进入之前的实例（重新 join 拿一份最新快照）；
   * 从实例菜单里直接切到另一个实例时传 false —— 那边会自己发 join，
   * 不必先回到旧实例再跳。
   */
  function close(restore = true): void {
    stopTimer()
    stopAudio()
    playing.value = false
    recording.value = null
    step.value = 1
    const back = restoreInstanceId.value
    restoreInstanceId.value = ''
    replay.playbackActive = false
    replay.instance = null
    replay.you = null
    replay.log = []
    if (restore && back) replay.joinInstance(back)
  }

  // ---------------- 播放控制 ----------------

  function play(): void {
    if (!recording.value) return
    if (atEnd.value) goto(1) // 停在最后一步时点播放 = 重播
    playing.value = true
    const list = clips.value
    if (autoAudio.value && list.length > 0) {
      // 起点这一步就有语音：先放完，再从这一步往后走
      void playQueue(list, () => {
        if (playing.value) schedule()
      })
      return
    }
    schedule()
  }

  function pause(): void {
    playing.value = false
    stopTimer()
    stopAudio()
  }

  function toggle(): void {
    if (playing.value) pause()
    else play()
  }

  /** 单步：往后走一步并停住（不触发自动语音 —— 手动一步通常是停下来看）。 */
  function nextStep(): void {
    if (!recording.value) return
    pause()
    if (atEnd.value) return
    goto(step.value + 1)
  }

  function prevStep(): void {
    if (!recording.value) return
    pause()
    goto(step.value - 1)
  }

  /** 重播：回到第 1 步并立即开始连续播放。 */
  function restart(): void {
    if (!recording.value) return
    pause()
    goto(1)
    play()
  }

  function setSpeed(v: PlaybackSpeed): void {
    speed.value = v
    if (playing.value) {
      stopTimer()
      schedule()
    }
  }

  function schedule(): void {
    stopTimer()
    if (!playing.value) return
    timer = window.setTimeout(tick, BASE_STEP_MS / speed.value)
  }

  function tick(): void {
    if (!playing.value || !recording.value) return
    if (atEnd.value) {
      playing.value = false
      return
    }
    goto(step.value + 1)
    const list = clips.value
    if (autoAudio.value && list.length > 0) {
      // 有语音的一步：等它按顺序播完再继续，声音和画面对得上
      void playQueue(list, () => {
        if (playing.value) schedule()
      })
      return
    }
    schedule()
  }

  function stopTimer(): void {
    if (timer) {
      window.clearTimeout(timer)
      timer = 0
    }
  }

  // ---------------- 语音 ----------------

  function ensureAudio(): HTMLAudioElement {
    if (!audioEl) {
      audioEl = new Audio()
      audioEl.preload = 'auto'
    }
    return audioEl
  }

  /** 顺序播一组录音，全放完（或被停掉）后回调。 */
  async function playQueue(list: AudioClip[], done?: () => void): Promise<void> {
    stopAudio()
    queue = [...list]
    queueDone = done ?? null
    await playNextInQueue()
  }

  async function playNextInQueue(): Promise<void> {
    const clip = queue.shift()
    if (!clip) {
      audioPlaying.value = false
      audioLabel.value = ''
      const done = queueDone
      queueDone = null
      done?.()
      return
    }
    audioPlaying.value = true
    audioLabel.value = `${clip.time} 的录音（${(clip.durationMs / 1000).toFixed(1)} 秒）`
    const el = ensureAudio()
    el.src = audioUrl(clip)
    el.onended = () => {
      void playNextInQueue()
    }
    el.onerror = () => {
      // 音频取不到不该把整段回放卡住：跳过这一条，继续走
      replay.noteNotice(`录音 ${clip.file} 取不到，已跳过`)
      void playNextInQueue()
    }
    try {
      await el.play()
    } catch {
      // 浏览器拦截自动播放（没有用户手势）时，静默跳过当前这条，继续下一步
      void playNextInQueue()
    }
  }

  function stopAudio(): void {
    queue = []
    queueDone = null
    audioPlaying.value = false
    audioLabel.value = ''
    if (audioEl) {
      audioEl.onended = null
      audioEl.onerror = null
      audioEl.pause()
    }
  }

  /**
   * 空格：在当前这一步里**逐条**播放录音。
   *
   * 和自动顺序播放的区别是「谁决定节奏」：自动播是跟着步走，逐条播是人按一次听一条，
   * 适合对着某一步的讲解反复听。正在播的时候按空格 = 停下。
   *
   * 返回 true 表示这一下被回放消费掉了（调用方不要再去触发别的空格行为）。
   */
  function spacePlay(): boolean {
    if (!recording.value) return false
    const list = clips.value
    if (list.length === 0) return false
    if (audioPlaying.value) {
      stopAudio()
      return true
    }
    // 连续播放中按空格 = 切到「逐条」：先停下自动步进，不然讲解会被下一步盖掉
    if (playing.value) {
      playing.value = false
      stopTimer()
    }
    const clip = list[spaceIndex % list.length]
    spaceIndex = (spaceIndex + 1) % list.length
    void playQueue([clip])
    return true
  }

  return {
    recording,
    step,
    playing,
    speed,
    autoAudio,
    audioPlaying,
    audioLabel,
    active,
    steps,
    total,
    currentStep,
    atEnd,
    clips,
    audioSteps,
    audioUrl,
    open,
    close,
    goto,
    play,
    pause,
    toggle,
    nextStep,
    prevStep,
    restart,
    setSpeed,
    spacePlay,
  }
})
