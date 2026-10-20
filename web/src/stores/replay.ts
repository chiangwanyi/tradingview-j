import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { ReplaySocket, defaultSocketUrl } from '@/net/socket'
import { applyInstanceEvent } from '@/stores/instance-reducer'
import { ACTION } from '@/types/protocol'
import type {
  AnnotationKind,
  AnnotationDeletedPayload,
  AnnotationStyle,
  BarAdvancedPayload,
  ClientMessage,
  ControlChangedPayload,
  InstanceDetail,
  InstanceStateMessage,
  InstanceSummary,
  OrderKind,
  Point,
  Recording,
  RecordingAudioClearedPayload,
  RecordingAudioPayload,
  RecordingState,
  RecordingStoppedPayload,
  RecordingSummary,
  ReplayEvent,
  ServerEvent,
  ServerMessage,
  Side,
  SymbolInfo,
  Template,
  You,
} from '@/types/protocol'

/** 画布上当前的工作模式。缩放/平移不属于工具，任何模式下都能用。 */
export type ChartTool = 'select' | 'modify' | 'trend' | 'ray' | 'rect' | 'arrow' | 'fib'

/** 五种绘图工具（不含指针/修改）。模板、样式、绘制规则都按它分类。 */
export type DrawTool = 'trend' | 'ray' | 'rect' | 'arrow' | 'fib'

export const DRAW_TOOLS: DrawTool[] = ['trend', 'ray', 'rect', 'arrow', 'fib']

/** 工具名。界面上的中文只在这里写一次。 */
export const TOOL_LABEL: Record<ChartTool, string> = {
  select: '指针',
  modify: '修改',
  trend: '趋势线',
  ray: '射线',
  rect: '矩形',
  arrow: '箭头线',
  fib: '斐波那契',
}

export function isDrawTool(tool: ChartTool): tool is DrawTool {
  return tool !== 'select' && tool !== 'modify'
}

/** 界面配色。只影响显示（画布配色 + 页面语义色），跟后端无关。 */
export type ThemeMode = 'light' | 'dark'

const MAX_LOG = 200
const THEME_KEY = 'tv-replay-theme'

function readTheme(): ThemeMode {
  try {
    const saved = localStorage.getItem(THEME_KEY)
    if (saved === 'light' || saved === 'dark') return saved
  } catch {
    // 某些隐私模式下 localStorage 会直接抛，忽略即可（大不了不记住）
  }
  return 'dark'
}

function applyTheme(mode: ThemeMode): void {
  // 挂在 <html> 上，style.css 里的 [data-theme='light'] 靠它切换 CSS 变量
  document.documentElement.dataset.theme = mode
}

