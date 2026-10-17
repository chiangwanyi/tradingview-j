import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { ReplaySocket, defaultSocketUrl } from '@/net/socket'
import { ACTION } from '@/types/protocol'
import type {
  AnnotationKind,
  AnnotationPayload,
  AnnotationDeletedPayload,
  BarAdvancedPayload,
  ClientMessage,
  ControlChangedPayload,
  InstanceDetail,
  InstanceStateMessage,
  InstanceSummary,
  MembersChangedPayload,
  OrderKind,
  OrdersPayload,
  Point,
  ReplayEvent,
  ServerEvent,
  ServerMessage,
  Side,
  SymbolInfo,
  You,
} from '@/types/protocol'

/** 画布上当前选中的工具。缩放/平移不属于工具，任何工具下都能用。 */
export type ChartTool = 'select' | 'line' | 'rect' | 'text'

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
  const me = ref<{ clientId: string; name: string; version: string } | null>(null)
  const symbols = ref<SymbolInfo[]>([])
  const instances = ref<InstanceSummary[]>([])
  const instance = ref<InstanceDetail | null>(null)
  const you = ref<You | null>(null)
  const log = ref<ReplayEvent[]>([])
  const lastError = ref('')
  const errorKey = ref(0)

  // 界面状态（只影响显示，不属于业务状态，所以不发给后端）
  const tool = ref<ChartTool>('select')
  const color = ref('#f59e0b')
  const selectedAnnotation = ref<string | null>(null)
  const theme = ref<ThemeMode>(readTheme())
  applyTheme(theme.value)
  /** 是否显示 EMA20（纯显示开关，跟后端无关） */
  const showEma = ref(true)

  const bars = computed(() => instance.value?.bars ?? [])
  const cursor = computed(() => instance.value?.cursor ?? 0)
  const currentBar = computed(() => bars.value[bars.value.length - 1] ?? null)
  const isOperator = computed(() => you.value?.operator === true)
  const botRunning = computed(() => instance.value?.bot === true)
  /** 能操作 = 拿到操作权，且不是机器人在跑（机器人跑着的时候真人只能看，这就是演示模式） */
  const canOperate = computed(() => isOperator.value && !botRunning.value)
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

  function send(msg: ClientMessage): boolean {
    if (!socket.send(msg)) {
      noteError('还没连上服务端（看一下菜单 4 的窗口是不是还开着）')
      return false
    }
    return true
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
        noteError('这个实例已经被删除了')
        instance.value = null
        you.value = null
        log.value = []
        break
      case 'pong':
        break
      default:
        break
    }
  }

  function applyState(msg: InstanceStateMessage): void {
    const inst = msg.instance
    // 快照里的 bars 只含已揭示的部分（后端根本不会发未来的 K 线）
    instance.value = { ...inst, bars: inst.bars ?? [] }
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

    switch (msg.action) {
      case ACTION.barAdvanced: {
        const p = msg.payload as BarAdvancedPayload
        inst.bars = [...inst.bars, p.bar]
        inst.cursor = p.cursor
        inst.hiddenBars = p.hiddenBars
        inst.hasMore = p.hasMore
        inst.orders = p.orders
        inst.trades = p.trades
        inst.position = p.position
        inst.realizedPnl = p.realizedPnl
        inst.unrealizedPnl = p.unrealizedPnl
        break
      }
      case ACTION.annotationAdded: {
        const p = msg.payload as AnnotationPayload
        inst.annotations = [...inst.annotations, p.annotation]
        break
      }
      case ACTION.annotationMoved: {
        const p = msg.payload as AnnotationPayload
        inst.annotations = inst.annotations.map((a) => (a.id === p.annotation.id ? p.annotation : a))
        break
      }
      case ACTION.annotationDeleted: {
        const p = msg.payload as AnnotationDeletedPayload
        inst.annotations = inst.annotations.filter((a) => a.id !== p.annotationId)
        if (selectedAnnotation.value === p.annotationId) selectedAnnotation.value = null
        break
      }
      case ACTION.orderPlaced:
      case ACTION.orderCancelled: {
        const p = msg.payload as OrdersPayload
        inst.orders = p.orders
        break
      }
      case ACTION.membersChanged: {
        const p = msg.payload as MembersChangedPayload
        inst.members = p.members
        break
      }
      case ACTION.controlChanged: {
        const p = msg.payload as ControlChangedPayload
        inst.operator = p.operator
        if (you.value) {
          you.value = { ...you.value, operator: p.operator !== '' && p.operator === me.value?.name }
        }
        break
      }
      case ACTION.botStarted:
        inst.bot = true
        break
      case ACTION.botStopped:
        inst.bot = false
        break
      default:
        break
    }
  }

  /** 断线重连后重新对齐：实例和列表都可能已经变了。 */
  function resync(): void {
    send({ type: 'list_symbols' })
    send({ type: 'list_instances' })
    const id = instance.value?.id
    if (id) send({ type: 'join_instance', instanceId: id })
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

  function addAnnotation(input: {
    kind: AnnotationKind
    start: Point
    end?: Point
    text?: string
    snap: boolean
  }): void {
    if (!instance.value) return
    send({
      type: 'annotation_add',
      instanceId: instance.value.id,
      kind: input.kind,
      start: input.start,
      end: input.end,
      text: input.text,
      color: color.value,
      snap: input.snap,
    })
  }

  function moveAnnotation(id: string, start: Point, end: Point | undefined, snap: boolean): void {
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

  function placeOrder(side: Side, kind: OrderKind, price: number, quantity: number): void {
    if (!instance.value) return
    send({ type: 'order_place', instanceId: instance.value.id, side, kind, price, quantity })
  }

  function cancelOrder(orderId: string): void {
    if (!instance.value) return
    send({ type: 'order_cancel', instanceId: instance.value.id, orderId })
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
    symbols,
    instances,
    instance,
    you,
    log,
    lastError,
    errorKey,
    tool,
    color,
    selectedAnnotation,
    theme,
    showEma,
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
    addAnnotation,
    moveAnnotation,
    deleteAnnotation,
    placeOrder,
    cancelOrder,
    startBot,
    stopBot,
    clearError,
  }
})
