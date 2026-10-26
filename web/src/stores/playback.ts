import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

import { useReplayStore } from '@/stores/replay'
import { applyInstanceEvent } from '@/stores/instance-reducer'
import { STEP_BASE, stepKindLabel } from '@/stores/step-kinds'
import { ACTION } from '@/types/protocol'
import type {
  AudioClip,
  InstanceDetail,
  Recording,
  RecordingExtendedMessage,
  RecordingStep,
  RecordingSummary,
  RecordingVoiceMessage,
} from '@/types/protocol'

/**
 * 回放：把一条录像「演」到画布上。
 *
 * 四个刻意的决定：
 *
 *  1. **不另起一套画布**。回放时把 `replay.instance` 换成「录像演出来的那一份」，
 *     画布、指标、挂单线、成交三角全都照常画 —— 因为它们本来就只认 InstanceDetail。
 *     同时 `playbackActive` 会关掉所有写操作，回放是纯只读的。
 *  2. **和实时共用 reducer**（stores/instance-reducer.ts）。录像里存的是当时的原始广播，
 *     按顺序重新应用一遍即可；机器人当时做的操作和人做的走的是同一段代码，所以
 *     回放里看到的「机器人动作」和演示时一模一样。
 *  3. **一步 = 一次操作**（v0.15.0）。所以「单步」就是「回到用户的下一次动手」：
 *     点【下一根】、画一个图形、挂一张单、录一段音，各是一步。界面上的步进条
 *     按类型上色，一根 K 线的边界有分隔 —— 一根 K 线上十几步也不会看糊。
 *  4. **语音是挂在步上的**。录音自己占一步，播到那一步就放那一段；走到别的步
 *     不会重复放同一根 K 线上一段已经听过的语音。
 *
 * 每次改步都是「从 Base 重算一遍」而不是增量回退：一条录像最多几百步、几百个事件，
 * 重算的代价可以忽略，但换来的是「任意跳步都不会错位」。
 *
 * v0.27.0 加的**回放延伸**：走到最后一步之后还能一根一根把后面的真实行情加载出来
 * （见 stores/replay.ts 的 extendRecording 和 internal/replay/extend.go）。
 * 三件事决定了它的形状：
 *
 *  1. **加载 = 记录里多一步**（kind = bar_extended），所以「跳到第 N 步」照样能重现，
 *     重新打开这条记录那几根也还在。它不是一次性的临时视图。
 *  2. **只在末尾**：中间插一根会让后面所有步的 K 线时间和游标错位，画在上面的
 *     图形、挂在上面的话全都会跑到别的柱子上。所以按钮只在 `atEnd` 时可用。
 *  3. **录音走同一条语音步**：在延伸出来的那根上录的话，和复盘期间录的一样
 *     变成一步（kind = recording_audio），于是「走到那一步就响」这条规则不用改。
 */

const BASE_STEP_MS = 900

/**
 * 各类操作在连续播放里停留多久（1x）。
 *
 * 为什么不是一个统一值：一步一根 K 线的时候，每步的信息量差不多；现在一步一个操作，
 * 「换了一根 K 线」要看清新 K 线的形状，「画了个图形」只要看清那条线出现 ——
 * 两者用同一个时长，要么 K 线还没看清就跳了，要么画图慢得让人以为卡住。
 */
const STEP_MS: Record<string, number> = {
  [STEP_BASE]: 1200,
  [ACTION.barAdvanced]: BASE_STEP_MS,
  [ACTION.annotationAdded]: 550,
  [ACTION.annotationMoved]: 450,
  [ACTION.annotationStyled]: 450,
  [ACTION.annotationDeleted]: 400,
  [ACTION.annotationCleared]: 600,
  [ACTION.orderPlaced]: 650,
  [ACTION.orderFilled]: 750,
  [ACTION.orderMoved]: 450,
  [ACTION.orderCancelled]: 450,
  // 一键退仓会同时动挂单和仓位，停久一点才看得清「全平了」
  [ACTION.flatAll]: 800,
}

export type PlaybackSpeed = 1 | 2 | 4

