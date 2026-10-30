import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

import { defaultStyle, resolveStyle } from '@/components/chart/annotations'
import { atrSeries, sanitizeAtr, type AtrSettings } from '@/components/chart/atr'
import { sanitizeBarCount, type BarCountSettings } from '@/components/chart/barcount'
import { sanitizeIndicator, type IndicatorSettings } from '@/components/chart/indicators'
import { DEFAULT_INDICATOR_PREFS, loadPrefs, savePrefs } from '@/lib/indicatorPrefs'
import { DEFAULT_NATR, planNatr, sanitizeNatr, type NatrSettings } from '@/lib/natr'
import {
  openDesk,
  type BarAdvancedPayload,
  type DeskConnection,
  type DeskEvent,
  type DeskSnapshot,
  type DeskState,
} from '@/data/socket'
import { TIMEFRAMES } from '@/types/protocol'
import type {
  Annotation,
  AnnotationKind,
  AnnotationStyle,
  Bar,
  ClosedLot,
  FlatAllPayload,
  InstanceDetail,
  Order,
  OrderFilledPayload,
  OrderKind,
  OrdersPayload,
  Point,
  Position,
  Side,
  Template,
  Timeframe,
  Trade,
} from '@/types/protocol'

/**
 * 交易页的状态。**它是 `web/src/stores/replay.ts` 的本地版**：
 * 画布和面板组件按同一份字段名读它，所以 `components/ChartPanel.vue` 之类搬过来
 * 一行都不用改（只换了 import）。
 *
 * 差别只有「谁来执行」：
 *
 * | | web/（复盘） | 这里 |
 * |---|---|---|
 * | K 线 | Go 服务的 WS 快照 + 逐根 step | Spring 的 `/ws`：连上给一份快照，之后每次 step 收一条广播 |
 * | 画线 / 模板 | 发消息给后端，后端编号后广播 | 只在本页内存 |
 * | 下单 / 撤单 / 平仓 | 后端撮合、落库、广播 | 同左：发一条意图，**等广播照抄**（见 {@link placeOrder}） |
 *
 * ## 为什么所有动作都走 WebSocket，而不是「读用 HTTP、写用 WS」
 *
 * 因为**写完之后盘面是什么样，只有后端知道**：下一根要读 CSV、账户的 `lastBarTime`
 * 要落库、操作记录要写 `audit_event`。前端若能自己算出同一份结果，就等于把这个算法实现了两遍 ——
 * 两边只要差一根，用户看到的就是「按了一下，图上跳了两格」。
 *
 * 所以这条链路是单向的：**发意图 → 后端算 → 广播结果 → 前端照抄**。
 * {@link connect} 里的 `onSnapshot` / `onEvent` 是唯一改 `instance` 的入口。
 *
 * ## 「停在哪一根」为什么是账户的状态
 *
 * 账户上有一个 `lastBarTime`：建户时等于起点，每步进一根就往后推一格，由后端落库。
 * 交易页打开时后端按它截断 —— 否则每次「继续交易」都整天全揭示，
 * 等于每次回来都提前看到未来，训练就没了。
 *
 * ## 为什么不假装「下单成功」
 *
 * 撮合、挂单状态、逐批平仓盈亏全在后端（见 `README.md` §三）。
 * 前端先本地生成一笔成交，界面会立刻和账户余额对不上 —— 而余额是后端算的。
 * 那比「按下去没反应」更糟：人以为自己开了仓。
 *
 * ## 画线为什么仍然允许在本地生效
 *
 * 画线是纯视觉的：它不改余额、不改持仓、不改考核判定。后端还没接 `annotation_*`
 * （消息会被明确拒绝），所以图形暂时只活在这个标签页里，刷新即失。
 */

/** 画布上当前手里的工具：指针 + 五种绘图工具（和 web/ 完全一致）。 */
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

/** 五种工具各给一条出厂模板：没有它，第一次画线时模板条是空的。 */
function seedTemplates(): Template[] {
  return DRAW_TOOLS.map((kind, i) => ({
    id: `local-${kind}`,
    kind,
    name: '默认',
    style: defaultStyle(kind),
    createdAt: i,
  }))
}

/** `"2024-03-04 09:35"` → 当天第几分钟。解析不了返回 NaN（聚合时整根丢掉）。 */
function minuteOf(time: string): number {
  const t = time.length >= 16 ? time.slice(11, 16) : ''
  const hh = Number(t.slice(0, 2))
  const mm = Number(t.slice(3, 5))
  return Number.isFinite(hh) && Number.isFinite(mm) ? hh * 60 + mm : Number.NaN
}

