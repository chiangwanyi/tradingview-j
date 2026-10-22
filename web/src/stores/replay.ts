import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { DEFAULT_INDICATOR, sanitizeIndicator, type IndicatorSettings } from '@/components/chart/indicators'
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
  StrategyInfo,
  SymbolInfo,
  Template,
  You,
} from '@/types/protocol'

/**
 * 画布上当前手里的工具。缩放/平移不属于工具，任何工具下都能用。
 *
 * 只有两类状态：**指针**（`select`，默认）和**五种绘图工具**。
 * 编辑图形不再是一种「工具」—— 它是指针模式下「选中了某个图形」的派生态，
 * 由 `selectedAnnotation` 一个值决定（见 `selectAnnotation` / `editingId`）。
 * 以前那种「指针 / 修改」两个模式按钮 + 选中项的写法有两份状态要互相对齐，
 * 关掉修改模式时漏清一次选中，就会留下一个看不见却还能被 Del 删掉的图形。
 */
export type ChartTool = 'select' | DrawTool

/** 五种绘图工具（不含指针）。模板、样式、绘制规则都按它分类。 */
export type DrawTool = 'trend' | 'ray' | 'rect' | 'arrow' | 'fib'

export const DRAW_TOOLS: DrawTool[] = ['trend', 'ray', 'rect', 'arrow', 'fib']

/** 工具名。界面上的中文只在这里写一次。 */
export const TOOL_LABEL: Record<ChartTool, string> = {
  select: '指针',
  trend: '趋势线',
  ray: '射线',
  rect: '矩形',
  arrow: '箭头线',
  fib: '斐波那契',
}

export function isDrawTool(tool: ChartTool): tool is DrawTool {
  return tool !== 'select'
}

/** 界面配色。只影响显示（画布配色 + 页面语义色），跟后端无关。 */
export type ThemeMode = 'light' | 'dark'

const MAX_LOG = 200
const THEME_KEY = 'tv-replay-theme'
/** 指标的开关与参数记在本机：它是显示偏好，换实例、重启程序都不该被重置 */
const INDICATOR_KEY = 'tv-replay-doji-zigzag'

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

/**
 * 读回上次的指标设置。
 *
 * 存的是整份 JSON：以后加参数（比如给蓝点换颜色）不用再动存档格式，
 * 读回来先过一遍 `sanitizeIndicator` —— 手改过 localStorage、或者旧版本留了半份
 * 字段，都不该让指标在一屏上直接消失。
 */
