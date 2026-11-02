<script setup lang="ts">
import { computed, ref } from 'vue'

import { atrLegend } from '@/components/chart/atr'
import { sessionBlockReason } from '@/lib/session'
import { useTradeStore } from '@/stores/trade'
import type { Side } from '@/types/protocol'

/**
 * 下单条：**顶部工具条同一行的右端**（`ChartToolbar` 的最后一个子元素，`ml-auto` 推到底）。
 *
 * 以前这一块是画布上的浮动窗（`TradeFloat`）：可以拖、可以折叠成一个 44px 的图标。
 * 后来改成固定在工具条里的一行（压在周期 / 指标下面），因为浮窗挡 K 线 —— 复盘时要
 * 反复对照「价格走到哪」和「我要挂哪个价」，浮窗压住右上角的刻度、挂单线标签和平仓线
 * 信息卡，得先拖开才能看。**2026-11-02 起再并进周期 / 指标那一行**：单独占一行等于白拿
 * 走画布一条高度，而这一行右边本来就是空的。
 *
 * 留在这一行的只有**真正常按的三类动作**：
 *
 *   1. 手数 + 两个市价按钮（方向写死在按钮里，不搞方向下拉 —— 复盘里最常见的动作是反手，
 *      共用一个方向每次都得先改下拉，改错方向就是一笔反向仓）；
 *   2. nATR 定手数（以损定仓）：参数（n、最多亏多少）**默认折叠成一行字**，
 *      点【参数】才展开成输入框 —— 这几个数是「设置一次、很久不动」的，占着位置只会把
 *      真正要按的按钮挤远；手数由 n × ATR 现算，按下去后端顺手挂一张同手数的保护性 stop 单；
 *   3. 【市价退仓并撤单】——「收摊」动作，和下单是一件事的两头，手边要够得着。
 *
 * 手填价格的限价 / 止损、以及【挂单】按钮已经拿掉：那套「方向 + 单型 + 价格」四个控件
 * 只为了一张挂单，却要占掉半行；挂单改走画布上的右键菜单（那里点哪根 K 线就用哪个价）。
 *
 * 持仓、挂单列表、成交流水也不在这里，还在底部 Tab 里（`TradeDock` / `OrderPanel`）：
 * 那些是「看」的，不需要随时点。数量用 `store.orderQuantity`，因为它同时被右键菜单用着，
 * 只能是同一个数。
 */

const store = useTradeStore()

const inst = computed(() => store.instance)
const currentPrice = computed(() => store.currentBar?.close ?? 0)
const priceText = computed(() =>
  currentPrice.value ? currentPrice.value.toFixed(store.priceDecimals) : '—',
)

/**
 * 市价成交：价格由后端取当前 K 线收盘价，前端不传价。方向是**参数**，
 * 两个按钮各自写死 —— 反手不用先改任何下拉。
 */