/**
 * 把 5 分钟序列聚合成更大的周期（10/15/30/60/2h/3h/4h）。
 *
 * 旧实现走 `GET /api/timeframe` 让 Go 服务算；这里只有 CSV，就在前端现算 ——
 * 一整天 276 根的桶运算，比一次网络往返还便宜。分桶按「当天第几分钟」切，
 * 所以每天的第一根永远是某个整点桶的开头，跨日不会把两根不相干的 K 线缝在一起。
 *
 * `ema20` 一律置空：那是均线，聚合周期上的均线得重新算，而画布只在 5 分钟上画 EMA。
 */
function aggregate(source: Bar[], stepMinutes: number): Bar[] {
  if (stepMinutes <= 5 || source.length === 0) return source
  const out: Bar[] = []
  let bucket = Number.NaN
  let cur: Bar | null = null
  for (const b of source) {
    const m = minuteOf(b.time)
    const key = Number.isNaN(m) ? Number.NaN : Math.floor(m / stepMinutes)
    if (cur === null || key !== bucket) {
      if (cur) out.push(cur)
      bucket = key
      cur = {
        time: b.time,
        open: b.open,
        high: b.high,
        low: b.low,
        close: b.close,
        volume: b.volume,
        ema20: null,
      }
      continue
    }
    cur.high = Math.max(cur.high, b.high)
    cur.low = Math.min(cur.low, b.low)
    cur.close = b.close
    cur.volume += b.volume
  }
  if (cur) out.push(cur)
  return out
}

/**
 * 补 EMA20。
 *
 * 旧版这条线由 Go 服务算好（`Bar.ema20`）；这里只有 CSV，就现算 —— 口径按
 * 「首根收盘价起算、周期 20 的指数平滑」，前 19 根没有值（和原来下发的形状一致：
 * `null`，画布据此从第 20 根开始画）。
 */
function withEma(bars: Bar[], length = 20): Bar[] {
  const k = 2 / (length + 1)
  let prev = Number.NaN
  return bars.map((b, i) => {
    prev = i === 0 ? b.close : b.close * k + prev * (1 - k)
    return { ...b, ema20: i < length - 1 ? null : prev }
  })
}

