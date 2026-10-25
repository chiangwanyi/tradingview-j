<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { atrLegend } from '@/components/chart/atr'
import { useReplayStore } from '@/stores/replay'
import type { OrderKind, Side } from '@/types/protocol'

/**
 * 下单悬浮窗：从底部停靠区里**单独提出来**的那一列「下单」，浮在页面上。
 *
 * 为什么搬出来：复盘时真正反复做的是「看一眼图 → 挂一张单 / 市价反手」，
 * 而底部面板要么占掉 200px 的画布高度、要么收起来以后每次下单都要先展开再找输入框。
 * 浮窗把这几个输入框钉在图上：位置自己拖、不挡图就折叠成一个 44px 的小图标。
 *
 * 三条设计约束（都为了「随手用」）：
 *
 *  1. **位置只有一份**（`pos`），展开态和折叠态共用同一个左上角。
 *     所以「拖到角落 → 折叠 → 再展开」不会跑到别的地方去；反过来拖动小图标，
 *     展开时面板就从那个角落长出来。位置存 localStorage，刷新页面还在原处。
 *  2. **拖动只发生在标题栏 / 小图标上**，`pointerdown` 里 `preventDefault` ——
 *     否则一按就开始框选文字，输入框还会抢焦点，手感像在拖一块烂泥。
 *     真的移动超过 5px 才算拖动：折叠态下「按下 → 松手」是**单击展开**，不是拖动。
 *  3. **永远留在视口里**：每次移动/展开/窗口缩放都夹一次位置。半截露在屏幕外的浮窗
 *     是找不回来的（尤其是缩到最小、只剩一个图标的时候）。
 *
 * 和底部面板的分工：这里只有「下单」（手填价格的限价/止损 + 两个市价按钮 +
 * nATR 以损定仓 + 一键退仓），持仓、挂单列表、成交流水还在底部 Tab 里 ——
 * 那些是「看」的，不需要随时点。
 * 一键退仓（v0.20.0）放在这里而不是底部的挂单列表里：它是「现在就收摊」的动作，
 * 和下单是同一件事的两头，手边要够得着。
 *
 * 价格/方向/单型是**这个组件自己的状态**（和以前底部那一列一样）：换实例时回到现价，
 * 行情往前走而用户没手改过就跟着现价走。数量用 `store.orderQuantity`，因为它同时被
 * 右键菜单用着，只能是同一个数。
 */

const store = useReplayStore()

/** 展开态宽度。锚点（左上角）是固定的，宽度只影响右侧夹边界的计算。 */
const PANEL_W = 276
/** 折叠态图标边长 */
const ICON = 44
/** 量不到实际高度时的估算值（首帧还没渲染，`offsetHeight` 是 0） */
const EST_H = 440
const MARGIN = 8

const POS_KEY = 'tv-trade-float-pos'

const expanded = ref(true)
/**
 * 面板左上角（视口像素）。**展开态和折叠态共用这一份**：折叠只是把盒子缩小，
 * 锚点不动，所以「拖到角落 → 折叠 → 展开」不会跑到别处去。
 *
 * 初值在 setup 里就算好（不是等 onMounted）：否则第一帧会画在 (0,0)、
 * 下一帧才跳到目标位置，看着像闪了一下。
 *
 * 首次出现的默认位置是**左上**：右上/右边要留给价格轴 —— 刻度、挂单线标签、
 * 平仓线的信息卡都长在那儿，盖住它最亏。
 */
const pos = ref(readSavedPos() ?? { x: MARGIN + 8, y: 112 })
const root = ref<HTMLElement | null>(null)

const inst = computed(() => store.instance)
const currentPrice = computed(() => store.currentBar?.close ?? 0)
const priceText = computed(() =>
  currentPrice.value ? currentPrice.value.toFixed(store.priceDecimals) : '—',
)

const side = ref<Side>('buy')
const kind = ref<OrderKind>('limit')
const price = ref<number>(0)
const manualPrice = ref(false)

/** 跳动用交易的 tick（没有就按两位小数） */
const step = computed(() => {
  const t = store.tick
  return t > 0 ? String(t) : '0.01'
})

