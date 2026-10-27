import { defineStore } from 'pinia'
import { computed, ref, watch } from 'vue'

import { atrSeries, DEFAULT_ATR, sanitizeAtr, type AtrSettings } from '@/components/chart/atr'
import { DEFAULT_BAR_COUNT, sanitizeBarCount, type BarCountSettings } from '@/components/chart/barcount'
import { DEFAULT_INDICATOR, sanitizeIndicator, type IndicatorSettings } from '@/components/chart/indicators'
import { DEFAULT_NATR, planNatr, sanitizeNatr, type NatrSettings } from '@/lib/natr'
import { ReplaySocket, defaultSocketUrl } from '@/net/socket'
import { applyInstanceEvent } from '@/stores/instance-reducer'
import { ACTION } from '@/types/protocol'
import type {
  AnnotationKind,
  AnnotationDeletedPayload,
  AudioClip,
  AnnotationStyle,
  Bar,
  ClientMessage,
  ControlChangedPayload,
  InstanceDetail,
  InstanceStateMessage,
  InstanceSummary,
  OrderKind,
  Point,
  RecordTag,
  Recording,
  RecordingExtendedMessage,
  RecordingSummary,
  RecordingVoiceMessage,
  ReplayEvent,
  ServerEvent,
  ServerMessage,
  Side,
  StrategyInfo,
  SymbolInfo,
  Template,
  Timeframe,
  TimeframeBars,
  VoiceAddedPayload,
  VoiceClearedPayload,
  VoiceState,
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

const MAX_LOG = 200
/** 指标的开关与参数记在本机：它是显示偏好，换实例、重启程序都不该被重置 */
const INDICATOR_KEY = 'tv-replay-doji-zigzag'
/** ATR 的开关与参数（Length / Smoothing）也记在本机，理由同上 */
const ATR_KEY = 'tv-replay-atr'
/** Bar Count 的开关与颜色（纯显示偏好：换机器就该回到默认蓝，所以只记本机） */
const BAR_COUNT_KEY = 'tv-replay-bar-count'
/** nATR 定手数的两个数（n、最大亏损）：做完一次设置很久不会动，值得记在本机 */
const NATR_KEY = 'tv-replay-natr'

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

/**
 * 读回上次的 ATR 设置（v0.23.0 起 ATR 有了 Length / Smoothing，值得记住）。
 *
 * 和 `readIndicator` 同一条路：整份 JSON 存、读回来先 sanitize。
 * 第一次运行时键不存在（v0.22.0 那会儿 ATR 只有一个「显示」开关、压根没落过盘），
 * 于是直接回出厂值 14 / RMA —— 和 TradingView 刚把指标加到图上时一致。
 */
function readAtr(): AtrSettings {
  try {
    const raw = localStorage.getItem(ATR_KEY)
    if (raw) {
      const saved = JSON.parse(raw) as Partial<AtrSettings>
      return sanitizeAtr({ ...DEFAULT_ATR, ...saved })
    }
  } catch {
    // 隐私模式 / 存的是坏 JSON：回到出厂值，不影响本次使用
  }
  return { ...DEFAULT_ATR }
}

/**
 * 读回上次的 Bar Count 设置（开关 + 颜色）。
 *
 * 和上面两个同一个套路：整份 JSON 存、读回来先 sanitize。
 * 键不存在（第一次用这个版本）时直接回出厂值 —— 默认是**开着**的，
 * 因为这个指标几乎不占地方，而它最大的价值恰恰是「不用每次都去打开」。
 */
function readBarCount(): BarCountSettings {
  try {
    const raw = localStorage.getItem(BAR_COUNT_KEY)
    if (raw) {
      const saved = JSON.parse(raw) as Partial<BarCountSettings>
      return sanitizeBarCount({ ...DEFAULT_BAR_COUNT, ...saved })
    }
  } catch {
    // 隐私模式 / 存的是坏 JSON：回到出厂值，不影响本次使用
  }
  return { ...DEFAULT_BAR_COUNT }
}

/**
 * 读回上次的 nATR 设置（n 与最大亏损）。
 *
 * 和上面三个同一个套路：整份 JSON 存、读回来先 sanitize。
 * 第一次运行没有这个键，回出厂值（1 倍 ATR / $200）—— 面板上立刻有数可算。
 */
function readNatr(): NatrSettings {
  try {
    const raw = localStorage.getItem(NATR_KEY)
    if (raw) {
      const saved = JSON.parse(raw) as Partial<NatrSettings>
      return sanitizeNatr({ ...DEFAULT_NATR, ...saved })
    }
  } catch {
    // 隐私模式 / 存的是坏 JSON：回到出厂值，不影响本次使用
  }
  return { ...DEFAULT_NATR }
}

export const useReplayStore = defineStore('replay', () => {  const socket = new ReplaySocket(defaultSocketUrl())

  const status = ref<'connecting' | 'open' | 'closed'>('closed')
  /**
   * 回放模式。开启时 `instance` 被换成「交易记录演出来的那一份」，
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
  /** 一句会自己消失的轻提示（交易记录已保存 / 语音已挂载）。 */
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
   * 正在编辑的那个图形（**对象本身**，不是 id）。
   *
   * 编辑态的模板条要拿它的 `kind` 去筛同类模板、拿它的 `style` 去高亮「当前是哪一条」。
   * 不在图上的选中（被删掉、锚点落在未揭示区）返回 null —— 那种情况下没有可改的东西。
   */
  const editingAnnotation = computed(() => {
    const id = editingId.value
    if (!id) return null
    return (instance.value?.annotations ?? []).find((a) => a.id === id) ?? null
  })

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
  /** 已保存的交易记录列表（后端扫记录目录得到的，新→旧） */
  const recordings = ref<RecordingSummary[]>([])
  /** 记录目录的绝对路径，显示在交易记录 Tab 上，方便用户去文件夹里找 */
  const recordingDir = ref('')
  /** 刚取回来、等着交给播放器的那一条记录 */
  const loadedRecording = ref<Recording | null>(null)
  /** 每取回一条就加一：组件用它判断"这是一条新的"，避免同一条被重复打开 */
  const loadedSeq = ref(0)
  /**
   * 回放延伸的两条回复（v0.27.0）：加载了 K 线 / 在延伸出来的 K 线上录了音。
   *
   * 为什么不直接改 `loadedRecording`：那一条的语义是「刚取回一整条记录，
   * 交给播放器打开」，重发一次会把回放**重置到第 1 步**。延伸只该在播放器
   * 手里那份 recording 上追加，播放进度要留在原处。
   *
   * 所以这里只做转发：播放器（stores/playback.ts）watch `playbackMsgSeq`，
   * 认 id 之后自己接上。和 `loadedSeq` 是同一套「信号 + 载荷」的写法。
   */
  const playbackMsg = ref<RecordingExtendedMessage | RecordingVoiceMessage | null>(null)
  const playbackMsgSeq = ref(0)
  /**
   * 延伸/录音**失败**的那一句（v0.27.0）。
   *
   * 必须单独转一次：播放器要据此把手里的「加载中」标记放掉，否则一次失败之后
   * 【加载下一根】会被永远锁住（按钮看着能点，其实一直转圈）。
   * 错误本身仍然由 `lastError` 弹出来，这条只负责「这件事结束了、结果是失败」。
   */
  const playbackFail = ref<{ request: string; message: string } | null>(null)
  const playbackFailSeq = ref(0)
  /**
   * 交易记录页点【回放】预约的那一条（v0.19.0）。
   *
   * 记录页没有画布，回放只能发生在 `/` 上：它把 id 放这儿再跳回去，
   * 复盘页取走之后立即清空 —— 留着它会在每次断线重连时把同一笔重复播一遍。
   */
  const pendingPlaybackId = ref('')
  /** 是否显示 EMA20（纯显示开关，跟后端无关） */
  const showEma = ref(true)
  /**
   * ATR 副图的开关与参数（v0.23.0）：Length / Smoothing，和 TradingView 的
   * 「Average true range」一模一样（RMA / SMA / EMA / WMA 四种平滑）。
   *
   * 默认**不显示**：副图会从价格区切走一条（约 18% 高度），第一次进复盘的人不该
   * 平白少看几十根 K 线；要看波动率就自己在【指标 ▾】里勾上。
   * 参数（不是开关）会记在 localStorage 里 —— 每个人的 14 / RMA 未必合用，
   * 调过一次就别每次重开都回到默认。
   */
  const atr = ref<AtrSettings>(readAtr())
  /**
   * Doji X + Blue ZigZag 的开关与两个参数。
   *
   * 和 EMA20 一样是纯显示状态：不发消息、不进存档、不影响任何人的盘面。
   * 它和「Zoom / 平移」属于同一类 —— 各人看各人的，别人不该看到我的指标被关掉。
   */
  const indicator = ref<IndicatorSettings>(readIndicator())
  /**
   * Bar Count（K 线下方的「当天第几根」）：开关 + 颜色。
   *
   * 编号口径不是「整份数据从 1 数到底」，而是**每张 CSV（每个交易日）重新从 1 数**、
   * 而且只标单数 —— 它是用来两根一组读节奏的（见 chart/barcount.ts）。
   * 和上面两个一样是纯显示状态：不进存档、不广播，别人看不到你的颜色。
   */
  const barCount = ref<BarCountSettings>(readBarCount())
  /**
   * nATR 定手数的两个数：几倍 ATR 当止损距离、最多亏多少美元。
   *
   * 它不是「显示参数」，但同样只存在本机：止损口径是每个人的交易习惯，
   * 进存档/广播出去就意味着「换个人接管，手数计算也跟着被改」，那是惊吓不是功能。
   * 真正下单仍然是 `placeOrder` / `marketOrder` 那两条消息，这里只决定填几手。
   */
  const natr = ref<NatrSettings>(readNatr())
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

  // ---------------- 周期切换（只影响画布，不影响盘面） ----------------

  /**
   * 当前看的 K 线周期。
   *
   * **只是画布的一种看法**，不是盘面的一部分：后端拿「已揭示的 5 分钟 K 线」现聚合
   * 一份返回来（`GET /api/timeframe`），不落盘、不进交易记录、也不广播给别人 ——
   * 所以它只活在这个标签页的内存里（刷新就回到 5 分钟）。
   *
   * 规矩：`bars` 永远是**5 分钟原始序列**（游标、步进、下单手数、语音挂哪根，全按它算）；
   * 只有画布用 `displayBars`。混用两者就会出「走了一根但图没动」或者「语音挂错根」。
   */
  const timeframe = ref<Timeframe>('5m')
  /** 后端聚合回来的那一份；5 分钟时不用请求（就是 `bars` 本身） */
  const tfBars = ref<Bar[]>([])
  /** `tfBars` 属于哪个实例（换实例时用来判断要不要先清空） */
  let tfOwnerID = ''
  /** 画布真正画的那一份 */
  const displayBars = computed(() => {
    // 回放里的 K 线是录像自己的（`instance.bars` 已被换成录像的 base），
    // 服务端没有这份盘面可以聚合 —— 回放一律按原始周期。
    if (timeframe.value === '5m' || playbackActive.value) return bars.value
    return tfBars.value
  })

  /**
   * 拉一次聚合数据。
   *
   * 不做缓存也不做增量：一次请求就是把几百根已揭示的 5 分钟 K 线按桶过一遍，
   * 比一次重绘还便宜。每走一根、换实例、换周期都会重来一次。
   */
  async function loadTimeframe(): Promise<void> {
    const id = instance.value?.id
    const tf = timeframe.value
    if (!id || tf === '5m' || playbackActive.value) {
      tfBars.value = []
      tfOwnerID = ''
      return
    }
    // 换了实例就先清空：否则在响应回来之前，画布会拿上一个标的的聚合 K 线顶一两帧
    if (tfOwnerID !== id) {
      tfBars.value = []
      tfOwnerID = id
    }
    try {
      const res = await fetch(
        `/api/timeframe?instance=${encodeURIComponent(id)}&timeframe=${encodeURIComponent(tf)}`,
        { cache: 'no-store' },
      )
      if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`)
      const data = (await res.json()) as TimeframeBars
      // 迟到的响应：人已经换了实例/周期，这一份不能再盖上去
      if (timeframe.value !== tf || instance.value?.id !== id) return
      tfBars.value = data.bars ?? []
    } catch (e) {
      if (timeframe.value !== tf) return
      // 聚合失败就老实退回 5 分钟：一张空图比「回不去」更难懂
      timeframe.value = '5m'
      noteNotice(`切到 ${tf} 失败（${String(e)}），已回到 5 分钟`)
    }
  }

  function setTimeframe(tf: Timeframe): void {
    timeframe.value = tf
  }

  // 换周期、换实例、多走一根 —— 三种情况都要重算（值没变时 watch 不会触发）
  watch([timeframe, () => instance.value?.id, () => cursor.value, () => bars.value.length], loadTimeframe)
  const isOperator = computed(() => you.value?.operator === true)
  /**
   * 语音讲解（v0.16.0）。**和交易记录无关**：空仓也有一份，空的时候就是空清单。
   *
   * 事实来源是 instance.voice（快照 + bar_advanced + voice_added / voice_cleared
   * 三路一起更新）。v0.18.0 删掉了「录制状态」那一份 —— 交易记录是自动的，
   * 界面上不该再有「正在录制」这个概念。
   */
  const EMPTY_VOICE: VoiceState = { barTime: '', clips: [], total: 0, max: 9 }
  const voice = computed<VoiceState>(() => instance.value?.voice ?? EMPTY_VOICE)
  const voiceClips = computed(() => voice.value.clips ?? [])
  /** 整个实例的语音清单（别的 K 线上还有几条，见 InstanceDetail.voiceAll） */
  const voiceAll = computed<AudioClip[]>(() => instance.value?.voiceAll ?? [])
  /** 不在当前这根上的语音（按时间排好）：复盘不能倒回，这些也得能点开听 */
  const voiceElsewhere = computed(() =>
    voiceAll.value.filter((c) => c.time !== voice.value.barTime).sort((a, b) => (a.time < b.time ? -1 : 1)),
  )
  const voiceMax = computed(() => voice.value.max || 9)
  const voiceFull = computed(() => voiceClips.value.length >= voiceMax.value)
  /**
   * 能不能录。和绘图同一条口径：**要操作权、不能在回放里**。
   *
   * 入口（打开浮窗）对所有人开放 —— 观众也得能听讲解；只有「往上加一段」这个
   * 动作需要操作权（服务端也会再挡一次）。
   */
  const canVoice = computed(() => !playbackActive.value && canOperate.value && bars.value.length > 0)
  /** 语音浮窗开着没有。关掉 = 放下麦克风，空格回到普通含义。 */
  const voiceOpen = ref(false)
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
  /**
   * 每点值多少美元 = `tickValue / tick`（MNQ: 0.5 / 0.25 = 2）。
   *
   * 和后端 `pointValueOf` 同一口径：用户只在 `_symbols.json` 里填一跳值多少美元，
   * 「每点」是除出来的。查不到（老存档 / 没配 tickValue）时按每点 $1 兜底 ——
   * 后端结算也是这么兜的，两边不能有一处兜得不一样。
   */
  const pointValue = computed(() => {
    const t = tick.value
    const tv = instance.value?.tickValue ?? 0
    if (t > 0 && tv > 0) return tv / t
    return 1
  })
  /**
   * 当前这根 K 线的 ATR（nATR 定手数要用）。
   *
   * 用的是**同一个** `store.atr` 的 Length / Smoothing —— 用户在图上看的是哪条 ATR，
   * 算手数用的就是哪条；副图没开也照样算（开关只管画不画，不管参数）。
   * 序列还没算出第一个值时返回 null：ATR 要到第 `length` 根才出值，
   * 这时候面板上应该写「ATR 还没出来」，而不是拿 0 去除。
   */
  const atrValue = computed<number | null>(() => {
    const list = bars.value
    if (list.length === 0) return null
    const series = atrSeries(list, atr.value.length, atr.value.smoothing)
    const v = series[series.length - 1]
    return Number.isFinite(v) ? v : null
  })
  /**
   * 一次 nATR 定手数的完整结果（止损距离 / 每手亏损 / 手数 / 实际亏损）。
   *
   * 算在 store 里而不是组件里：它是**盘面 + 本机设置**的派生物，将来右键菜单、
   * 机器人参数页想用同一个数，不该各自再乘一遍。ATR 没出来时是 null。
   */
  const natrPlan = computed(() => {
    const a = atrValue.value
    if (a === null) return null
    return planNatr(a, natr.value, pointValue.value)
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
        if (msg.request && msg.request.startsWith('recording_')) {
          playbackFail.value = { request: msg.request, message: msg.message }
          playbackFailSeq.value += 1
        }
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
      case 'trade_recorded': {
        // 一笔交易刚刚平完、记录已落盘（v0.18.0）。它是一条**一次性**消息，
        // 不是广播：不需要进操作记录，界面上也没有「正在记录」的状态要收尾。
        const sum = msg.recording
        listRecordings() // 列表里立刻能看见这一笔，不用用户手动刷新
        if (msg.warning) {
          noteError(`这笔交易记完了，但记录没落盘：${msg.warning}`)
          break
        }
        const s = sum.stats
        const money = `${s.realizedPnl >= 0 ? '+' : ''}${s.realizedPnl.toFixed(2)}`
        noteNotice(
          `交易记录已保存：${sum.name}（${sum.stepCount} 步 / ${sum.bars} 根 · ${money}${
            s.closed ? '' : ' · 还没平仓'
          }）`,
        )
        break
      }
      case 'recording':
        // 一条记录的完整内容到了：交给播放器（组件的 watch 会接住）
        loadedRecording.value = msg.recording ?? null
        loadedSeq.value += 1
        break
      case 'recording_extended':
      case 'recording_voice': {
        // 回放延伸的增量（v0.27.0）：列表那一行顺手更新（步数/根数/结束时间都变了），
        // 然后整条消息转给播放器接上 —— 这里不动 loadedRecording（那会重置回放进度）。
        applyRecordingSummary(msg.recording)
        playbackMsg.value = msg
        playbackMsgSeq.value += 1
        break
      }
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

  /**
   * 按 id 就地更新交易记录列表里的一行（延伸 / 录音之后摘要变了）。
   *
   * 不整份重发列表：那两个动作本来就是一问一答，重拉一次列表还会让人在记录页
   * 看到列表闪一下。找不到就什么都不做（比如另一条记录的消息，或列表还没到）。
   */
  function applyRecordingSummary(sum: RecordingSummary | undefined): void {
    if (!sum?.id) return
    const i = recordings.value.findIndex((r) => r.id === sum.id)
    if (i >= 0) recordings.value[i] = { ...recordings.value[i], ...sum }
  }

  function applyState(msg: InstanceStateMessage): void {
    const inst = msg.instance
    // 换了一块盘面（新建 / 进入另一个实例）：上一条错误属于上一条实例，跟着它作废。
    // 不这么做的话，删掉旧实例时留下的那句话会一直贴在右下角、还会被录制浮层
    // 抄进「提示」里 —— 新建的实例明明好好的，界面却在喊它已经被删了。
    if (instance.value?.id !== inst.id) {
      clearError()
      // 换了盘面：语音浮窗收起（麦克风是「对着这块盘面讲」的，不该跟到下一块上）
      voiceOpen.value = false
    }
    // 快照里的 bars 只含已揭示的部分（后端根本不会发未来的 K 线）
    instance.value = {
      ...inst,
      bars: inst.bars ?? [],
      // 语音永远是一个对象（后端保证不为 null；这里再兜一层，界面就不用到处判空）
      voice: inst.voice ?? EMPTY_VOICE,
      voiceAll: inst.voiceAll ?? [],
    }
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

    // 语音讲解（v0.16.0）：盘面数据，但**不进交易记录**（交易期间那一条已经由后端
    // 记成记录里的一个语音步），所以也不交给 reducer —— 回放时不该凭空多出语音。
    if (msg.action === ACTION.voiceAdded || msg.action === ACTION.voiceCleared) {
      applyVoiceEvent(inst, msg)
      return
    }

    // 盘面变化：和回放共用同一份更新逻辑（stores/instance-reducer.ts）
    applyInstanceEvent(inst, msg.action, msg.payload)

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

  /**
   * 语音讲解的两条广播（v0.16.0）。
   *
   * 只更新盘面那一份：语音是盘面数据，和有没有正在进行的一笔交易记录无关。
   * 交易期间录的那条在记录里另占一步 —— 那是后端的事，前端不需要知道，
   * 也不该因此多维护一个「录制状态」。
   */
  function applyVoiceEvent(inst: InstanceDetail, msg: ServerEvent): void {
    if (msg.action === ACTION.voiceAdded) {
      const p = msg.payload as VoiceAddedPayload
      inst.voice = p.voice
      inst.voiceAll = p.voiceAll ?? inst.voiceAll
      noteNotice(
        `语音已挂到 ${p.audio.time}（本根 ${p.voice.clips.length}/${p.voice.max} · ${(
          p.audio.durationMs / 1000
        ).toFixed(1)} 秒）`,
      )
      return
    }
    const p = msg.payload as VoiceClearedPayload
    inst.voice = p.voice
    inst.voiceAll = p.voiceAll ?? inst.voiceAll
    noteNotice(`已清除这根 K 线上的 ${p.removed.length} 条语音`)
  }

  /** 断线重连后重新对齐：实例和列表都可能已经变了。 */
  function resync(): void {
    send({ type: 'list_symbols' })
    send({ type: 'list_instances' })
    send({ type: 'list_templates' })
    send({ type: 'list_recordings' })
    // 挂着一条「等下要回放」的记录：断线期间那次 get_recording 已经丢了，这里补一次
    if (pendingPlaybackId.value) {
      send({ type: 'get_recording', recordingId: pendingPlaybackId.value })
    }
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

  /**
   * 改 ATR 设置（v0.23.0）：开关 / Length / Smoothing。
   *
   * 和 `setIndicator` 同一套写法 —— 整份换新对象（画布 watch 的是 `store.atr`，
   * 原地改字段不会触发重画），夹值走 `sanitizeAtr`，改完就落 localStorage。
   */
  function setAtr(patch: Partial<AtrSettings>): void {
    atr.value = sanitizeAtr({ ...atr.value, ...patch })
    try {
      localStorage.setItem(ATR_KEY, JSON.stringify(atr.value))
    } catch {
      // 记不住就算了，不影响本次使用
    }
  }

  /**
   * 改 Bar Count 设置：开关 / 颜色。
   *
   * 和 `setAtr` 同一套写法：整份换新对象（画布 watch 的是 `store.barCount`）+
   * `sanitizeBarCount` 夹值（颜色只认 #rgb / #rrggbb）+ 落 localStorage。
   */
  function setBarCount(patch: Partial<BarCountSettings>): void {
    barCount.value = sanitizeBarCount({ ...barCount.value, ...patch })
    try {
      localStorage.setItem(BAR_COUNT_KEY, JSON.stringify(barCount.value))
    } catch {
      // 记不住就算了，不影响本次使用
    }
  }

  /**
   * 改 nATR 的两个数（n / 最大亏损）。
   *
   * 和上面几个同一个写法：整份换新对象（computed `natrPlan` 才会重算）、
   * 夹值走 `sanitizeNatr`、落 localStorage。
   */
  function setNatr(patch: Partial<NatrSettings>): void {
    natr.value = sanitizeNatr({ ...natr.value, ...patch })
    try {
      localStorage.setItem(NATR_KEY, JSON.stringify(natr.value))
    } catch {
      // 记不住就算了，不影响本次使用
    }
  }

  function refresh(): void {
    send({ type: 'list_symbols' })
    send({ type: 'list_instances' })
  }
  /**
   * 新建实例。
   *
   * `latest = true` 走【新增实例（最新）】那条路：锚点由后端取「数据目录里最新的那一根」，
   * `session = true` 走【新增实例（开盘）】：锚点取「随机一个交易日的开盘首根」。
   * 两种模式下 `time` 都会被忽略。**不在前端算这个时间** —— 文件名只到「哪一天」，
   * 最后一根在几点（那天可能提前收市）、开盘是 6 点还是 7 点（美国夏令时）都只有
   * 读 CSV 才知道，拼一个猜的时刻只会得到「这个时间点不在 CSV 里」。
   */
  function createInstance(symbol: string, time: string, latest = false, session = false): void {
    send({ type: 'create_instance', symbol, time, latest, session, bars: 400, forwardDays: 5 })
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

  /**
   * 【跳转到下一天开盘】：让后端一路走到下一个交易日的开盘首根。
   *
   * 不是前端连点 step：一整天两百多根，来回两百次网络往返既慢又会刷屏。
   * 后端走完会补一份完整状态下来（那一夜多出来的 K 线跟着一起来），所以这里只管发请求。
   */
  function nextDayOpen(): void {
    if (!instance.value) return
    send({ type: 'next_day_open', instanceId: instance.value.id })
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
   * 把某个**已经画好的图形**换成另一条模板的样式（v0.20.0）。
   *
   * 和 `annotation_move` 分开：那条只挪位置（后端明确说过「样式不跟着移动变」），
   * 这一条只换样式、不动端点。两者合起来会让「拖一下线」也可能被当成改样式，
   * 而用户拖的时候根本没想改颜色。
   *
   * 后端拿图形自己的 kind 去 `sanitizeStyle`，所以「只在同类图形之间切换」是**服务端**
   * 也守着的约束，不只是界面上只列同类模板。
   */
  function restyleAnnotation(id: string, style: AnnotationStyle): void {
    if (!instance.value) return
    send({ type: 'annotation_style', instanceId: instance.value.id, annotationId: id, style })
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
   *
   * `stopDistance` > 0 时（nATR 定手数按下的那对按钮）后端会在**同一次动作**里
   * 再挂一张反方向的 stop 单，价格 = 成交价 ∓ 距离，也就是「最大亏损所在的那个价格」。
   * 前端只传距离、不传价格：成交价是后端 roundPrice(收盘价) 算的，拿这边的现价去减，
   * 差一跳就正好差在止损位上。合成一条消息也是为了不给别人看「有仓、没止损」的中间态。
   */
  function marketOrder(side: Side, quantity: number, stopDistance = 0): void {
    if (!instance.value) return
    const lots = Math.max(1, Math.round(quantity))
    const msg: ClientMessage = { type: 'order_market', instanceId: instance.value.id, side, quantity: lots }
    if (stopDistance > 0) msg.stopDistance = stopDistance
    send(msg)
  }

  function cancelOrder(orderId: string): void {
    if (!instance.value) return
    send({ type: 'order_cancel', instanceId: instance.value.id, orderId })
  }

  /**
   * 【市价退仓并取消所有挂单】（v0.20.0）：一键回到空仓、画布上没有一张挂着的单。
   *
   * 一条消息而不是「前端先撤单再反向市价」：那样会连发 N+1 条，别人的画布上一张张
   * 消失、仓位在中间还可能瞬间翻倍（撤到一半失败时）。服务端在一次锁里做完，
   * 只广播一条 `flat_all` —— 界面上看到的就是「一次性全平了」。
   *
   * 空仓 + 没有挂单时后端会回一句错误（不静默成功），所以按钮那边也要按同一个条件灰掉。
   */
  function flatAll(): void {
    if (!instance.value) return
    send({ type: 'order_flat_all', instanceId: instance.value.id })
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

  // ---------------- 交易记录 ----------------

  /**
   * 送一段语音讲解上去（WAV 的 base64）。挂载点是**服务端当前那根 K 线**，
   * 前端不传时间 —— 传了就有两种"当前"，一旦不一致语音会挂错柱子。
   *
   * v0.16.0 起语音就是盘面数据；v0.18.0 起交易记录自动开始，持仓期间录的那条
   * 由服务端顺手在记录里占一步（回放到那一步语音响），空仓时录的只挂在盘面上。
   */
  function sendVoiceAudio(audioData: string, durationMs: number): boolean {
    if (!instance.value || !canVoice.value) return false
    return send({ type: 'voice_add', instanceId: instance.value.id, audioData, durationMs })
  }

  /** 清空**当前这根 K 线**上的语音（录坏了重来，不用把整条录像作废）。 */
  function clearVoiceBar(): boolean {
    if (!instance.value || !canVoice.value) return false
    return send({ type: 'voice_clear', instanceId: instance.value.id })
  }

  /** 打开 / 收起语音浮窗。收起 = 放下麦克风（空格回到普通含义）。 */
  function toggleVoicePanel(): void {
    voiceOpen.value = !voiceOpen.value
  }

  function listRecordings(): void {
    send({ type: 'list_recordings' })
  }

  /** 请求一条录像的完整内容；回来之后由回放面板打开播放器。 */
  function requestRecording(id: string): void {
    send({ type: 'get_recording', recordingId: id })
  }

  /**
   * 回放走到末尾之后，往后加载一根（或几根）真实 K 线（v0.27.0）。
   *
   * 服务端会把它写进记录本身：所以这一步是**修改数据**，不只是「看看」——
   * 但改的只是「这条记录演到哪儿为止」，任何交易状态、任何原有的步都不动。
   */
  function extendRecording(id: string, count = 1): boolean {
    return send({ type: 'recording_extend', recordingId: id, count })
  }

  /** 在「回放末尾新加载的那根」上录一段语音（WAV 的 base64）。 */
  function addRecordingVoice(id: string, audioData: string, durationMs: number): boolean {
    return send({ type: 'recording_voice_add', recordingId: id, audioData, durationMs })
  }

  /** 清空「回放末尾新加载的那根」上的全部语音。 */
  function clearRecordingVoice(id: string): boolean {
    return send({ type: 'recording_voice_clear', recordingId: id })
  }

  /**
   * 「在画布上放这一条」—— 交易记录页专用的入口（v0.19.0）。
   *
   * 回放离不开 K 线画布，而记录页没有画布，所以点【回放】只是**预约**：
   * 记下 id、立刻请一次内容，然后由页面跳回 `/`。真正打开播放器的是复盘页
   * （它 watch `loadedSeq`），那边才知道画布准备好了没有。
   *
   * 为什么要留 pending：跳回去的那一刻 WebSocket 可能正在重连，这条
   * `get_recording` 会直接丢掉（没连上时 send 返回 false）。pending 记着这件事，
   * `resync()` 在连上之后补发一次，用户看到的就是「等一下自己就放起来了」。
   */
  function openRecordingAt(id: string): void {
    pendingPlaybackId.value = id
    requestRecording(id)
  }

  /** 取走并清空待回放的 id：复盘页接住之后不该再补发第二次。 */
  function takePendingPlayback(): string {
    const id = pendingPlaybackId.value
    pendingPlaybackId.value = ''
    return id
  }

  function deleteRecording(id: string): void {
    if (pendingPlaybackId.value === id) pendingPlaybackId.value = ''
    send({ type: 'delete_recording', recordingId: id })
  }

  /**
   * 整份替换一笔交易的标签（v0.21.0）。
   *
   * 前端**不做乐观更新**：标签存在服务端的 tags.json 里，回来的那份 `recordings`
   * 列表才是事实。这样「点一下、看起来加上了、其实没存住」不会发生 ——
   * 而标签恰恰是那种「下次打开就没了」最让人恼火的东西。
   *
   * 发的是「这笔交易之后该有的那一组」而不是 add/remove 两条：服务端只有一条
   * 写路径，两个标签页同时改也不会各留一半。
   */
  function setRecordingTags(id: string, tags: RecordTag[]): void {
    send({ type: 'set_recording_tags', recordingId: id, tags })
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
    playbackMsg,
    playbackMsgSeq,
    playbackFail,
    playbackFailSeq,
    voice,
    voiceClips,
    voiceAll,
    voiceElsewhere,
    voiceMax,
    voiceFull,
    canVoice,
    voiceOpen,
    toggleVoicePanel,
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
    editingAnnotation,
    templates,
    activeTemplate,
    showEma,
    atr,
    setAtr,
    indicator,
    setIndicator,
    barCount,
    setBarCount,
    natr,
    setNatr,
    orderQuantity,
    tick,
    priceDecimals,
    pointValue,
    // 派生
    bars,
    cursor,
    currentBar,
    timeframe,
    setTimeframe,
    displayBars,
    atrValue,
    natrPlan,
    isOperator,
    botRunning,
    botStrategyLabel,
    canOperate,
    operatorLabel,
    strategies,
    openOrders,
    // 动作
    connect,
    refresh,
    createInstance,
    joinInstance,
    leaveInstance,
    deleteInstance,
    step,
    nextDayOpen,
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
    restyleAnnotation,
    clearAnnotations,
    placeOrder,
    marketOrder,
    moveOrder,
    cancelOrder,
    flatAll,
    startBot,
    stopBot,
    listStrategies,
    clearError,
    noteNotice,
    clearNotice,
    sendVoiceAudio,
    clearVoiceBar,
    listRecordings,
    requestRecording,
    extendRecording,
    addRecordingVoice,
    clearRecordingVoice,
    openRecordingAt,
    takePendingPlayback,
    deleteRecording,
    setRecordingTags,
    clearLoadedRecording,
  }
})