export const useReplayStore = defineStore('replay', () => {
  const socket = new ReplaySocket(defaultSocketUrl())

  const status = ref<'connecting' | 'open' | 'closed'>('closed')
  /**
   * 回放模式。开启时 `instance` 被换成「录像演出来的那一份」，
   * 所有会改状态的动作都必须停手 —— 这是**只读**地看历史，不能往后端发任何修改。
   */
  const playbackActive = ref(false)
  const me = ref<{ clientId: string; name: string; version: string } | null>(null)
  const symbols = ref<SymbolInfo[]>([])
  const instances = ref<InstanceSummary[]>([])
  const instance = ref<InstanceDetail | null>(null)
  const you = ref<You | null>(null)
  const log = ref<ReplayEvent[]>([])
  const lastError = ref('')
  const errorKey = ref(0)
  /** 一句会自己消失的轻提示（录像已保存 / 录音已挂载）。 */
  const notice = ref('')
  const noticeKey = ref(0)
  let noticeTimer = 0

  // 界面状态（只影响显示，不属于业务状态，所以不发给后端）
  const tool = ref<ChartTool>('select')
  const selectedAnnotation = ref<string | null>(null)
  /**
   * 标注模板：后端存的全量列表 + 「每个工具上次用的是哪一条」。
   *
   * 模板本身在后端（改一次存一次盘，换台机器/重启后还在）；
   * 选中的是哪一条是纯界面状态，只记在内存里 —— 它不值得进协议。
   */
  const templates = ref<Template[]>([])
  const activeTemplate = ref<Record<string, string>>({})
  /** 已保存的录像列表（后端扫录像目录得到的，新→旧） */
  const recordings = ref<RecordingSummary[]>([])
  /** 录像目录的绝对路径，显示在回放 Tab 上，方便用户去文件夹里找 */
  const recordingDir = ref('')
  /** 刚取回来、等着交给播放器的那一条录像 */
  const loadedRecording = ref<Recording | null>(null)
  /** 每取回一条就加一：组件用它判断"这是一条新的"，避免同一条被重复打开 */
  const loadedSeq = ref(0)
  const theme = ref<ThemeMode>(readTheme())
  applyTheme(theme.value)
  /** 是否显示 EMA20（纯显示开关，跟后端无关） */
  const showEma = ref(true)
  /**
   * 下单数量（手）。**只能是正整数**：期货没有「0.4 手」这种报单，
   * 允许小数会让持仓、成交、平仓配对全都出现半手。
   *
   * 用「内部 ref + 可写 computed」而不是裸 ref：底部下单框、右键菜单、挂单动作
   * 三处共用这一个数，夹整数的逻辑只能有一份 —— 否则某条入口漏了，后端还是会拒，
   * 用户看到的就是「点了没反应」。
   */
  const orderQuantityRaw = ref(1)
  const orderQuantity = computed<number>({
    get: () => orderQuantityRaw.value,
    set: (v) => {
      const n = Math.round(Number(v))
      orderQuantityRaw.value = Number.isFinite(n) && n >= 1 ? Math.min(n, 10000) : 1
    },
  })

  const bars = computed(() => instance.value?.bars ?? [])
  const cursor = computed(() => instance.value?.cursor ?? 0)
  const currentBar = computed(() => bars.value[bars.value.length - 1] ?? null)
  const isOperator = computed(() => you.value?.operator === true)
  /** 当前实例的录制状态（没在录是 null） */
  const recording = computed<RecordingState | null>(() => instance.value?.recording ?? null)
  const isRecording = computed(() => recording.value?.active === true)
  const botRunning = computed(() => instance.value?.bot === true)
  /** 能操作 = 拿到操作权，且不是机器人在跑（机器人跑着的时候真人只能看，这就是演示模式） */
  const canOperate = computed(() => isOperator.value && !botRunning.value && !playbackActive.value)
  const operatorLabel = computed(() => {
    const inst = instance.value
    if (!inst) return ''
    if (inst.bot) return '机器人'
    return inst.operator || ''
  })
  const openOrders = computed(() => (instance.value?.orders ?? []).filter((o) => o.status === 'open'))
  /** 当前标的的最小跳动；没进实例时是 0（画布按两位小数兜底） */
  const tick = computed(() => instance.value?.tick ?? 0)
  /** 价格显示小数位：由最小跳动决定，至少两位 */
  const priceDecimals = computed(() => {
    const t = tick.value
    if (!(t > 0)) return 2
    return Math.min(6, Math.max(2, Math.ceil(-Math.log10(t) - 1e-9)))
  })

  function noteError(message: string): void {
    lastError.value = message
    errorKey.value += 1
  }

  function clearError(): void {
    lastError.value = ''
  }

  /** 一条会自己消失的提示（6 秒）。比 error 轻：保存成功不该弹一个要人点掉的框。 */
  function noteNotice(text: string): void {
    notice.value = text
    noticeKey.value += 1
    if (noticeTimer) window.clearTimeout(noticeTimer)
    noticeTimer = window.setTimeout(() => {
      notice.value = ''
    }, 6000)
  }

  function clearNotice(): void {
    notice.value = ''
    if (noticeTimer) window.clearTimeout(noticeTimer)
  }

  function send(msg: ClientMessage): boolean {
    // 连不上就静默失败：顶栏的「已断开（正在自动重连）」已经把状态说清楚了，
    // 再弹一个提示框纯属噪音（而且崩的是 WS，不是用户点错了什么）。
    return socket.send(msg)
  }

  function handleMessage(msg: ServerMessage): void {
    switch (msg.type) {
      case 'welcome':
        me.value = { clientId: msg.clientId, name: msg.name, version: msg.version }
        break
      case 'symbols':
        symbols.value = msg.symbols
        break
      case 'instances':
        instances.value = msg.instances
        break
      case 'instance_state':
        applyState(msg)
        break
      case 'event':
        applyEvent(msg)
        break
      case 'error':
        noteError(msg.message)
        // 重连后原来的实例可能已经不在了：别让界面停在假状态上
        if (msg.request === 'join_instance') {
          instance.value = null
          you.value = null
          log.value = []
        }
        break
      case 'left':
        instance.value = null
        you.value = null
        log.value = []
        break
      case 'deleted':
        // 删除是**用户自己点的**（别人的实例删不掉，见后端 handleDelete），
        // 再弹一句「这个实例已经被删除了」等于把他刚做的事复述一遍，还挂在界面上
        // 一直不消失（错误框要手动关、录制浮层会把它当成提示吃进去）。
        // 所以这里只安静地退回大厅；`instanceId` 对不上就什么都不做 ——
        // 删的要是别的实例，当前这块盘面不该被清掉。
        if (instance.value?.id === msg.instanceId) {
          instance.value = null
          you.value = null
          log.value = []
        }
        break
      case 'pong':
        break
      case 'recordings':
        recordings.value = msg.recordings ?? []
        recordingDir.value = msg.dir ?? ''
        break
      case 'recording':
        // 一条录像的完整内容到了：交给播放器（组件的 watch 会接住）
        loadedRecording.value = msg.recording ?? null
        loadedSeq.value += 1
        break
      case 'templates':
        templates.value = msg.templates ?? []
        if (msg.lastSavedId) {
          // 新建的模板立刻用上：id 是后端分配的，只有这条消息知道它
          const saved = templates.value.find((t) => t.id === msg.lastSavedId)
          if (saved) activeTemplate.value = { ...activeTemplate.value, [saved.kind]: saved.id }
        }
        break
      default:
        break
    }
  }

  function applyState(msg: InstanceStateMessage): void {
    const inst = msg.instance
    // 换了一块盘面（新建 / 进入另一个实例）：上一条错误属于上一条实例，跟着它作废。
    // 不这么做的话，删掉旧实例时留下的那句话会一直贴在右下角、还会被录制浮层
    // 抄进「提示」里 —— 新建的实例明明好好的，界面却在喊它已经被删了。
    if (instance.value?.id !== inst.id) clearError()
    // 快照里的 bars 只含已揭示的部分（后端根本不会发未来的 K 线）
    instance.value = { ...inst, bars: inst.bars ?? [], recording: inst.recording ?? null }
    you.value = msg.you
    log.value = [...(inst.events ?? [])].slice(-MAX_LOG)
    selectedAnnotation.value = null
  }

  function applyEvent(msg: ServerEvent): void {
    const inst = instance.value
    if (!inst || msg.instanceId !== inst.id) return // 只处理当前实例的广播

    log.value.push({
      seq: msg.seq,
      at: msg.at,
      by: msg.by,
      action: msg.action,
      payload: msg.payload,
    })
    if (log.value.length > MAX_LOG) log.value = log.value.slice(-MAX_LOG)

    // 录制相关的四条不是盘面变化，单独处理（放进 reducer 会让"回放"里也出现录制动作）
    if (
      msg.action === ACTION.recordingStarted ||
      msg.action === ACTION.recordingStopped ||
      msg.action === ACTION.recordingAudio ||
      msg.action === ACTION.recordingAudioCleared
    ) {
      applyRecordingEvent(inst, msg)
      return
    }

    // 盘面变化：和回放共用同一份更新逻辑（stores/instance-reducer.ts）
    applyInstanceEvent(inst, msg.action, msg.payload)

    // 录制中的「第 N 步」只能在这里往上加：服务端只在 instance_state 全量快照里带
    // 录制状态，而步进走的是 bar_advanced 这条事件流，不会重发快照。
    // 不加这一句，HUD 会一直停在「第 1 步」，而录像其实已经录了十几步（实测过）。
    // 只在**实时**里加：回放的 reducer 是同一份，但那时 recording 是 null（见 playback.ts）。
    if (inst.recording?.active && msg.action === ACTION.barAdvanced) {
      const p = msg.payload as BarAdvancedPayload
      inst.recording = {
        ...inst.recording,
        step: inst.recording.step + 1,
        current: p.bar?.time ?? inst.recording.current,
        // 新的一步还没有录音：服务端的 stepAudios 要等下一次快照才刷新，
        // 这里跟着清掉，否则 HUD 会拿上一步的清单当这一步的。
        stepAudios: 0,
        clips: [],
      }
    }

    // 下面是界面状态，不属于盘面
    if (msg.action === ACTION.annotationDeleted) {
      const p = msg.payload as AnnotationDeletedPayload
      if (selectedAnnotation.value === p.annotationId) selectedAnnotation.value = null
    } else if (msg.action === ACTION.annotationCleared) {
      selectedAnnotation.value = null
    } else if (msg.action === ACTION.controlChanged) {
      const p = msg.payload as ControlChangedPayload
      if (you.value) {
        you.value = { ...you.value, operator: p.operator !== '' && p.operator === me.value?.name }
      }
    }
  }

  /** 录制状态的三条广播：更新实例上的录制标记 + 给一句提示。 */
  function applyRecordingEvent(inst: InstanceDetail, msg: ServerEvent): void {
    switch (msg.action) {
      case ACTION.recordingStarted: {
        inst.recording = msg.payload as RecordingState
        noteNotice('开始录制：当前盘面记为第 1 步，之后每次【下一根】加一步')
        break
      }
      case ACTION.recordingAudio: {
        const p = msg.payload as RecordingAudioPayload
        inst.recording = p.state
        noteNotice(
          `录音 ${p.state.audios}/${p.state.maxAudios} 已挂到 ${p.audio.time}（第 ${p.audio.step} 步 · ${(p.audio.durationMs / 1000).toFixed(1)} 秒）`,
        )
        break
      }
      case ACTION.recordingAudioCleared: {
        const p = msg.payload as RecordingAudioClearedPayload
        inst.recording = p.state
        noteNotice(`已清除这一步的 ${p.removed.length} 条录音`)
        break
      }
      case ACTION.recordingStopped: {
        const p = msg.payload as RecordingStoppedPayload
        inst.recording = null
        if (p.error) {
          noteError(`录制已结束，但录像没有保存：${p.error}`)
        } else {
          noteNotice(
            `录像已保存：${p.recording.name}（${p.recording.stepCount} 步 / ${p.recording.audioCount} 条录音）→ ${p.dir ?? ''}`,
          )
        }
        // 录完就该在【回放】里看见它，不用用户手动刷新
        listRecordings()
        break
      }
    }
  }

  /** 断线重连后重新对齐：实例和列表都可能已经变了。 */
  function resync(): void {
    send({ type: 'list_symbols' })
    send({ type: 'list_instances' })
    send({ type: 'list_templates' })
    send({ type: 'list_recordings' })
    // 回放时 instance 是「演出来的那一份」，id 是 rec:xxx，后端根本没有这个实例：
    // 重连后去 join 它只会收到一条错误，还会把回放画面清空。等退出回放再重新 join。
    const id = instance.value?.id
    if (id && !playbackActive.value) send({ type: 'join_instance', instanceId: id })
  }

  socket.onMessage(handleMessage)
  socket.onStatus((s) => {
    status.value = s
    if (s === 'open') resync()
  })

  // ---------------- 主动作 ----------------

  function connect(): void {
    socket.connect()
  }

  function setTheme(mode: ThemeMode): void {
    theme.value = mode
    applyTheme(mode)
    try {
      localStorage.setItem(THEME_KEY, mode)
    } catch {
      // 记不住就算了，不影响本次使用
    }
  }

  function toggleTheme(): void {
    setTheme(theme.value === 'dark' ? 'light' : 'dark')
  }

  function refresh(): void {
    send({ type: 'list_symbols' })
    send({ type: 'list_instances' })
  }

  function createInstance(symbol: string, time: string): void {
    send({ type: 'create_instance', symbol, time, bars: 400, forwardDays: 5 })
  }

  function joinInstance(id: string): void {
    send({ type: 'join_instance', instanceId: id })
  }

  function leaveInstance(): void {
    if (!instance.value) return
    send({ type: 'leave_instance', instanceId: instance.value.id })
  }

  function deleteInstance(id: string): void {
    send({ type: 'delete_instance', instanceId: id })
  }

  function step(): void {
    if (!instance.value) return
    send({ type: 'step', instanceId: instance.value.id })
  }

  function takeControl(): void {
    if (!instance.value) return
    send({ type: 'take_control', instanceId: instance.value.id })
  }

  function releaseControl(): void {
    if (!instance.value) return
    send({ type: 'release_control', instanceId: instance.value.id })
  }

  // ---------------- 标注模板 ----------------

  function templatesFor(kind: DrawTool): Template[] {
    return templates.value.filter((t) => t.kind === kind)
  }

  /**
   * 当前该用哪条模板：上次选的那条（还在的话），否则第一条。
   * 返回 null 只可能发生在「后端一条模板都没有」时 —— 那种情况下画布会用出厂样式兜底。
   */
  function templateFor(kind: DrawTool): Template | null {
    const list = templatesFor(kind)
    const id = activeTemplate.value[kind]
    return list.find((t) => t.id === id) ?? list[0] ?? null
  }

  function selectTemplate(kind: DrawTool, id: string): void {
    activeTemplate.value = { ...activeTemplate.value, [kind]: id }
  }

  /** 新建（不带 id）或就地修改（带 id）。后端存完会广播整份列表回来。 */
  function saveTemplate(input: { id?: string; kind: DrawTool; name: string; style: AnnotationStyle }): void {
    send({ type: 'save_template', template: input })
  }

  function deleteTemplate(id: string): void {
    send({ type: 'delete_template', templateId: id })
  }

  // ---------------- 标注 ----------------

  function addAnnotation(input: {
    kind: AnnotationKind
    start: Point
    end: Point
    style: AnnotationStyle
    snap: boolean
  }): void {
    if (!instance.value) return
    send({
      type: 'annotation_add',
      instanceId: instance.value.id,
      kind: input.kind,
      start: input.start,
      end: input.end,
      style: input.style,
      snap: input.snap,
    })
  }

  /**
   * 移动标注：**整体移动和只动一个端点走同一条消息** —— 两个端点各自的新位置都在这里，
   * 只动一个时另一个原样传回。后端校验完广播回来，界面上的图形永远来自广播。
   */
  function moveAnnotation(id: string, start: Point, end: Point, snap: boolean): void {
    if (!instance.value) return
    send({
      type: 'annotation_move',
      instanceId: instance.value.id,
      annotationId: id,
      start,
      end,
      snap,
    })
  }

  function deleteAnnotation(id: string): void {
    if (!instance.value) return
    send({ type: 'annotation_delete', instanceId: instance.value.id, annotationId: id })
  }

  /**
   * 移除当前画布上的**全部**绘图（右键菜单）。
   *
   * 走一条专门的 `annotation_clear`，而不是循环调 deleteAnnotation：那样会连发 N 条消息、
   * 别人的画布一条条消失，操作记录里也多出 N 行。一条消息 = 一次状态变化 = 一条广播。
   *
   * 没有本地「先清空界面」的乐观更新：清空是服务端动作，广播回来才算数 ——
   * 被拒（比如没有操作权）时界面上什么都不该变。
   */
  function clearAnnotations(): void {
    if (!instance.value) return
    send({ type: 'annotation_clear', instanceId: instance.value.id })
  }

  function placeOrder(side: Side, kind: OrderKind, price: number, quantity: number): void {
    if (!instance.value) return
    // 后端也会校验整数，这里再夹一次是为了让「右键菜单里的 N 手」和真正发出去的一致
    const lots = Math.max(1, Math.round(quantity))
    send({ type: 'order_place', instanceId: instance.value.id, side, kind, price, quantity: lots })
  }

  /**
   * 市价单：不带价格，后端按当前收盘价立即成交。
   * 它和 placeOrder 分开是因为协议上「不需要 price」本身就是语义的一部分 ——
   * 复用 order_place 再传一个假价格，会让日志里出现一个不存在的挂单价。
   */
  function marketOrder(side: Side, quantity: number): void {
    if (!instance.value) return
    const lots = Math.max(1, Math.round(quantity))
    send({ type: 'order_market', instanceId: instance.value.id, side, quantity: lots })
  }

  function cancelOrder(orderId: string): void {
    if (!instance.value) return
    send({ type: 'order_cancel', instanceId: instance.value.id, orderId })
  }

  /**
   * 改挂单价（画布上拖动挂单线）。
   *
   * 只发改价：单型是下单那一刻按方向定的，拖过现价就让后端自动换单型，
   * 会让订单在用户手里悄悄变性质 —— 他以为自己只是挪了条线。
   */
  function moveOrder(orderId: string, price: number): void {
    if (!instance.value) return
    send({ type: 'order_modify', instanceId: instance.value.id, orderId, price })
  }

  // ---------------- 录制与录像 ----------------

  /**
   * 开始录制。【第 1 步 = 点下去时的那一屏】是后端定的：
   * 它把当时的 K 线、标注、挂单、成交整份存成 Base，不依赖前端算步数。
   */
  function startRecording(): void {
    if (!instance.value || !canOperate.value) return
    send({ type: 'record_start', instanceId: instance.value.id })
  }

  /** 结束录制。后端收尾、落盘、广播，前端只等消息。 */
  function stopRecording(): void {
    if (!instance.value) return
    send({ type: 'record_stop', instanceId: instance.value.id })
  }

  function toggleRecording(): void {
    if (isRecording.value) stopRecording()
    else startRecording()
  }

  /**
   * 送一段录音上去（WAV 的 base64）。挂载点是**服务端当前那根 K 线**，
   * 前端不传时间 —— 传了就有两种"当前"，一旦不一致录音会挂错柱子。
   */
  function sendRecordingAudio(audioData: string, durationMs: number): boolean {
    if (!instance.value || !isRecording.value) return false
    return send({ type: 'record_audio', instanceId: instance.value.id, audioData, durationMs })
  }

  function listRecordings(): void {
    send({ type: 'list_recordings' })
  }

  /** 清空**当前这一步**的录音（录坏了重来，不用把整条录像作废）。 */
  function deleteStepAudio(): boolean {
    if (!instance.value || !isRecording.value) return false
    return send({ type: 'record_audio_delete', instanceId: instance.value.id })
  }

  /** 请求一条录像的完整内容；回来之后由回放面板打开播放器。 */
  function requestRecording(id: string): void {
    send({ type: 'get_recording', recordingId: id })
  }

  function deleteRecording(id: string): void {
    send({ type: 'delete_recording', recordingId: id })
  }

  function clearLoadedRecording(): void {
    loadedRecording.value = null
  }

  function startBot(intervalMs = 1200): void {
    if (!instance.value) return
    send({ type: 'bot_start', instanceId: instance.value.id, intervalMs })
  }

  function stopBot(): void {
    if (!instance.value) return
    send({ type: 'bot_stop', instanceId: instance.value.id })
  }

  return {
    // 状态
    status,
    me,
    playbackActive,
    notice,
    noticeKey,
    recordings,
    recordingDir,
    loadedRecording,
    loadedSeq,
    recording,
    isRecording,
    symbols,
    instances,
    instance,
    you,
    log,
    lastError,
    errorKey,
    tool,
    selectedAnnotation,
    templates,
    activeTemplate,
    theme,
    showEma,
    orderQuantity,
    tick,
    priceDecimals,
    // 派生
    bars,
    cursor,
    currentBar,
    isOperator,
    botRunning,
    canOperate,
    operatorLabel,
    openOrders,
    // 动作
    connect,
    setTheme,
    toggleTheme,
    refresh,
    createInstance,
    joinInstance,
    leaveInstance,
    deleteInstance,
    step,
    takeControl,
    releaseControl,
    templatesFor,
    templateFor,
    selectTemplate,
    saveTemplate,
    deleteTemplate,
    addAnnotation,
    moveAnnotation,
    deleteAnnotation,
    clearAnnotations,
    placeOrder,
    marketOrder,
    moveOrder,
    cancelOrder,
    startBot,
    stopBot,
    clearError,
    noteNotice,
    clearNotice,
    startRecording,
    stopRecording,
    toggleRecording,
    sendRecordingAudio,
    deleteStepAudio,
    listRecordings,
    requestRecording,
    deleteRecording,
    clearLoadedRecording,
  }
})