// 换实例、或者行情往前走而用户没手动改过价格时，价格框跟着现价
watch(
  () => inst.value?.id,
  () => {
    manualPrice.value = false
    price.value = round2(currentPrice.value)
  },
  { immediate: true },
)
watch(currentPrice, (v) => {
  if (!manualPrice.value && v > 0) price.value = round2(v)
})

function round2(v: number): number {
  return Math.round(v * 100) / 100
}

function useCurrentPrice(): void {
  manualPrice.value = false
  price.value = round2(currentPrice.value)
}

function submit(): void {
  if (!inst.value || !store.canOperate) return
  if (!(price.value > 0) || !(store.orderQuantity > 0)) return
  store.placeOrder(side.value, kind.value, price.value, store.orderQuantity)
}

/**
 * 市价成交：价格由后端取当前 K 线收盘价，所以这里不校验、也不传上面那个价格框 ——
 * 用户填的限价还留在框里，切回挂单时不会丢。方向是**参数**，不跟上面的下拉走。
 */
function submitMarket(dir: Side): void {
  if (!inst.value || !store.canOperate) return
  if (!(currentPrice.value > 0) || !(store.orderQuantity > 0)) return
  store.marketOrder(dir, store.orderQuantity)
}

// ---------------- nATR 定手数（以损定仓） ----------------

/**
 * 【nATR 定手数】：先定「最多亏多少」，再倒推「下几手」。
 *
 *     nATR 距离 = n × ATR        （例：10 × 19.24 = 192.4 点）
 *     每手亏     = 距离 × 每点价值（MNQ 每点 $2 → $384.80）
 *     手数       = 四舍五入(最大亏损 / 每手亏)（$2000 → 5.197 → 5 手）
 *
 * 算式只有一份，在 `lib/natr.ts`；这里只负责显示和发两条市价单。
 * 用**当前这根 K 线的 ATR**（和【指标 ▾】里那套 Length / Smoothing 同一份），
 * 所以盘中 ATR 变了，手数会跟着变 —— 这正好是「以损定仓」的意思。
 *
 * 为什么是四舍五入而不是向下取整：用户定的是「最多亏这么多」，不是「尽量少亏」。
 * 最接近上限的整数手就是让实际亏损最贴近上限的那个；允许略微超出（需求里
 * 5 手亏 2100 优于 4 手亏 1800 就是这个口径）。只有「1 手都亏不起」时才会出现
 * 实际亏损超过上限，那时数字标红，让用户自己判断要不要做。
 */
const plan = computed(() => store.natrPlan)
const natrLots = computed(() => plan.value?.lots ?? 0)
const atrText = computed(() =>
  store.atrValue === null ? '—' : store.atrValue.toFixed(store.priceDecimals),
)

/** 美元金额：千分位 + 两位小数（$1,924.00）。手数换算出来的钱经常上四位数，不加分隔符读不动。 */
function fmtUsd(v: number): string {
  return `$${v.toLocaleString('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })}`
}

/** 点数：跟着标的的小数位走（MNQ 两位、CL 两位），不四舍五入到整数档。 */
function fmtPoints(v: number): string {
  return v.toFixed(Math.max(2, store.priceDecimals))
}

/**
 * 两个输入框用 `change` 而不是 `input`：输到一半的「1」不该立刻被夹成 1，
 * 等回车 / 失焦再一次性生效（和【指标 ▾】里那几个参数同一个手感）。
 * 生效后把夹过的真实值写回 DOM —— 否则框里会留着用户敲的那个越界值。
 */
function onNatrN(e: Event): void {
  const el = e.target as HTMLInputElement
  store.setNatr({ n: Number(el.value) })
  el.value = String(store.natr.n)
}

function onNatrMaxLoss(e: Event): void {
  const el = e.target as HTMLInputElement
  store.setNatr({ maxLoss: Number(el.value) })
  el.value = String(store.natr.maxLoss)
}

/**
 * 按算出来的手数市价成交。**方向是参数**、手数只来自 `plan` ——
 * 不读也不写上面的「数量」输入框：那一格是手填挂单用的，两个数混在一起
 * 就会出现「点了 nATR 买入，手数和输入框对不上」的困惑。
 */
function submitNatrMarket(dir: Side): void {
  if (!store.canOperate || !plan.value) return
  if (!(currentPrice.value > 0)) return
  store.marketOrder(dir, plan.value.lots)
}

