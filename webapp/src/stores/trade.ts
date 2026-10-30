import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

import { defaultStyle, resolveStyle } from '@/components/chart/annotations'
import { atrSeries, DEFAULT_ATR, sanitizeAtr, type AtrSettings } from '@/components/chart/atr'
import { DEFAULT_BAR_COUNT, sanitizeBarCount, type BarCountSettings } from '@/components/chart/barcount'
import { DEFAULT_INDICATOR, sanitizeIndicator, type IndicatorSettings } from '@/components/chart/indicators'
import * as barsApi from '@/data/bars'
import type { SymbolBrief } from '@/data/bars'
import { DEFAULT_NATR, planNatr, sanitizeNatr, type NatrSettings } from '@/lib/natr'
import { TIMEFRAMES } from '@/types/protocol'
import type {
  Annotation,
  AnnotationKind,
  AnnotationStyle,
  Bar,
  InstanceDetail,
  OrderKind,
  Point,
  Side,
  Template,
  Timeframe,
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
 * | K 线 | Go 服务的 WS 快照 + 逐根 step | 一次 HTTP 取一整天，全揭示 |
 * | 画线 / 模板 | 发消息给后端，后端编号后广播 | 只在本页内存 |
 * | 下单 / 撤单 / 平仓 | 后端撮合、落库、广播 | 没有后端，点了给一句明确提示 |
 *
 * ## 为什么不假装「下单成功」
 *
 * 撮合、挂单状态、逐批平仓盈亏全在后端（见 `TRADING-ENGINE-DESIGN.md`）。
 * 前端先本地生成一笔成交，界面会立刻和账户余额对不上 —— 而余额是后端算的。
 * 那比「按下去没反应」更糟：人以为自己开了仓。所以这些动作只写一条提示，
 * 说清楚「后端没接通」，不产生任何假数据。
 *
 * ## 画线为什么允许在本地生效
 *
 * 画线是纯视觉的：它不改余额、不改持仓、不改考核判定。让它在本页生效，
 * 工具链（工具 → 模板 → 拖动 → 删除）就能完整走一遍；
 * 代价是刷新即失，页面顶部有一条常驻说明写着这件事。
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
  const symbols = ref<SymbolBrief[]>([])
  const instance = ref<InstanceDetail | null>(null)
  /** 当前标的代码（`_symbols.json` 的键）与日期。**标的由账户锁定**，页头只有日期选择器；
   *  `setSymbol` 留着是给「换一个账户 / 换一份数据」用的，界面上没有入口。 */
  const symbolCode = ref('')
  const day = ref('')
  const loading = ref(false)
  const lastError = ref('')
  const errorKey = ref(0)
  /** 一句会自己消失的轻提示（本地动作的结果都走这里）。 */
  const notice = ref('')
  const noticeKey = ref(0)
  let noticeTimer = 0

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

  const showEma = ref(true)
  const atr = ref<AtrSettings>({ ...DEFAULT_ATR })
  const indicator = ref<IndicatorSettings>({ ...DEFAULT_INDICATOR })
  const barCount = ref<BarCountSettings>({ ...DEFAULT_BAR_COUNT })
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

  /** 所有「要落库、但后端还没有」的动作都从这里出去 —— 一处文案，免得每个按钮各说一套。 */
  function notWired(what: string): void {
    noteError(`${what}要等交易后端（撮合 / 挂单 / 成交）接通：现在这一步不会产生任何数据。`)
  }

  // ---------------- 取数 ----------------

  /** 拉标的清单。清单为空（还没抓行情）时不是错误，页面会写明「先跑抓取」。 */
  async function loadSymbols(): Promise<void> {
    try {
      symbols.value = await barsApi.symbols()
    } catch (e) {
      noteError(`标的清单读取失败：${String(e instanceof Error ? e.message : e)}`)
    }
  }

  /**
   * 读某个标的某一天的 K 线，并把它装成一块「盘面」。
   *
   * `instance` 是照 `InstanceDetail` 造的：画布只认这几个字段（bars / annotations /
   * orders / trades / position / tick / tickValue），给它一份形状完整的空盘面，
   * 那些分支就都不用改。
   */
  async function load(code: string, date?: string): Promise<void> {
    if (!code) return
    loading.value = true
    try {
      const res = await barsApi.day(code, date || undefined)
      symbolCode.value = res.code
      day.value = res.day
      const list = withEma(
        res.bars.map((b) => ({ ...b, ema20: null as number | null })),
      )
      instance.value = {
        id: `${res.code}|${res.day}`,
        symbol: res.code,
        folder: res.folder,
        anchor: list[0]?.time ?? `${res.day} 00:00`,
        cursor: Math.max(0, list.length - 1),
        tick: res.tick ?? 0,
        tickValue: res.tickValue ?? 0,
        bars: list,
        // 一整天的 K 线一次全部给出：这里没有「逐根揭示」，也就没有未揭示的部分
        hiddenBars: 0,
        hasMore: false,
        annotations: [],
        orders: [],
        trades: [],
        closedLots: [],
        position: { quantity: 0, side: 'flat', averagePrice: 0 },
        realizedPnl: 0,
        unrealizedPnl: 0,
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
      clearError()
      if (list.length === 0) noteNotice(`${res.code} 在 ${res.day} 没有 K 线（休市或那一天还没抓）`)
    } catch (e) {
      noteError(`K 线读取失败：${String(e instanceof Error ? e.message : e)}`)
    } finally {
      loading.value = false
    }
  }

  function setSymbol(code: string): void {
    if (code && code !== symbolCode.value) void load(code)
  }

  function setDay(date: string): void {
    if (date && date !== day.value) void load(symbolCode.value, date)
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

  // ---------------- 交易动作：一律「未接通」 ----------------
  function placeOrder(side: Side, kind: OrderKind, price: number, quantity: number): void {
    void side
    void kind
    void price
    void quantity
    notWired('挂单')
  }

  function marketOrder(side: Side, quantity: number, stopDistance = 0): void {
    void side
    void quantity
    void stopDistance
    notWired('市价下单')
  }

  function cancelOrder(orderId: string): void {
    void orderId
    notWired('撤单')
  }

  function moveOrder(orderId: string, price: number): void {
    void orderId
    void price
    notWired('改挂单价')
  }

  function flatAll(): void {
    notWired('一键平仓')
  }

  /** 键盘 →（ChartPanel 里绑着）：这里没有「逐根揭示」，只提示一句不再多出 K 线。 */
  function step(): void {
    noteNotice('这一天的 K 线已经全部画出：逐根推进要等交易后端接通。')
  }

  // 换周期不需要请求，聚合是 computed 现算的；这里保留 watch 只为清掉「上一份」的选中态。
  watch(timeframe, () => selectAnnotation(null))

  return {
    // 数据
    symbols,
    instance,
    symbolCode,
    day,
    loading,
    bars,
    cursor,
    currentBar,
    displayBars,
    timeframe,
    setTimeframe,
    loadSymbols,
    load,
    setSymbol,
    setDay,
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
    // 交易（未接通）
    placeOrder,
    marketOrder,
    cancelOrder,
    moveOrder,
    flatAll,
    step,
  }
})