export const usePlaybackStore = defineStore('playback', () => {
  const replay = useReplayStore()

  const recording = ref<Recording | null>(null)
  /** 当前第几步（1-based，和录像里的 Step 一致） */
  const step = ref(1)
  const playing = ref(false)
  const speed = ref<PlaybackSpeed>(1)
  /** 走到录音步时，是否自动播放 */
  const autoAudio = ref(true)
  const audioPlaying = ref(false)
  const audioLabel = ref('')
  /** 正在响的那一条（列表里点的那条会高亮，自动播放时也跟上） */
  const currentClip = ref<AudioClip | null>(null)
  /** 进入回放之前所在的实例：退出时回到它 */
  const restoreInstanceId = ref('')
  /**
   * 正在等「加载下一根」的回复。
   *
   * 它只有一个用处：挡住连点。一次点击是一次**磁盘写**（记录里多一步），
   * 连点五下会变成五次往返，中间那几次的回复顺序还可能和点击顺序不一样。
   */
  const extending = ref(false)
  /**
   * 数据目录里还有没有更晚的 K 线。
   *
   * 默认 true，由每次加载的回复纠正：从没加载过的时候谁也说不准（记录里不含
   * 「之后还有没有行情」这个信息），所以第一次总是允许点 —— 真有数据就加载一根，
   * 真没有就由服务端回一句「已经到最后一根」，这里再置灰。
   */
  const extendHasMore = ref(true)

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

  /** 当前这一步的中文名（顶栏显示「第 7/N 步 · 录音」用） */
  const currentKindLabel = computed(() => {
    const cur = currentStep.value
    return cur ? stepKindLabel(cur.kind) : ''
  })

  /**
   * 当前是第几根 K 线（1 起）：数到当前步为止换过几次根。
   *
   * 延伸步也算 —— 不数它的话，走到第 9 步（延伸第 3 根）顶栏会写「第 7/6 根」，
   * 像数据错乱。
   */
  const barNo = computed(() => {
    let n = 1
    for (let i = 0; i < step.value && i < steps.value.length; i++) {
      const k = steps.value[i].kind
      if (k === ACTION.barAdvanced || k === ACTION.barExtended) n++
    }
    return n
  })

  /**
   * 这一步挂的录音 —— **按步号**匹配，不按 K 线时间。
   *
   * 这一条是从「一步一根 K 线」改过来时最容易错的地方：以前按 `a.time === cur.time`
   * 过滤，那时一根 K 线只有一步，所以看着没问题；现在一根 K 线上可能是「画图 →
   * 录音 → 画图」三步，按时间过滤会让**三步同时列出同一段语音**，回放时一句讲解
   * 被重复放三遍。存储键仍然是时间（跨文件稳定），但回放要按步对齐。
   */
  const clips = computed<AudioClip[]>(() => {
    const rec = recording.value
    const cur = currentStep.value
    if (!rec || !cur) return []
    return (rec.audios ?? []).filter((a) => a.step === cur.step)
  })

  /** 整条录像里「有语音的步号」集合（界面上给那几步加个小喇叭） */
  const audioSteps = computed<Set<number>>(() => {
    const set = new Set<number>()
    for (const a of recording.value?.audios ?? []) set.add(a.step)
    return set
  })

  // ---------------- 回放延伸（v0.27.0） ----------------

  /** 能不能再往后加载一根：在末尾、上一根还在路上时不能点、数据到头了也不能点。 */
  const canExtend = computed(
    () => recording.value !== null && atEnd.value && extendHasMore.value && !extending.value,
  )

  /** 这条记录末尾那根「延伸出来」的 K 线（没有 = 这一笔从来没延伸过）。 */
  const extensionTail = computed<RecordingStep | null>(() => {
    const list = steps.value
    for (let i = list.length - 1; i >= 0; i--) {
      if (list[i].kind === ACTION.barExtended) return list[i]
    }
    return null
  })

  /**
   * 现在正停在「延伸出来的最后一根」上 —— 录音只在这里可用。
   *
   * 判据是**时间**而不是步号：在那根上录过音之后，最后一步变成了语音步
   * （Time 还是同一根），可录音的位置并没有变。服务端也是按「时间」认的。
   */
  const onExtensionTail = computed(() => {
    const tail = extensionTail.value
    if (!tail || !atEnd.value) return false
    return currentStep.value?.time === tail.time
  })

  /** 已经挂在末尾这根上的语音（面板上显示「本根 x/9」和清空按钮用）。 */
  const extensionClips = computed<AudioClip[]>(() => {
    const tail = extensionTail.value
    if (!tail) return []
    return (recording.value?.audios ?? []).filter((a) => a.time === tail.time)
  })

  /** 每根 K 线的起点步号（步进条上的分隔线用） */
  const barStarts = computed<Set<number>>(() => {
    const set = new Set<number>()
    for (const s of steps.value) {
      if (s.kind === ACTION.barAdvanced) set.add(s.step)
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
      // 语音的空壳：回放里不放「盘面语音」（那是实时的东西），语音只从记录的
      // 语音步里来（见 pb.clips）。字段必须存在，界面才不用到处判空。
      voice: { barTime: '', clips: [], total: 0, max: 9 },
      voiceAll: [],
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
    // 每次打开都重新问一次「后面还有没有 K 线」：同一条记录可能刚刚被别的页面加载过
    extending.value = false
    extendHasMore.value = true
    replay.playbackActive = true
    // 回放的是一盘历史 K 线：手先放回指针，正在编辑的图形也一起放掉（否则手柄会挂在新盘面上）
    replay.tool = 'select'
    replay.selectAnnotation(null)
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
    extending.value = false
    extendHasMore.value = true
    const back = restoreInstanceId.value
    restoreInstanceId.value = ''
    replay.playbackActive = false
    replay.instance = null
    replay.you = null
    replay.log = []
    if (restore && back) replay.joinInstance(back)
  }

  // ---------------- 回放延伸 ----------------

  /**
   * 往后加载一根 K 线（在最后一步点【加载下一根】）。
   *
   * 为什么要求「在最后一步」：延伸步是**追加**到记录末尾的。如果人正停在中间，
   * 追加的那一步在步进条上是最后一格，画面不会动 —— 用户会觉得「点了没反应」。
   * 更要紧的是服务端的判据是「记录里最后一根 K 线之后」，人在中间时那根可能
   * 还没走到，加载出来的行情就成「未来的行情提前出现」了。所以先请人走到末尾。
   */
  function extendNext(): void {
    const rec = recording.value
    if (!rec || extending.value) return
    pause()
    if (!atEnd.value) {
      replay.noteNotice('先走到最后一步，再往后加载 K 线 —— 中间插一根会让后面的图形和语音错位')
      return
    }
    if (!extendHasMore.value) {
      replay.noteNotice('数据目录里已经到最后一根 K 线了')
      return
    }
    extending.value = true
    if (!replay.extendRecording(rec.id, 1)) {
      extending.value = false
      replay.noteNotice('连接断了，这一根没加载上')
    }
  }

  /**
   * 在末尾那根延伸出来的 K 线上录一段语音。
   *
   * 校验（每根 9 条 / 2MB / WAV）全在服务端 —— 和复盘录音是同一份实现，
   * 前端只负责把字节送上去，不做「我以为还能录」的判断（那会和服务端口径分叉）。
   */
  function addExtensionVoice(audioData: string, durationMs: number): boolean {
    const rec = recording.value
    if (!rec || !onExtensionTail.value) return false
    return replay.addRecordingVoice(rec.id, audioData, durationMs)
  }

  /** 清空末尾那根上的全部语音（和复盘里的【清空本根】同一个语义）。 */
  function clearExtensionVoice(): boolean {
    const rec = recording.value
    if (!rec || !onExtensionTail.value) return false
    return replay.clearRecordingVoice(rec.id)
  }

  /**
   * 把服务端回的摘要贴回手里的记录（步数 / 根数 / 结束时间 / 名字都变了）。
   *
   * 只挑这几个字段：`base` / `steps` / `audios` 是本地的正本，
   * 整份 Object.assign 会把它们换成摘要里没有的 undefined。
   */
  function patchSummary(rec: Recording, sum: RecordingSummary | undefined): void {
    if (!sum) return
    rec.stepCount = sum.stepCount
    rec.audioCount = sum.audioCount
    rec.eventCount = sum.eventCount
    rec.bars = sum.bars
    rec.endTime = sum.endTime
    rec.name = sum.name
    rec.size = sum.size
    rec.stats = sum.stats
  }

  /**
   * 步号重排（本地版）：和服务端 extend.go 的 renumberRecording 同一条规则。
   *
   * 为什么前端也要会：清空语音时服务端把步摘掉了，但**不回传整份 steps**
   * （几百步没必要重传）。规则只有「Step = 下标 + 1」一条，两边各写一遍不会分叉；
   * 而 AudioClip.Step 必须跟着改，否则「语音说自己在第 12 步、实际在第 9 步」。
   */
  function renumberLocal(rec: Recording): void {
    const stepOf = new Map<string, number>()
    rec.steps.forEach((st, i) => {
      st.step = i + 1
      if (st.audio) {
        st.audio.step = st.step
        stepOf.set(st.audio.id, st.step)
      }
    })
    for (const a of rec.audios) {
      const n = stepOf.get(a.id)
      if (n !== undefined) a.step = n
    }
  }

  /** 处理「又加载了几根」（replay 转过来的增量）。 */
  function applyExtended(msg: RecordingExtendedMessage): void {
    const rec = recording.value
    if (!rec) return
    extending.value = false
    rec.steps = [...rec.steps, ...(msg.steps ?? [])]
    patchSummary(rec, msg.recording)
    extendHasMore.value = msg.hasMore
    // 跳到新的最后一步：加载出来的那根立刻出现在画布上（否则「点了没反应」）
    goto(rec.steps.length)
    const last = msg.steps[msg.steps.length - 1]
    const tail = msg.hasMore ? '' : ' · 数据已到末尾'
    replay.noteNotice(`${msg.steps.length} 根已加载（到 ${last?.time ?? ''}）${tail}`)
  }

  /** 处理录音 / 清空的回复。 */
  function applyVoiceResult(msg: RecordingVoiceMessage): void {
    const rec = recording.value
    if (!rec) return
    if (msg.audio && msg.step) {
      rec.audios = [...rec.audios, msg.audio]
      rec.steps = [...rec.steps, msg.step]
    }
    if (msg.removed?.length) {
      const gone = new Set(msg.removed.map((c) => c.id))
      // 正在放的就是被删掉的那条：先停，否则「清空了还在响」
      if (currentClip.value && gone.has(currentClip.value.id)) stopAudio()
      rec.audios = rec.audios.filter((a) => !gone.has(a.id))
      rec.steps = rec.steps.filter((s) => !(s.audio && gone.has(s.audio.id)))
      renumberLocal(rec)
    }
    patchSummary(rec, msg.recording)
    if (msg.audio && msg.step) {
      // 语音自己占一步：跟着走到那一步，面板上就能看到「本步 1 条」并自动放出来
      goto(msg.step.step)
    } else {
      goto(Math.min(step.value, Math.max(1, rec.steps.length)))
    }
    if (msg.removed?.length) {
      replay.noteNotice(`已清除这根 K 线上的 ${msg.removed.length} 条语音`)
    } else if (msg.audio) {
      replay.noteNotice(
        `语音已存进这条交易记录（${msg.audio.time} · ${(msg.audio.durationMs / 1000).toFixed(1)} 秒）`,
      )
    }
  }

  /**
   * 收到延伸/录音的回复：只在「是同一条记录」时接上。
   *
   * 认 id 是必须的：同一个浏览器可能开着记录页和回放页，另一条记录的消息
   * 不该被当成「我这条又多了几根」。
   */
  watch(
    () => replay.playbackMsgSeq,
    () => {
      const msg = replay.playbackMsg
      if (!msg || !recording.value || msg.recordingId !== recording.value.id) return
      if (msg.type === 'recording_extended') applyExtended(msg)
      else applyVoiceResult(msg)
    },
  )

  /**
   * 失败也要收尾：把「加载中」放掉，并在「数据到头」时把按钮置灰。
   *
   * 没有这一步的话，第一次加载失败（最常见的就是整份数据已经到末尾）
   * 之后【加载下一根】会永远停在 loading 态 —— 按钮写着「加载中…」却什么都不发生。
   */
  watch(
    () => replay.playbackFailSeq,
    () => {
      const fail = replay.playbackFail
      if (!fail || !recording.value) return
      if (fail.request !== 'recording_extend') return
      extending.value = false
      if (fail.message.includes('最后一根')) extendHasMore.value = false
    },
  )

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
    timer = window.setTimeout(tick, stepDelay(currentStep.value) / speed.value)
  }

  /** 当前这一步该停多久（1x）。录音步不按这个走 —— 它由音频长度决定。 */
  function stepDelay(st: RecordingStep | null): number {
    if (!st) return BASE_STEP_MS
    return STEP_MS[st.kind] ?? 500
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
      currentClip.value = null
      const done = queueDone
      queueDone = null
      done?.()
      return
    }
    audioPlaying.value = true
    currentClip.value = clip
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
    currentClip.value = null
    if (audioEl) {
      audioEl.onended = null
      audioEl.onerror = null
      audioEl.pause()
    }
  }

  /**
   * 手动播一条（回放列表里点的那条）。
   *
   * 和 spacePlay 的区别：那个是「按顺序挨着放」，这个是「我就想听这一条」。
   * 再点同一条 = 停。
   */
  function playClip(clip: AudioClip): void {
    if (currentClip.value?.file === clip.file && audioPlaying.value) {
      stopAudio()
      return
    }
    void playQueue([clip])
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
    currentClip,
    active,
    steps,
    total,
    currentStep,
    currentKindLabel,
    barNo,
    atEnd,
    clips,
    audioSteps,
    barStarts,
    audioUrl,
    extending,
    extendHasMore,
    canExtend,
    extensionTail,
    onExtensionTail,
    extensionClips,
    extendNext,
    addExtensionVoice,
    clearExtensionVoice,
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
    playClip,
  }
})