const natrDisabled = computed(
  () => !store.canOperate || !inst.value || !plan.value || !(currentPrice.value > 0),
)

/** 一句话说清这次是怎么算出来的（鼠标停在按钮上就能核对，不用回翻文档）。 */
const natrCalc = computed(() => {
  const p = plan.value
  if (!p) return ''
  return `${store.natr.n}×ATR ${fmtPoints(p.stopDistance)} 点 · 每手亏 ${fmtUsd(p.riskPerLot)} · 理论 ${p.exact.toFixed(2)} 手 → ${p.lots} 手（实际 ${fmtUsd(p.actualLoss)}）`
})

const natrButtonTitle = computed(() => {
  const p = plan.value
  if (!p) return 'ATR 还没算出来：K 线不足 Length 根，先往前多揭示几根'
  return `${natrCalc.value}；下单价按现价 ${priceText.value}，止损要你自己挂在 ${store.natr.n}×ATR 之外`
})

// ---------------- 市价退仓并取消所有挂单（v0.20.0） ----------------

/** 仓位手数（绝对值）：平仓手数由**仓位**决定，和上面那个「数量」输入框无关 */
const positionQty = computed(() => Math.abs(inst.value?.position.quantity ?? 0))
const flatSide = computed(() => (inst.value?.position.quantity ?? 0) < 0 ? '空' : '多')

/**
 * 按钮能不能点：有仓位或有挂单，二者之一就值得按。
 *
 * 空仓 + 零挂单时灰掉，理由写在 title 里 —— 让用户知道自己不是点坏了，
 * 而不是点一下等后端回一句错误（那种「点了没反应」最费解）。
 */
const flatDisabled = computed(
  () => !store.canOperate || !inst.value || (positionQty.value === 0 && store.openOrders.length === 0),
)

const flatTitle = computed(() => {
  if (!inst.value) return '先进入一个实例'
  if (!store.canOperate) return '现在只能看：先「接管」操作权再退仓'
  if (positionQty.value === 0 && store.openOrders.length === 0) return '已经空仓、也没有挂单，没什么可退的'
  const parts: string[] = []
  if (positionQty.value > 0) parts.push(`按现价平掉 ${flatSide.value} 头 ${positionQty.value} 手`)
  if (store.openOrders.length > 0) parts.push(`撤销全部 ${store.openOrders.length} 张挂单`)
  return `${parts.join('，')}（一条消息发出去，别人看到的就是一次性完成）`
})

/**
 * 一键退仓。
 *
 * 提示语在**发出去之前**按当前盘面拼好：这条动作会把仓位和挂单都清掉，
 * 等广播回来之后再读 instance，已经数不出「平了几手、撤了几张」了。
 * 真正的执行仍然在后端（见 internal/replay/instance.go 的 flatAll），这里不本地改状态。
 */
function flatAll(): void {
  if (flatDisabled.value) return
  const qty = positionQty.value
  const n = store.openOrders.length
  store.flatAll()
  const parts: string[] = []
  if (qty > 0) parts.push(`市价退仓 ${qty} 手`)
  if (n > 0) parts.push(`撤销 ${n} 张挂单`)
  store.noteNotice(`已发出：${parts.join('，')}`)
}

// ---------------- 位置：拖动、折叠、夹在视口里 ----------------

function clampPos(p: { x: number; y: number }): { x: number; y: number } {
  // 展开态用实测宽高：内容会随「有没有实例 / 小数位」微变，硬编码迟早夹不准
  const w = root.value?.offsetWidth || (expanded.value ? PANEL_W : ICON)
  const h = root.value?.offsetHeight || (expanded.value ? EST_H : ICON)
  const maxX = Math.max(MARGIN, window.innerWidth - w - MARGIN)
  const maxY = Math.max(MARGIN, window.innerHeight - h - MARGIN)
  return {
    x: Math.min(Math.max(p.x, MARGIN), maxX),
    y: Math.min(Math.max(p.y, MARGIN), maxY),
  }
}

function readSavedPos(): { x: number; y: number } | null {
  try {
    const raw = localStorage.getItem(POS_KEY)
    if (!raw) return null
    const v = JSON.parse(raw) as { x?: unknown; y?: unknown }
    if (typeof v.x !== 'number' || typeof v.y !== 'number') return null
    if (!Number.isFinite(v.x) || !Number.isFinite(v.y)) return null
    return { x: v.x, y: v.y }
  } catch {
    return null
  }
}