export const useTradeStore = defineStore('trade', () => {
  // ---------------- 数据 ----------------
  const instance = ref<InstanceDetail | null>(null)
  /** 当前标的代码（`_symbols.json` 的键）与**截止那天**（后端回的 `day`，可能跨两个日文件）。
   *  **标的由账户锁定**：这一页没有标的选择器。 */
  const symbolCode = ref('')
  const day = ref('')
  /** 账户名（快照里带回来的，用来写标签页标题）。 */
  const accountName = ref('')
  const loading = ref(false)
  /**
   * 盘面上**已经揭示**的 K 线。快照给到账户记住的那一根，之后每步进一根就追加一根
   * —— 和旧的「先拿一整天再截断」不同，这里手上没有「还没到的那些」，所以没有隐藏区。
   */
  const dayBars = ref<Bar[]>([])
  /** 步进要落库的那个账户。由交易页在 {@link connect} 时给出。 */
  const accountId = ref('')
  /** 账户上记着的「最新 K 线时间」（快照带回来的那个，每步进一根跟着更新）。 */
  const accountBarTime = ref('')
  /** 磁盘上还有没有下一根 —— 【下一根】按钮的可用性。 */
  const hasNext = ref(false)
  /** 这一天（这个日文件）还剩几根没放出来。只用来写提示，不决定按钮能不能按。 */
  const restOfDay = ref(0)
  /** 一条 step 已经发出去、还没等到广播（防连点）。 */
  const pendingStep = ref(false)
  /** 和交易服务的连接状态：connecting / open / closed。 */
  const socketState = ref<DeskState>('connecting')
  const lastError = ref('')
  const errorKey = ref(0)
  /** 一句会自己消失的轻提示（本地动作的结果都走这里）。 */
  const notice = ref('')
  const noticeKey = ref(0)
  let noticeTimer = 0
  /** 连接对象与「等广播」的兜底计时器（连上了但一直没回，不能让按钮永远转）。 */
  let desk: DeskConnection | null = null
  let stepTimer = 0

  // ---------------- 界面状态（只影响显示） ----------------
  const tool = ref<ChartTool>('select')
  const selectedAnnotation = ref<string | null>(null)
  const pointerMode = computed(() => tool.value === 'select')
  const editingId = computed(() => (tool.value === 'select' ? selectedAnnotation.value : null))
  const editing = computed(() => editingId.value !== null)
  const editingAnnotation = computed(() => {
    const id = editingId.value
    if (!id) return null
    return (instance.value?.annotations ?? []).find((a) => a.id === id) ?? null
  })

  function selectAnnotation(id: string | null): void {
    if (id !== null && tool.value !== 'select') return
    selectedAnnotation.value = id
  }

  /** 模板：本页内存里的一套（每种工具一条出厂模板，可以自己再加）。 */
  const templates = ref<Template[]>(seedTemplates())
  const activeTemplate = ref<Record<string, string>>({})
  /** 本页内存里的自增 id：模板和图形各一个，别共用 —— 共用会让 id 看起来毫无规律。 */
  let templateSeq = 0
  let annotationSeq = 0

  /**
   * 指标设置（EMA / ATR / Doji X + ZigZag / Bar Count）。
   *
   * 初值**从本机存档读**（`lib/indicatorPrefs.ts`），不是写死的出厂值 ——
   * 「上次调到的那一套」和缩放、下单浮窗的位置一样属于这台电脑的偏好，刷新该还在。
   * 读出来的东西已经过 sanitize：存档被手改坏了只会让**那一个**字段回默认。
   */
  const savedPrefs = loadPrefs()
  const showEma = ref(savedPrefs.showEma)
  const atr = ref<AtrSettings>(savedPrefs.atr)
  const indicator = ref<IndicatorSettings>(savedPrefs.indicator)
  const barCount = ref<BarCountSettings>(savedPrefs.barCount)
  const natr = ref<NatrSettings>({ ...DEFAULT_NATR })

  const orderQuantityRaw = ref(1)
  const orderQuantity = computed<number>({
    get: () => orderQuantityRaw.value,
    set: (v) => {
      const n = Math.round(Number(v))
      orderQuantityRaw.value = Number.isFinite(n) && n >= 1 ? Math.min(n, 10000) : 1
    },
  })

  // ---------------- 派生 ----------------
  const bars = computed(() => instance.value?.bars ?? [])
  const cursor = computed(() => instance.value?.cursor ?? 0)
  const currentBar = computed(() => bars.value[bars.value.length - 1] ?? null)
  /** 盘面现在停在哪一根（已揭示的最后一根的时间）—— 也就是账户上那个 lastBarTime。 */
  const currentTime = computed(() => currentBar.value?.time ?? '')
  const tick = computed(() => instance.value?.tick ?? 0)
  const priceDecimals = computed(() => {
    const t = tick.value
    if (!(t > 0)) return 2
    return Math.min(6, Math.max(2, Math.ceil(-Math.log10(t) - 1e-9)))
  })
  const pointValue = computed(() => {
    const t = tick.value
    const tv = instance.value?.tickValue ?? 0
    if (t > 0 && tv > 0) return tv / t
    return 1
  })

  /** 交易页是单用户看自己的图：没有「操作权」这回事，能画就能画。 */
  const canOperate = computed(() => true)
  const isOperator = computed(() => true)

  const openOrders = computed(() => (instance.value?.orders ?? []).filter((o) => o.status === 'open'))

  const atrValue = computed<number | null>(() => {
    const list = bars.value
    if (list.length === 0) return null
    const series = atrSeries(list, atr.value.length, atr.value.smoothing)
    const v = series[series.length - 1]
    return Number.isFinite(v) ? v : null
  })

  const natrPlan = computed(() => {
    const a = atrValue.value
    if (a === null) return null
    return planNatr(a, natr.value, pointValue.value)
  })

  const timeframe = ref<Timeframe>('5m')
  const displayBars = computed(() => {
    const tf = TIMEFRAMES.find((t) => t.id === timeframe.value)
    return aggregate(bars.value, tf?.minutes ?? 5)
  })

  // ---------------- 提示 ----------------
  function noteError(message: string): void {
    lastError.value = message
    errorKey.value += 1
  }

  function clearError(): void {
    lastError.value = ''
  }

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

  /**
   * 一条会改盘面的动作已经发出去、还没等到广播（防连点）。
   *
   * 空串 = 没有在等。它是**唯一**的等待位：一次只允许一条未确认的动作 ——
   * 并发发出两条，回来的两条广播分不清哪条是哪条，用户也不知道自己按的哪个先生效。
   */
  const pendingAction = ref('')
  let actionTimer = 0

  function clearActionTimer(): void {
    if (actionTimer) window.clearTimeout(actionTimer)
    actionTimer = 0
  }

  // ---------------- 连上交易服务（唯一的取数与写入入口） ----------------

  /**
   * 进入一个账户的盘面：开 WebSocket、自报 `accountId`、等后端回快照。
   *
   * **这一页不再调任何 HTTP 接口**。账户名、标的、K 线窗口、「还有没有下一根」
   * 全部来自快照 ── 它们互相之间有约束（窗口截止到 `barTime`、`hasNext` 是从
   * `barTime` 往后找的），分成几个接口去取就会出现「A 接口的结果配 B 接口的参数」。
   *
   * 断线后连接层会自动重连并重新 `hello`，后端重发快照 —— 所以这里只负责开局，
   * 不用管重连：{@link applySnapshot} 是幂等的，重连就是「把盘面按后端的样子重建一遍」。
   */
  function connect(id: string): void {
    if (!id) {
      noteError('没有账户 id，打不开盘面')
      return
    }
    desk?.close()
    accountId.value = id
    loading.value = true
    socketState.value = 'connecting'

    desk = openDesk(id, {
      onSnapshot: (snapshot) => applySnapshot(snapshot),
      onEvent: (event) => applyEvent(event),
      onError: (message) => {
        pendingStep.value = false
        pendingAction.value = ''
        clearStepTimer()
        clearActionTimer()
        loading.value = false
        noteError(message)
      },
      onState: (state) => {
        socketState.value = state
        if (state === 'closed') {
          // 断线时不能让人以为按钮还灵：pending 一起清掉，按钮会立刻变灰
          pendingStep.value = false
          pendingAction.value = ''
          clearStepTimer()
          clearActionTimer()
        }
      },
    })
  }

  /** 关页面时收尾：主动断开，别让连接层在后台一直重连。 */
  function disconnect(): void {
    desk?.close()
    desk = null
    clearStepTimer()
    clearActionTimer()
    pendingStep.value = false
    pendingAction.value = ''
  }

  /**
   * 装上一份盘面。**幂等**：重连后重发快照就是重建，不叠加。
   *
   * `bars` 已经在后端截断到账户的 `lastBarTime`（他自己那一根），所以这里全给，
   * 不需要前端再截一次 —— 截断的规则只有后端一份。
   */
  function applySnapshot(snapshot: DeskSnapshot): void {
    const list = withEma(
      snapshot.bars.map((b) => ({ ...b, ema20: null as number | null })),
    )
    symbolCode.value = snapshot.symbolId
    day.value = snapshot.day
    accountName.value = snapshot.accountName ?? ''
    lastBarTime = snapshot.barTime ?? ''
    accountBarTime.value = lastBarTime
    hasNext.value = snapshot.hasNext
    dayBars.value = list
    restOfDay.value = 0
    pendingStep.value = false
    pendingAction.value = ''
    clearStepTimer()
    clearActionTimer()

    instance.value = {
      id: `${snapshot.symbolId}|${snapshot.day}`,
      symbol: snapshot.symbolId,
      folder: snapshot.folder,
      anchor: list[0]?.time ?? `${snapshot.day} 00:00`,
      cursor: Math.max(0, list.length - 1),
      tick: snapshot.tick ?? 0,
      tickValue: snapshot.tickValue ?? 0,
      bars: list,
      hiddenBars: 0,
      hasMore: snapshot.hasNext,
      annotations: [],
      // 账和 K 线一起来（重连后整块重建）；数组永远给，空就是「确实是空的」
      orders: snapshot.orders ?? [],
      trades: snapshot.trades ?? [],
      closedLots: snapshot.closedLots ?? [],
      position: snapshot.position ?? { quantity: 0, side: 'flat', averagePrice: 0 },
      realizedPnl: snapshot.realizedPnl ?? 0,
      unrealizedPnl: snapshot.unrealizedPnl ?? 0,
      operator: '',
      members: [],
      bot: false,
      seq: 0,
      events: [],
      notes: [],
      createdBy: '',
      createdAt: Date.now(),
      voice: { barTime: '', clips: [], total: 0, max: 9 },
      voiceAll: [],
    }
    // 换了标的 / 换了日子 = 换了图：旧图上的选中必须一起丢掉
    selectedAnnotation.value = null
    loading.value = false
    clearError()
    if (list.length === 0) {
      noteNotice(
        `${snapshot.symbolId} 在 ${snapshot.day} 这一屏还没有 K 线（休市、没抓，或者账户的位置还在这一天之前）`,
      )
    }
  }

  /**
   * 后端广播的一条动作。**账的每一次变化都只从这里进来**。
   *
   * 任何一条广播都说明后端还活着：「在等回应」的等待位就地清掉，
   * 按钮从转圈回到可点 —— 不用等 8 秒兜底计时器。
   */
  function applyEvent(event: DeskEvent): void {
    pendingAction.value = ''
    clearActionTimer()
    switch (event.action) {
      case 'bar_advanced':
        // 撮合就发生在这一步：负载里 orders / trades / closedLots / position 已经是
        // 「这根 K 线撮合完」的样子，不需要再等一条 order_filled
        applyAdvanced(event.payload as BarAdvancedPayload)
        return
      case 'order_placed':
      case 'order_cancelled':
      case 'order_moved':
        // 这三个只改挂单区（改价连成交都不动），但后端还是发全量列表：
        // 前端只做整份替换，不做增量合并
        applyPortfolio(event.payload as OrdersPayload)
        return
      case 'order_filled':
      case 'flat_all':
        applyPortfolio(event.payload as OrderFilledPayload | FlatAllPayload)
        return
      default:
        // 收到一个还不认识的动作：说明协议变了，得说出来，不能装作没事
        noteError(`收到一个还不认识的动作「${event.action}」`)
    }
  }

  /**
   * 把一份「全量账」盖到盘面上。
   *
   * **整份替换，不做增量合并**：后端每次发的都是全量（一次复盘通常只有个位数的单子，
   * 全量发的代价很小），而增量合并逻辑极易出错 —— 少合并一笔成交不会报错，
   * 只会让持仓和账户余额慢慢对不上。缺省的字段保持原样（`order_placed` 不带成交）。
   */
  function applyPortfolio(book: {
    orders?: Order[]
    trades?: Trade[]
    closedLots?: ClosedLot[]
    position?: Position
    realizedPnl?: number
    unrealizedPnl?: number
  }): void {
    const inst = instance.value
    if (!inst) return
    instance.value = {
      ...inst,
      orders: book.orders ?? inst.orders,
      trades: book.trades ?? inst.trades,
      closedLots: book.closedLots ?? inst.closedLots,
      position: book.position ?? inst.position,
      realizedPnl: book.realizedPnl ?? inst.realizedPnl,
      unrealizedPnl: book.unrealizedPnl ?? inst.unrealizedPnl,
    }
  }

  /**
   * 【下一根】的广播：把那一根接到盘面末尾。
   *
   * 三个细节：
   *
   * - **同一根只接一次**：重连后如果没有更晚的 K 线，后端不会重播旧事件，但同一时间戳
   *   重复到达仍然是不该发生的 —— 真发生了，宁可忽略也不要插入一根重复的 K 线（图会崩）。
   * - **EMA 要重算**：新收盘价会改变最后几个 EMA。重算整列的代价是一天几百个数，
   *   比「维护一个增量公式」便宜得多，也不会和 `withEma` 的口径分叉。
   * - **`day` 跟着走**：跨日以后盘面还是同一块（后续的 K 线接在后面），
   *   所以 `instance.id` 不动 —— 动一下画布会当成「换了张图」把缩放重置掉。
   */
  function applyAdvanced(payload: BarAdvancedPayload): void {
    pendingStep.value = false
    clearStepTimer()
    const bar: Bar = { ...payload.bar, ema20: null }
    hasNext.value = payload.hasMore

    if (dayBars.value.some((b) => b.time === bar.time)) return

    dayBars.value = withEma([...dayBars.value, bar])
    restOfDay.value = payload.hiddenBars
    accountBarTime.value = bar.time
    if (bar.time.length >= 10) day.value = bar.time.slice(0, 10)

    const inst = instance.value
    if (!inst) return
    instance.value = {
      ...inst,
      bars: dayBars.value,
      cursor: Math.max(0, dayBars.value.length - 1),
      hiddenBars: payload.hiddenBars,
      hasMore: payload.hasMore,
    }
    // 这根 K 线可能撮合了挂单：账和 K 线在同一条广播里，一起换掉
    applyPortfolio(payload)
  }

  /** 快照里那个「最新 K 线时间」，用来判断「是不是已经走到最后」。 */
  let lastBarTime = ''

  function clearStepTimer(): void {
    if (stepTimer) window.clearTimeout(stepTimer)
    stepTimer = 0
  }

  // ---------------- 指标设置（改完立刻重画：整份换新对象） ----------------
  function setIndicator(patch: Partial<IndicatorSettings>): void {
    indicator.value = sanitizeIndicator({ ...indicator.value, ...patch })
  }

  function setAtr(patch: Partial<AtrSettings>): void {
    atr.value = sanitizeAtr({ ...atr.value, ...patch })
  }

  function setBarCount(patch: Partial<BarCountSettings>): void {
    barCount.value = sanitizeBarCount({ ...barCount.value, ...patch })
  }

  function setNatr(patch: Partial<NatrSettings>): void {
    natr.value = sanitizeNatr({ ...natr.value, ...patch })
  }

  function setTimeframe(tf: Timeframe): void {
    timeframe.value = tf
  }

  /**
   * 指标恢复出厂（【指标 ▾】底部的「恢复默认」）。
   *
   * 四项**一起**打回默认，而不是逐项清 —— 用户点这个的意思就是「这套调乱了，重来」。
   * 存档不用手动删：下面那个 `watch` 看到引用变了就会重写一遍。
   */
  function resetIndicators(): void {
    const d = DEFAULT_INDICATOR_PREFS
    showEma.value = d.showEma
    atr.value = { ...d.atr }
    indicator.value = { ...d.indicator }
    barCount.value = { ...d.barCount }
    noteNotice('指标已恢复默认（存到这台电脑的浏览器里）')
  }

  // ---------------- 标注模板 ----------------
  function templatesFor(kind: DrawTool): Template[] {
    return templates.value.filter((t) => t.kind === kind)
  }

  function templateFor(kind: DrawTool): Template | null {
    const list = templatesFor(kind)
    const id = activeTemplate.value[kind]
    return list.find((t) => t.id === id) ?? list[0] ?? null
  }

  function selectTemplate(kind: DrawTool, id: string): void {
    activeTemplate.value = { ...activeTemplate.value, [kind]: id }
  }

  /** 新建（不带 id）或就地修改（带 id）。存在本页内存，刷新即回到出厂那五条。 */
  function saveTemplate(input: { id?: string; kind: DrawTool; name: string; style: AnnotationStyle }): void {
    const style = resolveStyle({ kind: input.kind, style: input.style })
    const existing = input.id ? templates.value.find((t) => t.id === input.id) : undefined
    if (existing) {
      templates.value = templates.value.map((t) =>
        t.id === existing.id ? { ...t, name: input.name, style } : t,
      )
      noteNotice(`模板「${input.name}」已更新（本页有效，刷新即失）`)
      return
    }
    const t: Template = {
      id: `local-t${++templateSeq}`,
      kind: input.kind,
      name: input.name,
      style,
      createdAt: Date.now(),
    }
    templates.value = [...templates.value, t]
    selectTemplate(input.kind, t.id)
    noteNotice(`模板「${t.name}」已添加（本页有效，刷新即失）`)
  }

  function deleteTemplate(id: string): void {
    templates.value = templates.value.filter((t) => t.id !== id)
  }

  // ---------------- 标注（本页内存） ----------------
  function patchAnnotations(fn: (list: Annotation[]) => Annotation[]): void {
    const inst = instance.value
    if (!inst) return
    instance.value = { ...inst, annotations: fn(inst.annotations) }
  }

  function addAnnotation(input: {
    kind: AnnotationKind
    start: Point
    end: Point
    style: AnnotationStyle
    snap: boolean
  }): void {
    if (!instance.value) return
    const a: Annotation = {
      id: `local-a${++annotationSeq}`,
      kind: input.kind,
      start: input.start,
      end: input.end,
      style: resolveStyle({ kind: input.kind, style: input.style }),
      snapped: input.snap,
      createdBy: 'local',
      createdAt: Date.now(),
    }
    patchAnnotations((list) => [...list, a])
  }

  function moveAnnotation(id: string, start: Point, end: Point, snap: boolean): void {
    patchAnnotations((list) =>
      list.map((a) => (a.id === id ? { ...a, start, end, snapped: snap } : a)),
    )
  }

  function deleteAnnotation(id: string): void {
    if (editingId.value === id) selectAnnotation(null)
    patchAnnotations((list) => list.filter((a) => a.id !== id))
  }

  function restyleAnnotation(id: string, style: AnnotationStyle): void {
    patchAnnotations((list) =>
      list.map((a) => (a.id === id ? { ...a, style: resolveStyle({ kind: a.kind, style }) } : a)),
    )
  }

  function clearAnnotations(): void {
    selectAnnotation(null)
    patchAnnotations(() => [])
  }

  // ---------------- 交易动作：发意图 → 后端算 → 等广播 ----------------

  /**
   * 发一条会改盘面的消息，然后**就地等广播**。
   *
   * 不本地预演任何效果：撮合、成交价、盈亏全在后端（见 `TradingService`），
   * 前端先改一遍本地状态就会立刻和账户对不上 —— 而「对不上」比「慢一拍」糟得多。
   *
   * 三条护栏都在这里，一次说清：
   * 1. 没有盘面 / 连接断了 / 正在等上一条 —— 当场说明，不发；
   * 2. 发不出去（连接刚断）—— 明说「没发出去」，不假装成功；
   * 3. 8 秒没回 —— 放弃这一步并提示，按钮不会永远转着。
   */
  function sendAction(type: string, extra: Record<string, unknown>, label: string): boolean {
    if (!instance.value) {
      noteError('还没有盘面，先打开一个账户')
      return false
    }
    if (pendingAction.value) {
      noteError(`「${pendingAction.value}」还没有回应，稍等一下再点`)
      return false
    }
    if (socketState.value !== 'open' || !desk) {
      noteError('和交易服务的连接断了，正在重连：连上之后再试')
      return false
    }
    if (!desk.send({ type, accountId: accountId.value, ...extra })) {
      noteError('这条消息没发出去（连接刚断），稍后重试')
      return false
    }
    pendingAction.value = label
    clearActionTimer()
    actionTimer = window.setTimeout(() => {
      pendingAction.value = ''
      actionTimer = 0
      noteError('交易服务 8 秒没有回应，已放弃这一步：请确认后端还在运行')
    }, 8000)
    return true
  }

  /**
   * 挂一张限价单或止损单（`kind` = `limit` / `stop`）。
   *
   * 它只是「等价格过来」的意图：本根挂的单**从下一根才撮合**，所以发出去之后
   * 挂单列表里会立刻多一条 `open`，成交要等后面某根 K 线碰到它。
   *
   * `kind` 传 `market` 时转到 {@link marketOrder} —— 市价单下单即成交，
   * 不留在挂单列表里，走的是另一条命令（`market_order`）。
   */
  function placeOrder(side: Side, kind: OrderKind, price: number, quantity: number): void {
    if (kind === 'market') {
      marketOrder(side, quantity)
      return
    }
    if (!(price > 0)) {
      noteError('挂单价格必须大于 0')
      return
    }
    if (!(quantity > 0)) {
      noteError('手数必须大于 0')
      return
    }
    sendAction('order_placed', { side, kind, price, quantity }, `${side === 'buy' ? '买' : '卖'}${kind === 'limit' ? '限价' : '止损'} ${quantity} 手`)
  }

  /**
   * 市价单：按当前这根 K 线的收盘价立即成交。
   *
   * `stopDistance` > 0 时，后端会在**同一个事务里**顺手挂一张反方向的止损单
   * （价格 = 成交价 ∓ 距离）。传的是**距离**而不是止损价：成交价必须是后端自己的那个，
   * 让前端拿现价去减会在四舍五入上差一跳，而那一跳正好落在止损位上。
   */
  function marketOrder(side: Side, quantity: number, stopDistance = 0): void {
    if (!(quantity > 0)) {
      noteError('手数必须大于 0')
      return
    }
    const extra: Record<string, unknown> = { side, quantity }
    if (stopDistance > 0) extra.stopDistance = stopDistance
    sendAction('market_order', extra, `${side === 'buy' ? '买' : '卖'}市价 ${quantity} 手`)
  }

  function cancelOrder(orderId: string): void {
    sendAction('order_cancelled', { orderId }, '撤单')
  }

  function moveOrder(orderId: string, price: number): void {
    if (!(price > 0)) {
      noteError('挂单价必须大于 0')
      return
    }
    sendAction('order_moved', { orderId, price }, '改挂单价')
  }

  /**
   * 一键退仓：市价平掉整个仓位 + 撤掉全部挂单。
   *
   * 提示语在**发出去之前**按当前盘面拼好：这条动作会把仓位和挂单都清掉，
   * 等广播回来之后再读 instance，已经数不出「平了几手、撤了几张」了。
   * 真正的执行在后端（`TradingService.flatAll`，先平后撤），这里不本地改状态。
   */
  function flatAll(): void {
    sendAction('flat_all', {}, '一键退仓')
  }

  /**
   * 【下一根】：把盘面往后放一根（工具栏的按钮、画布上的 → 键都走这里）。
   *
   * 这是一条**意图**，不是一次本地推进：
   *
   * 1. 先看连接在不在。断了就明说 —— 连接层正在重连，这时候按下去什么都不会发生，
   *    不给提示的话人只会以为按钮坏了；
   * 2. 再看后端说还有没有下一根（`hasNext` 来自快照和上一次广播）。没有了就说明白等什么；
   * 3. 发出去之后**就地等广播**：真正把 K 线接上去的是 {@link applyAdvanced}，
   *    连发起方也等那条广播 —— 一份数据一个来源，慢一个往返换来永远对得上。
   *
   * 兜底计时器是为了「连上了但后端一直没回」这种半死状态：8 秒后把 pending 清掉，
   * 否则按钮会永远转着，人只会反复点、越堆越多。
   */
  function step(): void {
    if (!instance.value) return
    if (pendingStep.value) return

    if (socketState.value !== 'open' || !desk) {
      noteError('和交易服务的连接断了，正在重连：连上之后再按【下一根】')
      return
    }
    if (!hasNext.value) {
      noteNotice(
        `${currentTime.value || '—'} 已经是磁盘上最后一根 K 线了：等抓取程序再录一段行情才能继续。`,
      )
      return
    }
    if (!desk.send({ type: 'step', accountId: accountId.value })) {
      noteError('这一条消息没发出去（连接刚断），稍后重试')
      return
    }

    pendingStep.value = true
    clearStepTimer()
    stepTimer = window.setTimeout(() => {
      pendingStep.value = false
      stepTimer = 0
      noteError('交易服务 8 秒没有回应，已放弃这一步：请确认后端还在运行')
    }, 8000)
  }

  // 换周期不需要请求，聚合是 computed 现算的；这里保留 watch 只为清掉「上一份」的选中态。
  watch(timeframe, () => selectAnnotation(null))

  /**
   * 指标一改就写回本机存档。
   *
   * 挂在**四个 ref 的组合**上而不是各个 setter 里，是因为 EMA 的开关走的是模板里的
   * `v-model="store.showEma"`（直接改 ref，不过 setter），而那一条恰恰是最常被点的。
   * `deep: true` 兜住「原地改字段」这种改法（现在没有，但将来加一个就别忘了这里）。
   *
   * 每次改动整份重写：存档只有百来字节，比维护「哪几个字段脏了」便宜得多，也不会
   * 出现「改了 A 忘了写 A」这种漏项（写漏一个，下次刷新就回到旧值，最难查）。
   */
  watch(
    [showEma, atr, indicator, barCount],
    () => {
      savePrefs({
        showEma: showEma.value,
        atr: atr.value,
        indicator: indicator.value,
        barCount: barCount.value,
      })
    },
    { deep: true },
  )

  return {
    // 连接
    connect,
    disconnect,
    socketState,
    accountId,
    // 数据
    instance,
    symbolCode,
    accountName,
    day,
    loading,
    bars,
    cursor,
    currentBar,
    currentTime,
    restOfDay,
    hasNext,
    pendingStep,
    pendingAction,
    accountBarTime,
    displayBars,
    timeframe,
    setTimeframe,
    // 提示
    lastError,
    errorKey,
    notice,
    noticeKey,
    noteNotice,
    clearNotice,
    noteError,
    clearError,
    // 界面状态
    tool,
    selectedAnnotation,
    selectAnnotation,
    pointerMode,
    editingId,
    editing,
    editingAnnotation,
    canOperate,
    isOperator,
    // 指标
    showEma,
    atr,
    setAtr,
    indicator,
    setIndicator,
    barCount,
    setBarCount,
    resetIndicators,
    natr,
    setNatr,
    atrValue,
    natrPlan,
    // 盘面派生
    tick,
    priceDecimals,
    pointValue,
    openOrders,
    orderQuantity,
    // 模板
    templates,
    activeTemplate,
    templatesFor,
    templateFor,
    selectTemplate,
    saveTemplate,
    deleteTemplate,
    // 标注
    addAnnotation,
    moveAnnotation,
    deleteAnnotation,
    restyleAnnotation,
    clearAnnotations,
    // 交易（发意图，等广播）
    placeOrder,
    marketOrder,
    cancelOrder,
    moveOrder,
    flatAll,
    step,
  }
})