function readIndicator(): IndicatorSettings {
  try {
    const raw = localStorage.getItem(INDICATOR_KEY)
    if (raw) {
      const saved = JSON.parse(raw) as Partial<IndicatorSettings>
      return sanitizeIndicator({ ...DEFAULT_INDICATOR, ...saved })
    }
  } catch {
    // 隐私模式 / 存的是坏 JSON：回到出厂值，不影响本次使用
  }
  return { ...DEFAULT_INDICATOR }
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
  /**
   * 正在编辑的图形 id。`null` = 指针的常规状态（只缩放平移）。
   *
   * **这是「修改态」的唯一真相**：画布上点中哪个图形就只有哪个可编辑，
   * 点空白/点 K 线就把它清成 null —— 「退出修改」不需要另设一个模式去切，
   * 也就不存在「模式退了、选中还留着」这种不一致。
   */
  const selectedAnnotation = ref<string | null>(null)
  /** 是不是指针模式（= 手里没拿笔）。编辑态只在它下面成立。 */
  const pointerMode = computed(() => tool.value === 'select')
  /**
   * 真正生效的编辑对象：绘图工具在手时一律为空。
   *
   * 不只是保险 —— 画布模板里的 pointer-events、端点手柄、Del 删除全都读这一个值，
   * 所以「拿起笔」和「退出编辑」是同一个动作，不会出现笔在手却还能拖图形的中间态。
   */
  const editingId = computed(() => (tool.value === 'select' ? selectedAnnotation.value : null))
  const editing = computed(() => editingId.value !== null)

  /**
   * 进入 / 切换 / 退出编辑。
   *
   * 界面上的三条规则（点图形进入、点另一个切过去、点空白退出）全部收敛到这一个入口，
   * 所以「切换过程」只是把 id 换个值：被点中的那个立刻显示手柄，上一个同时失去手柄，
   * 中间没有第二个状态需要清。传 `null` 永远有效（清不了选中才是残留的来源）。
   */
  function selectAnnotation(id: string | null): void {
    // 手上还拿着绘图工具时点哪儿都是「落笔」，不接选中 —— 界面那边图形本身也不接指针事件，
    // 这里再挡一次，免得将来多出别的调用方绕过画布直接改选中。
    if (id !== null && tool.value !== 'select') return
    selectedAnnotation.value = id
  }

  /**
   * 标注模板：后端存的全量列表 + 「每个工具上次用的是哪一条」。
   *
   * 模板本身在后端（改一次存一次盘，换台机器/重启后还在）；
   * 选中的是哪一条是纯界面状态，只记在内存里 —— 它不值得进协议。
   */
  const templates = ref<Template[]>([])
  const activeTemplate = ref<Record<string, string>>({})
  /**
   * 可加载的策略清单（后端注册表里有什么）。
   *
   * 连上就取一次：策略是编译期注册的，运行期不会变，没必要轮询或者做失效。
   */
  const strategies = ref<StrategyInfo[]>([])
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
   * Doji X + Blue ZigZag 的开关与两个参数。
   *
   * 和 EMA20 一样是纯显示状态：不发消息、不进存档、不影响任何人的盘面。
   * 它和「Zoom / 平移」属于同一类 —— 各人看各人的，别人不该看到我的指标被关掉。
   */
  const indicator = ref<IndicatorSettings>(readIndicator())
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
  /**
   * 机器人正在跑的策略叫什么名字。
   *
   * 实例上只存 id（协议里也只传 id），名字从策略清单里查 —— 清单可能还没到，
   * 那时退回显示 id：显示 id 比显示空白强，至少能看出「跑的不是随机演示」。
   */
  const botStrategyLabel = computed(() => {
    const id = instance.value?.botStrategy
    if (!id) return ''
    return strategies.value.find((s) => s.id === id)?.name ?? id
  })
  /** 能操作 = 拿到操作权，且不是机器人在跑（机器人跑着的时候真人只能看，这就是演示模式） */
  const canOperate = computed(() => isOperator.value && !botRunning.value && !playbackActive.value)
  const operatorLabel = computed(() => {
    const inst = instance.value
    if (!inst) return ''
    if (inst.bot) {
      const name = botStrategyLabel.value
      return name ? `机器人 · ${name}` : '机器人'
    }
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
        // 连上就顺手把策略清单拉下来：启动面板要用，晚一天取就是白等一次往返
        listStrategies()
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
      case 'strategies':
        strategies.value = msg.strategies ?? []
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
    // 快照 = 换了一块盘面：上一条实例的选中在这条里没有对应图形，必须一起丢掉
    selectAnnotation(null)
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

    // 录制中的「第几步」由**服务端下发**（msg.recStep，v0.15.0）。
    //
    // 以前这里是「收到 bar_advanced 就把步数 +1」，因为那时一步 = 一根 K 线，
    // 步进是唯一会加步的动作。改成一操作一步之后那样必然算错：画一个图形、
    // 挂一张单、录一段音也各加一步，而它们的广播不会带 bar_advanced。
    // 现在服务端在每条广播里带上「这条被录成了第几步」，界面只显示，不自己数。
    if (inst.recording?.active && msg.recStep && msg.recStep > 0) {
      const adv = msg.action === ACTION.barAdvanced
      const cur = adv ? (msg.payload as BarAdvancedPayload) : null
      inst.recording = {
        ...inst.recording,
        step: msg.recStep,
        total: Math.max(inst.recording.total ?? 0, msg.recStep),
        kind: msg.action,
        barNo: adv ? (inst.recording.barNo ?? 1) + 1 : inst.recording.barNo,
        current: cur?.bar?.time ?? inst.recording.current,
        // 换了一根 K 线：录音额度与「本根已录几条」都要归零。服务端的 state
        // 只在下一次快照/录音广播时才来，这里不跟着清，HUD 会拿上一根的清单
        // 当这一根的（明明还没录，却显示「本根 3/9」）。
        ...(adv ? { barAudios: 0, clips: [] } : {}),
      }
    }

    // 下面是界面状态，不属于盘面
    if (msg.action === ACTION.annotationDeleted) {
      const p = msg.payload as AnnotationDeletedPayload
      // 别人（或机器人）删掉了我正在编辑的那个：选中必须跟着失效，
      // 否则手柄会画在一个已经不存在的图形上，下一次 Del 打到空气。
      if (editingId.value === p.annotationId) selectAnnotation(null)
    } else if (msg.action === ACTION.annotationCleared) {
      selectAnnotation(null)
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
        noteNotice('开始录制：当前盘面记为第 1 步，之后每个操作各占一步')
        break
      }
      case ACTION.recordingAudio: {
        const p = msg.payload as RecordingAudioPayload
        inst.recording = p.state
        noteNotice(
          `录音 ${p.state.audios} 条已挂到 ${p.audio.time}（第 ${p.audio.step} 步 · ${(p.audio.durationMs / 1000).toFixed(1)} 秒）`,
        )
        break
      }
      case ACTION.recordingAudioCleared: {
        const p = msg.payload as RecordingAudioClearedPayload
        inst.recording = p.state
        noteNotice(`已清除这根 K 线上的 ${p.removed.length} 条录音`)
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

  /**
   * 改指标设置。
   *
   * 整体换一份新对象（而不是原地改字段）：画布那边 watch 的是 `store.indicator`，
   * 原地改属性不会触发重画 —— 用户拧了输入框却看不到图变，是最典型的「没生效」。
   * 夹值也在这里做，输入框、开关、以后别的入口共用一道闸。
   */
  function setIndicator(patch: Partial<IndicatorSettings>): void {
    indicator.value = sanitizeIndicator({ ...indicator.value, ...patch })
    try {
      localStorage.setItem(INDICATOR_KEY, JSON.stringify(indicator.value))
    } catch {
      // 记不住就算了，不影响本次使用
    }
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

  /** 请求策略清单。连上会自动取一次；用户重开启动面板时也可以手动再取。 */
  function listStrategies(): void {
    send({ type: 'list_strategies' })
  }

  /**
   * 启动机器人。
   *
   * 不填 strategy 就是原来的「随机演示机器人」：只会步进、乱画线、乱下单，
   * 用来验证链路。填了策略 id 就是策略机器人：每推进一根 K 线问策略一次该不该开仓，
   * 该开就市价进、挂止损和止盈两张保护单，收盘前平掉。
   *
   * intervalMs 传 0 = 让后端按类型给默认节奏（演示 1.2 秒、策略 0.3 秒）。
   * quantity 传 0 = 1 手。
   */
  function startBot(
    opts: { strategy?: string; quantity?: number; intervalMs?: number; params?: Record<string, number> } = {},
  ): void {
    if (!instance.value) return
    send({
      type: 'bot_start',
      instanceId: instance.value.id,
      intervalMs: opts.intervalMs ?? 0,
      strategy: opts.strategy ?? '',
      quantity: opts.quantity ?? 0,
      // 没填参数就不发这个键：后端拿到 undefined 会整份用默认值，
      // 传 {} 和传 undefined 在 Go 里是同一个 nil map，但少一个键日志更干净。
      params: opts.params,
    })
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
    selectAnnotation,
    pointerMode,
    editingId,
    editing,
    templates,
    activeTemplate,
    theme,
    showEma,
    indicator,
    setIndicator,
    orderQuantity,
    tick,
    priceDecimals,
    // 派生
    bars,
    cursor,
    currentBar,
    isOperator,
    botRunning,
    botStrategyLabel,
    canOperate,
    operatorLabel,
    strategies,
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
    listStrategies,
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