function savePos(): void {
  try {
    localStorage.setItem(
      POS_KEY,
      JSON.stringify({ x: Math.round(pos.value.x), y: Math.round(pos.value.y) }),
    )
  } catch {
    // 隐私模式下 localStorage 会直接抛：记不住位置不影响这次使用
  }
}

/** 拖动中的基准：按下那一刻的指针位置 + 当时的面板位置 */
let dragFrom: { px: number; py: number; x: number; y: number } | null = null
/**
 * 这一次按下到底有没有真的移动。位移（|dx|+|dy|）不超过这么多像素就算**单击** ——
 * 折叠态只有「点一下展开」这一个动作，手抖 2px 不该被当成拖动。
 */
const DRAG_SLOP = 5
let dragged = false

function beginDrag(ev: PointerEvent): void {
  if (ev.button !== 0) return
  // 关键：不 preventDefault 的话，拖动过程会顺手选中面板里的文字、输入框还会抢焦点
  ev.preventDefault()
  ev.stopPropagation()
  dragFrom = { px: ev.clientX, py: ev.clientY, x: pos.value.x, y: pos.value.y }
  dragged = false
  window.addEventListener('pointermove', onDragMove)
  window.addEventListener('pointerup', onDragEnd)
  // 手指/触控笔被系统抢走时也要收尾，否则监听会留在 window 上，面板一直跟着指针跑
  window.addEventListener('pointercancel', onDragEnd)
}

function onDragMove(ev: PointerEvent): void {
  if (!dragFrom) return
  const dx = ev.clientX - dragFrom.px
  const dy = ev.clientY - dragFrom.py
  if (!dragged && Math.abs(dx) + Math.abs(dy) > DRAG_SLOP) dragged = true
  if (!dragged) return
  pos.value = clampPos({ x: dragFrom.x + dx, y: dragFrom.y + dy })
}

function onDragEnd(): void {
  window.removeEventListener('pointermove', onDragMove)
  window.removeEventListener('pointerup', onDragEnd)
  window.removeEventListener('pointercancel', onDragEnd)
  const moved = dragged
  dragFrom = null
  dragged = false
  if (moved) savePos()
  // 折叠态：图标只有一个动作 —— 没拖动就是「点一下展开」
  if (!moved && !expanded.value) setExpanded(true)
}

/** 展开/折叠。折叠后左上角不动，展开时按新宽度再夹一次（否则贴右边的面板会露出去）。 */
function setExpanded(v: boolean): void {
  expanded.value = v
  void nextTick(() => {
    pos.value = clampPos(pos.value)
  })
}

function onResize(): void {
  pos.value = clampPos(pos.value)
}

onMounted(() => {
  // 存档位置可能来自更大的窗口（缩窗口后位置就越界了），挂载时先夹一次
  pos.value = clampPos(pos.value)
  window.addEventListener('resize', onResize)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  window.removeEventListener('pointermove', onDragMove)
  window.removeEventListener('pointerup', onDragEnd)
  window.removeEventListener('pointercancel', onDragEnd)
})

const inputClass =
  'w-full rounded border border-line bg-surface px-1.5 py-1 text-[13px] text-ink tnum focus:border-accent focus:outline-none'
</script>