function submitMarket(dir: Side): void {
  if (!inst.value || !store.canOperate) return
  // 禁交易时段 / 已判失败 / 已达标：按钮本来就灰了，这里再挡一次 ——
  // 快捷键也会走到这个函数，按钮的 disabled 拦不住它们
  if (!store.canTrade) {
    store.noteError(orderBlocked.value ?? '现在不能下单')
    return
  }
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
 * 算式只有一份，在 `lib/natr.ts`；这里只负责显示、发**一条**市价单，
 * 并把**止损距离**一起交给后端（后端在同一个锁里成交完就挂 stop 单）。
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

/**
 * 参数是否展开。默认收起：`n` 和「最多亏」是设置一次能用很久的两个数，
 * 常态下只把「现在设的是多少」印出来，要改再点【参数】。
 * 不存 localStorage —— 刷新页面回到收起态，这一行永远是它最短的样子。
 */
const natrEdit = ref(false)

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
 * 按算出来的手数市价成交，**并让后端顺手挂上保护性止损**。
 * **方向是参数**、手数只来自 `plan` —— 不读也不写上面的「数量」输入框：那一格是
 * 手填市价用的，两个数混在一起就会出现「点了 nATR 买入，手数和输入框对不上」的困惑。
 *
 * 传的是**距离**（n × ATR）而不是止损价：价格由后端按它自己的成交价算
 * （成交价 ∓ 距离）。前端拿这份现价去减会在四舍五入上差一跳，而那一跳正好在止损位上。
 * 一条消息 = 服务端一次做完（市价成交 + 挂 stop 单），中间不会出现「有仓、没止损」。
 */
function submitNatrMarket(dir: Side): void {
  if (!store.canOperate || !plan.value) return
  if (!store.canTrade) {
    store.noteError(orderBlocked.value ?? '现在不能下单')
    return
  }
  if (!(currentPrice.value > 0)) return
  store.marketOrder(dir, plan.value.lots, plan.value.stopDistance)
}

/**
 * 不能下单的原因（`null` = 能下单）。
 *
 * 后端才是判官（`requireTradable` 每次现算），这里只为了把按钮提前关掉并说明为什么 ——
 * 让用户点下去再收到一条报错，等于把「已判失败」写在错误弹窗里而不是按钮上。
 */
const orderBlocked = computed(() => {
  const pf = store.pf
  if (pf?.status === 'FAILED') return '账户已判失败（撞了亏损线）：只能看盘，挂单和市价单已禁用'
  if (pf?.frozen) return '已达标待升级：持仓已平、暂停下单，去账户详情页点「升级」'
  return sessionBlockReason(store.currentTime)
})

/**
 * 工具条上只放得下一个**短标签**：完整原因在 `title` 里。
 * 这么长的句子铺在这里会把整行按钮挤到第二行，而它只是「按钮为什么灰了」的解释。
 */
const orderBlockedShort = computed(() => {
  const pf = store.pf
  if (pf?.status === 'FAILED') return '已判失败'
  if (pf?.frozen) return '待升级'
  if (sessionBlockReason(store.currentTime)) return '禁交易时段'
  return ''
})

const natrDisabled = computed(
  () => !store.canOperate || !store.canTrade || !inst.value || !plan.value || !(currentPrice.value > 0),
)

/**
 * 两个按钮**各自**会挂上的止损价（买 = 现价 − 距离，卖 = 现价 + 距离）。
 *
 * 这只是预告：真正的止损价由后端按它自己的成交价算（见 store.marketOrder）。
 * 两边差不出一个小数位，但方向上不能含糊 —— 按下去之前要能看见「止损会挂在哪」。
 */
const natrStopPrices = computed(() => {
  const p = plan.value
  if (!p || !(currentPrice.value > 0)) return null
  return { buy: currentPrice.value - p.stopDistance, sell: currentPrice.value + p.stopDistance }
})

/** 一句话说清这次是怎么算出来的（鼠标停在按钮上就能核对，不用回翻文档）。 */
const natrCalc = computed(() => {
  const p = plan.value
  if (!p) return ''
  const stops = natrStopPrices.value
  const where = stops
    ? ` · 止损 ${stops.buy.toFixed(store.priceDecimals)}（买）/ ${stops.sell.toFixed(store.priceDecimals)}（卖）`
    : ''
  return `${store.natr.n}×ATR ${fmtPoints(p.stopDistance)} 点 · 每手亏 ${fmtUsd(p.riskPerLot)} · 理论 ${p.exact.toFixed(2)} 手 → ${p.lots} 手（实际 ${fmtUsd(p.actualLoss)}）${where}`
})

const natrButtonTitle = computed(() => {
  const p = plan.value
  if (!p) return 'ATR 还没算出来：K 线不足 Length 根，先往前多揭示几根'
  const stops = natrStopPrices.value
  if (!stops) return `${natrCalc.value}；按现价成交，止损挂在成交价 ${store.natr.n}×ATR 之外`
  return `${natrCalc.value}；买入会在 ${stops.buy.toFixed(store.priceDecimals)}、卖出会在 ${stops.sell.toFixed(store.priceDecimals)} 自动挂一张同手数的 stop 单`
})

// ---------------- 市价退仓并取消所有挂单 ----------------

/** 仓位手数（绝对值）：平仓手数由**仓位**决定，和左边那个「数量」输入框无关 */
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
  if (!inst.value) return '先选一个标的和日期'
  if (!store.canOperate) return '现在只能看：没有操作权时不能退仓'
  if (positionQty.value === 0 && store.openOrders.length === 0) return '已经空仓、也没有挂单，没什么可退的'
  const parts: string[] = []
  if (positionQty.value > 0) parts.push(`按现价平掉 ${flatSide.value} 头 ${positionQty.value} 手`)
  if (store.openOrders.length > 0) parts.push(`撤销全部 ${store.openOrders.length} 张挂单`)
  return `${parts.join('，')}（一条消息发出去，后端一次做完）`
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

const matchHint =
  '市价按当前 K 线收盘价成交；手续费 = 每手每边，开仓平仓各收一次。挂单（限价 / 止损）走画布右键菜单：本根挂的单下一根才撮合，跳空越过挂单价时按开盘价成交。'

/** 工具条里的控件都小一号：这一行要装下三组东西，字号再大就折行了 */
const field =
  'h-6 rounded border border-line bg-panel px-1 text-[12px] text-ink tnum focus:border-accent focus:outline-none'
const sep = 'h-4 w-px shrink-0 bg-line'
</script>

<template>
  <div
    v-if="store.instance"
    class="flex flex-wrap items-center gap-x-2 gap-y-1 text-[12px]"
    :title="matchHint"
  >
    <!-- 不能下单时先说清楚为什么：按钮置灰而不解释，看起来像坏了 -->
    <span
      v-if="orderBlockedShort"
      class="shrink-0 rounded-sm border border-red-200 bg-red-50 px-1.5 py-0.5 text-[11px] text-red-700"
      :title="orderBlocked ?? ''"
    >
      {{ orderBlockedShort }}
    </span>

    <!-- 1. 手数 + 两个市价按钮：方向写死在按钮里，方便反手 -->
    <div class="flex items-center gap-1">
      <label class="flex items-center gap-1" title="手数只能是整数：1、2、3…（没有 0.5 手）；nATR 的手数由参数算，不看这里">
        <span class="text-faint">数量</span>
        <input
          v-model.number="store.orderQuantity"
          type="number"
          min="1"
          step="1"
          :class="field"
          class="w-[3.4rem]"
        />
      </label>
      <button
        type="button"
        class="shrink-0 rounded bg-up px-2 py-0.5 font-medium whitespace-nowrap text-white transition hover:bg-up/90 disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="!store.canOperate || !store.canTrade || !inst || !(currentPrice > 0)"
        :title="orderBlocked ?? `市价买入 ${store.orderQuantity} 手：按现价 ${priceText} 立即成交`"
        @click="submitMarket('buy')"
      >
        市价买入 {{ store.orderQuantity }} 手
      </button>
      <button
        type="button"
        class="shrink-0 rounded bg-down px-2 py-0.5 font-medium whitespace-nowrap text-white transition hover:bg-down/90 disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="!store.canOperate || !store.canTrade || !inst || !(currentPrice > 0)"
        :title="orderBlocked ?? `市价卖出 ${store.orderQuantity} 手：按现价 ${priceText} 立即成交`"
        @click="submitMarket('sell')"
      >
        市价卖出 {{ store.orderQuantity }} 手
      </button>
    </div>

    <div :class="sep" />

    <!--
      2. nATR 定手数＝以损定仓：先定「最多亏多少」，手数由 n × ATR 推出来。
      参数默认**收起来只显示当前值**（`1× · 最多亏 $200`），点【参数】才展开两个输入框 ——
      它们是「设一次用很久」的数，常态占着位置只会把要按的按钮挤远。
      算式只有一份（lib/natr.ts）；两个按钮用**描边**而不是实心，和上面那对实心市价按钮区分开：
      区别不只是「手数从哪来」，还在于**这两个按钮带止损**，长得一样会点错。
    -->
    <div class="flex items-center gap-1 rounded border border-line bg-elev px-1.5 py-0.5">
      <span class="shrink-0 font-semibold text-ink">nATR</span>

      <!-- 收起态：只印当前参数 -->
      <span
        v-if="!natrEdit"
        class="tnum shrink-0 whitespace-nowrap text-dim"
        :title="`当前参数：${store.natr.n}×ATR，最多亏 ${fmtUsd(store.natr.maxLoss)}（${atrLegend(store.atr)}）；点【参数】可改`"
      >
        {{ store.natr.n }}× · 最多亏 {{ fmtUsd(store.natr.maxLoss) }}
      </span>

      <!-- 展开态：两个可改的输入框 -->
      <template v-else>
        <label class="flex items-center gap-0.5" title="止损距离 = n × ATR。可以带小数：1.5 倍 ATR 就是 1.5">
          <span class="text-[11px] text-faint">n</span>
          <input
            type="number"
            min="0.1"
            max="100"
            step="0.1"
            :class="field"
            class="w-[3.2rem]"
            :value="store.natr.n"
            @change="onNatrN"
          />
        </label>
        <label class="flex items-center gap-0.5" title="这笔交易最多亏多少美元（按 nATR 距离被止损时的金额）">
          <span class="text-[11px] text-faint">最多亏 $</span>
          <input
            type="number"
            min="1"
            step="10"
            :class="field"
            class="w-[4.2rem]"
            :value="store.natr.maxLoss"
            @change="onNatrMaxLoss"
          />
        </label>
      </template>

      <button
        type="button"
        class="shrink-0 rounded px-1 text-[11px] text-faint transition hover:bg-panel hover:text-accent"
        :title="natrEdit ? '收起参数（值已经生效了）' : '改 n 和最大亏损'"
        @click="natrEdit = !natrEdit"
      >
        {{ natrEdit ? '收起' : '参数' }}
      </button>

      <span class="tnum flex items-baseline gap-1 whitespace-nowrap" :title="natrCalc || atrLegend(store.atr)">
        <template v-if="plan">
          <b class="text-[13px] text-ink">{{ plan.lots }} 手</b>
          <span v-if="natrStopPrices" class="text-[11px] text-faint">
            止损
            <span class="text-up">{{ natrStopPrices.buy.toFixed(store.priceDecimals) }}</span>
            /
            <span class="text-down">{{ natrStopPrices.sell.toFixed(store.priceDecimals) }}</span>
          </span>
          <!-- 取整后略超上限是正常结果（用户定的口径），淡淡提一句；1 手都亏不起才标红 -->
          <span v-if="plan.overRisk" class="text-down" :title="`1 手就亏 ${fmtUsd(plan.actualLoss)}，超上限`">超上限</span>
          <span v-else-if="plan.over" class="text-faint" :title="`实际 ${fmtUsd(plan.actualLoss)}（略超上限）`">略超</span>
        </template>
        <span v-else class="text-[11px] text-faint">ATR 不足 {{ store.atr.length }} 根</span>
      </span>

      <button
        type="button"
        class="shrink-0 rounded border border-up bg-up/10 px-1.5 py-0.5 font-medium whitespace-nowrap text-up transition hover:bg-up/20 disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="natrDisabled"
        :title="natrButtonTitle"
        @click="submitNatrMarket('buy')"
      >
        买入 {{ natrLots }} 手
      </button>
      <button
        type="button"
        class="shrink-0 rounded border border-down bg-down/10 px-1.5 py-0.5 font-medium whitespace-nowrap text-down transition hover:bg-down/20 disabled:cursor-not-allowed disabled:opacity-40"
        :disabled="natrDisabled"
        :title="natrButtonTitle"
        @click="submitNatrMarket('sell')"
      >
        卖出 {{ natrLots }} 手
      </button>
    </div>

    <div :class="sep" />

    <!--
      3. 【市价退仓并取消所有挂单】：单独一组、描边而不是实心 —— 它是「收摊」动作，
      不该和上面两个高频的市价按钮长得一样。一条消息 = 服务端一次做完（先平仓、再撤单），
      所以不需要「先点撤单、再点市价」那两步。颜色用中性描边而不是红：红在这里只表示
      「做空 / 亏损」，退仓不是一个方向，别让它看起来像在叫你卖。
    -->
    <button
      type="button"
      class="shrink-0 rounded border border-line bg-elev px-2 py-0.5 font-medium whitespace-nowrap text-ink transition hover:border-accent hover:text-accent disabled:cursor-not-allowed disabled:opacity-40"
      :disabled="flatDisabled"
      :title="flatTitle"
      @click="flatAll"
    >
      市价退仓并撤单
    </button>
  </div>
</template>