<template>
  <div
    v-if="store.instance"
    ref="root"
    class="fixed z-[45]"
    :style="{
      left: `${pos.x}px`,
      top: `${pos.y}px`,
      width: expanded ? `${PANEL_W}px` : `${ICON}px`,
    }"
  >
    <!-- 展开态：标题栏是把手（按下去拖），折叠按钮不参与拖动 -->
    <div v-if="expanded" class="overflow-hidden rounded-lg border border-line bg-panel shadow-2xl">
      <div
        class="flex cursor-move items-center gap-1.5 border-b border-line bg-elev px-2 py-1.5 select-none"
        title="按住这里拖动到顺手的位置"
        @pointerdown="beginDrag"
      >
        <span class="text-[13px] font-semibold">下单</span>
        <span class="tnum text-[12px] text-dim">现价 {{ priceText }}</span>
        <button
          type="button"
          class="ml-auto flex h-4 w-4 items-center justify-center rounded text-[14px] leading-none text-faint hover:bg-surface hover:text-ink"
          title="折叠成小图标（仍可拖动）"
          @pointerdown.stop
          @click.stop="setExpanded(false)"
        >
          —
        </button>
      </div>

      <div class="flex flex-col gap-1.5 p-2">
        <div class="grid grid-cols-2 gap-1.5">
          <select v-model="side" :class="inputClass">
            <option value="buy">买入 / 做多</option>
            <option value="sell">卖出 / 做空</option>
          </select>
          <select v-model="kind" :class="inputClass">
            <option value="limit">限价单</option>
            <option value="stop">止损单</option>
          </select>
        </div>

        <div class="grid grid-cols-2 gap-1.5">
          <label class="block">
            <span class="text-[12px] text-faint">价格（跳动 {{ step }}）</span>
            <input
              v-model.number="price"
              type="number"
              :step="step"
              :class="inputClass"
              @input="manualPrice = true"
            />
          </label>
          <label class="block">
            <span class="text-[12px] text-faint">数量（手）</span>
            <input
              v-model.number="store.orderQuantity"
              type="number"
              min="1"
              step="1"
              :class="inputClass"
              title="手数只能是整数：1、2、3…（没有 0.5 手）"
            />
          </label>
        </div>

        <div class="flex items-center gap-2">
          <button
            type="button"
            class="text-[12px] text-accent hover:underline"
            @click="useCurrentPrice"
          >
            用现价
          </button>
          <button
            type="button"
            class="ml-auto rounded bg-amber-600 px-3 py-1 text-[13px] font-medium text-white hover:bg-amber-500 disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="!store.canOperate || !inst"
            @click="submit"
          >
            挂单
          </button>
        </div>

        <!--
          市价单：两个按钮，方向写死在按钮里，不跟上面的下拉走 ——
          复盘里最常见的动作是反手（刚市价买完，下一根就要市价卖），
          共用一个方向的话每次都得先改下拉再点按钮，改错方向就是一笔反向仓。
        -->
        <div class="grid grid-cols-2 gap-1.5">
          <button
            type="button"
            class="rounded bg-up px-2 py-1 text-[13px] font-medium whitespace-nowrap text-white transition hover:bg-up/90 disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="!store.canOperate || !inst || !(currentPrice > 0)"
            :title="`市价买入 ${store.orderQuantity} 手：按现价 ${priceText} 立即成交，不使用上面填的价格`"
            @click="submitMarket('buy')"
          >
            市价买入 {{ store.orderQuantity }} 手
          </button>
          <button
            type="button"
            class="rounded bg-down px-2 py-1 text-[13px] font-medium whitespace-nowrap text-white transition hover:bg-down/90 disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="!store.canOperate || !inst || !(currentPrice > 0)"
            :title="`市价卖出 ${store.orderQuantity} 手：按现价 ${priceText} 立即成交，不使用上面填的价格`"
            @click="submitMarket('sell')"
          >
            市价卖出 {{ store.orderQuantity }} 手
          </button>
        </div>

        <!--
          【nATR 定手数】＝ 以损定仓：先填「最多亏多少」，手数由 n × ATR 推出来。
          算式只有一份（lib/natr.ts），这里显示它算的三件事：止损距离、每手亏多少、几手。
          两个按钮用**描边**而不是实心 —— 和上面那对实心的普通市价按钮区分开：
          它们的区别只在「手数从哪来」（手填 vs 按 ATR 算出来），长得一样会点错。
        -->
        <div class="rounded border border-line bg-elev p-1.5">
          <div class="flex items-baseline justify-between gap-1">
            <span class="text-[12px] font-semibold text-ink">nATR 定手数</span>
            <span class="tnum truncate text-[11px] text-faint" title="用的是【指标 ▾】里这套 ATR 参数">
              {{ atrLegend(store.atr) }}
            </span>
          </div>

          <div class="mt-1 grid grid-cols-2 gap-1.5">
            <label class="block">
              <span class="text-[11px] text-faint">n（倍 ATR）</span>
              <input
                type="number"
                min="0.1"
                max="100"
                step="0.1"
                :class="inputClass"
                :value="store.natr.n"
                title="止损距离 = n × ATR。可以带小数：1.5 倍 ATR 就是 1.5"
                @change="onNatrN"
              />
            </label>
            <label class="block">
              <span class="text-[11px] text-faint">最大亏损（$）</span>
              <input
                type="number"
                min="1"
                step="10"
                :class="inputClass"
                :value="store.natr.maxLoss"
                title="这笔交易最多亏多少美元（按 nATR 距离被止损时的金额）"
                @change="onNatrMaxLoss"
              />
            </label>
          </div>

          <div class="tnum mt-1 text-[11px] leading-4" :title="natrCalc">
            <template v-if="plan">
              <div class="text-dim">
                ATR {{ atrText }} · {{ store.natr.n }}×ATR = {{ fmtPoints(plan.stopDistance) }} 点
              </div>
              <div>
                <span class="text-faint">每手亏 {{ fmtUsd(plan.riskPerLot) }} →</span>
                <b class="ml-1 text-[12px] text-ink">{{ plan.lots }} 手</b>
              </div>
              <!-- 取整后略超上限是正常结果（用户定的口径），淡淡提一句；1 手都亏不起才标红 -->
              <div v-if="plan.overRisk" class="text-down">
                1 手就亏 {{ fmtUsd(plan.actualLoss) }}，超上限
              </div>
              <div v-else-if="plan.over" class="text-faint">
                实际 {{ fmtUsd(plan.actualLoss) }}（略超上限）
              </div>
            </template>
            <div v-else class="text-faint">ATR 还没算出来：K 线不足 {{ store.atr.length }} 根</div>
          </div>

          <div class="mt-1 grid grid-cols-2 gap-1.5">
            <button
              type="button"
              class="rounded border border-up bg-up/10 px-2 py-1 text-[13px] font-medium whitespace-nowrap text-up transition hover:bg-up/20 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="natrDisabled"
              :title="natrButtonTitle"
              @click="submitNatrMarket('buy')"
            >
              市价买入 {{ natrLots }} 手
            </button>
            <button
              type="button"
              class="rounded border border-down bg-down/10 px-2 py-1 text-[13px] font-medium whitespace-nowrap text-down transition hover:bg-down/20 disabled:cursor-not-allowed disabled:opacity-40"
              :disabled="natrDisabled"
              :title="natrButtonTitle"
              @click="submitNatrMarket('sell')"
            >
              市价卖出 {{ natrLots }} 手
            </button>
          </div>
        </div>

        <!--
          【市价退仓并取消所有挂单】（v0.20.0）。
          单独占一整行、描边而不是实心：它是「收摊」动作，不该和上面两个高频的
          市价按钮长得一样。一条消息 = 服务端一次做完（先平仓、再撤单），
          所以这里不需要「先点撤单、再点市价」那两步。
          颜色用中性描边而不是红：红在这个程序里只表示「做空 / 亏损」，
          退仓不是一个方向，别让它看起来像在叫你卖。
        -->
        <button
          type="button"
          class="rounded border border-line bg-elev px-2 py-1 text-[13px] font-medium whitespace-nowrap text-ink transition hover:border-accent hover:text-accent disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="flatDisabled"
          :title="flatTitle"
          @click="flatAll"
        >
          市价退仓并取消所有挂单
        </button>

        <div
          class="truncate text-[12px] leading-4 text-faint"
          title="限价买：最低价 ≤ 挂单价；限价卖：最高价 ≥ 挂单价；止损单相反。无资金/手续费/滑点。"
        >
          规则见 README「模拟交易：撮合规则与盈亏」· 不计手续费/滑点
        </div>
      </div>
    </div>

    <!--
      折叠态：一枚 44px 的圆形小图标。整枚都是把手 —— 没拖动就展开，拖了就只是换位置。
      `pointerdown` 走 beginDrag（内含 preventDefault），展开交给 onDragEnd 判定，
      所以这里不绑 click：绑了会和拖动打架（拖完松手也算一次 click）。
    -->
    <button
      v-else
      type="button"
      class="flex cursor-move items-center justify-center rounded-full border border-line bg-panel text-[12px] font-semibold text-ink shadow-xl transition select-none hover:border-accent hover:text-accent"
      :style="{ width: `${ICON}px`, height: `${ICON}px` }"
      title="单击展开下单面板 · 按住可拖到别处"
      @pointerdown="beginDrag"
    >
      下单
    </button>
  </div>
</template>
