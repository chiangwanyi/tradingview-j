<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import AnnotationEditTemplates from '@/components/AnnotationEditTemplates.vue'
import { ATR_SMOOTHINGS, type AtrSmoothing } from '@/components/chart/atr'
import AnnotationTemplates from '@/components/AnnotationTemplates.vue'
import { TOOL_LABEL, isDrawTool, useTradeStore, type DrawTool } from '@/stores/trade'
import { TIMEFRAMES, type Timeframe } from '@/types/protocol'

/**
 * 图表上方的操作条。
 *
 * 从 web/（复盘）搬过来，形状基本没变：左边五种绘图工具 + 模板条，中间周期和指标，
 * 右边这一块盘面的概况。**改掉的只有「谁来执行」**：
 *
 *   - 旧版里步进 / 接管 / 机器人 / 录音都是发消息给 Go 复盘服务，由后端执行再广播回来；
 *   - webapp 没有那套服务，交易后端也还没接通（`TRADING-ENGINE-DESIGN.md` 仍是方案），
 *     所以这些入口全部拿掉 —— 不留「点了没反应」的按钮。留下的按钮（工具、周期、
 *     指标、模板）都是**纯前端状态**，点一下立刻生效。
 *
 * 绘图仍然是「前端预览、本页定稿」：图形只活在这个标签页的内存里，刷新就没了。
 * 这不是遗漏，页面上有一条常驻说明写着这件事，别让人以为画完就存住了。
 */
const store = useTradeStore()

const drawTools: DrawTool[] = ['trend', 'ray', 'rect', 'arrow', 'fib']

const drawHint: Record<DrawTool, string> = {
  trend: '趋势线：两次单击（起点 → 终点），按住 Shift 画水平线',
  ray: '射线：单击一下就画出一条水平朝右的射线；按住 Shift 改成两次单击画斜射线',
  rect: '矩形：两次单击定两个对角点',
  arrow: '箭头线：两次单击，终点带箭头；Shift 画水平箭头',
  fib: '斐波那契回撤：两次单击定价格区间，四条水平线 -1 / 0 / 1 / 2 自动排开',
}

const canEdit = computed(() => store.canOperate)

/** 当前是不是绘图工具 —— 模板条只在绘图工具下出现（v-if 用得到，也顺手做类型收窄） */
const drawKind = computed<DrawTool | null>(() => (isDrawTool(store.tool) ? store.tool : null))

const hasMoreHint = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  return `${inst.symbol} · ${inst.bars.length} 根 5 分钟 K 线`
})

const btn = 'rounded border border-line px-2 py-1 text-[13px] transition disabled:cursor-not-allowed disabled:opacity-40'
const btnIdle = 'bg-panel text-dim hover:border-accent hover:text-accent'
const btnActive = 'border-accent bg-accent-soft text-accent'

/** 参数输入框：小一号、贴边，和按钮同一行不显胖 */
const num = 'w-12 rounded border border-line bg-panel px-1 py-0.5 text-[12px] text-ink'
/** 参数下拉框（平滑方式）：和输入框等高，别在面板里显得凹进去一块 */
const sel = 'rounded border border-line bg-panel px-1 py-0.5 text-[12px] text-ink'

/**
 * 指标参数用 `change` 而不是 `input`：输到一半的「0.」「1」不该立刻被夹成 0.01，
 * 等回车 / 失焦再一次性生效（和 TradingView 的设置框同一个手感）。
 * 生效后把夹过的真实值写回 DOM —— 否则输入框里会留着用户敲的那个越界值。
 */
function onLag(e: Event): void {
  const el = e.target as HTMLInputElement
  store.setIndicator({ lookback: Number(el.value) })
  el.value = String(store.indicator.lookback)
}

function onDojiSize(e: Event): void {
  const el = e.target as HTMLInputElement
  store.setIndicator({ dojiSize: Number(el.value) })
  el.value = String(store.indicator.dojiSize)
}

/**
 * Bar Count 的颜色。
 *
 * 用 `input`（拖动取色器时实时生效）而不是 `change`：调颜色本来就是个「拖到满意为止」
 * 的动作，每次松手才变一次的话根本没法调。颜色本身是 #rrggbb，取色器给不出非法值，
 * 不需要写回；`sanitizeBarCount` 仍然兜一道（手改过 localStorage 的情况）。
 */
function onBarCountColor(e: Event): void {
  store.setBarCount({ color: (e.target as HTMLInputElement).value })
}

/**
 * ATR 的三个参数（v0.23.0）：开关 / Length / Smoothing。
 *
 * 和 Doji 那两个输入框同一套手感：`change` 才生效（输到一半的「1」不该立刻被夹成 1），
 * 生效后把夹过的真实值写回 DOM —— 否则框里会留着用户敲的那个越界值。
 * 下拉框不用写回：它的可选值是写死的四个，改不出别的。
 */
function onAtrShow(e: Event): void {
  store.setAtr({ show: (e.target as HTMLInputElement).checked })
}

function onAtrLength(e: Event): void {
  const el = e.target as HTMLInputElement
  store.setAtr({ length: Number(el.value) })
  el.value = String(store.atr.length)
}

function onAtrSmoothing(e: Event): void {
  const el = e.target as HTMLSelectElement
  store.setAtr({ smoothing: el.value as AtrSmoothing })
}

// ---------------- 周期切换 ----------------

/**
 * 【周期 ▾】：把画布上的 K 线换成 10/15/30/60/2h/3h/4h。
 *
 * 只换**画布看的那一份**，不动数据：这里没有「游标」那套东西（交易后端未接通），
 * 聚合由前端现算（见 `stores/trade.ts` 的 `displayBars`）。
 */
const tfOpen = ref(false)
const tfBox = ref<HTMLElement | null>(null)
/** 当前周期是几个 5 分钟一根（写提示用，不参与计算） */
const tfMinutes = computed(() => TIMEFRAMES.find((t) => t.id === store.timeframe)?.minutes ?? 5)
const canTimeframe = computed(() => !!store.instance && store.bars.length > 0)
const tfHint = computed(() => {
  if (!store.instance) return '选一个标的和日期之后才能换周期'
  return 'K 线周期：用这一天已加载的 5 分钟数据现聚合（只影响显示，不改数据）'
})

/** 选一个周期：点完就收起面板（请求由 store 里的 watch 触发） */
function pickTimeframe(tf: Timeframe): void {
  store.setTimeframe(tf)
  tfOpen.value = false
}

// ---------------- 指标下拉框 ----------------

const indicatorOpen = ref(false)
const indicatorBox = ref<HTMLElement | null>(null)

/**
 * 当前有几个指标是开着的（写在下拉按钮上）。
 *
 * 为什么要这个角标：指标收进下拉框之后，工具条上不再能一眼看出「EMA 关掉了没有」。
 * 一个数字就是最小的提示 —— 不用点开也知道现在图上有没有指标。
 * ATR 也算一个：它占的是价格区下方的地盘，收起状态下更看不出来。
 */
const activeIndicators = computed(
  () =>
    (store.showEma ? 1 : 0) +
    (store.atr.show ? 1 : 0) +
    (store.indicator.show ? 1 : 0) +
    (store.barCount.show ? 1 : 0),
)

/** 点下拉框外面就收起：面板是浮在画布上的，不收起会一直挡住 K 线（两个下拉各判各的框） */
function onDocPointerDown(ev: PointerEvent): void {
  const target = ev.target as Node
  if (indicatorOpen.value && indicatorBox.value && !indicatorBox.value.contains(target)) {
    indicatorOpen.value = false
  }
  if (tfOpen.value && tfBox.value && !tfBox.value.contains(target)) tfOpen.value = false
}

/** Esc 也收起（和画布上「Esc 退一层」的习惯一致；这里只退下拉框这一层） */
function onDocKeyDown(ev: KeyboardEvent): void {
  if (ev.key !== 'Escape') return
  indicatorOpen.value = false
  tfOpen.value = false
}

onMounted(() => {
  window.addEventListener('pointerdown', onDocPointerDown)
  window.addEventListener('keydown', onDocKeyDown)
})

onBeforeUnmount(() => {
  window.removeEventListener('pointerdown', onDocPointerDown)
  window.removeEventListener('keydown', onDocKeyDown)
})
</script>

<template>
  <div class="relative flex flex-wrap items-center gap-2 border-b border-line px-2 py-1.5">
    <div class="flex items-center gap-1">
      <!--
        只有绘图工具。指针 / 修改两个按钮已经删掉：
        「没拿笔」本身就是指针状态（画完自动交回），「改一个图形」靠直接点它。
      -->
      <button
        v-for="t in drawTools"
        :key="t"
        type="button"
        :title="drawHint[t]"
        :class="[btn, store.tool === t ? btnActive : btnIdle]"
        :disabled="!canEdit"
        @click="store.tool = store.tool === t ? 'select' : t"
      >
        {{ TOOL_LABEL[t] }}
      </button>
    </div>

    <!-- 模板条：每个工具各有自己的一组（后端持久化，换实例也不会丢） -->
    <AnnotationTemplates v-if="drawKind" :kind="drawKind" />

    <!--
      编辑态的模板条（v0.20.0）：选中一个图形之后出现，只列**同类**模板，
      点一下就把样式套到那个图形上（不动位置）。

      **必须脱离排版流**（绝对定位 + `top-full`）：它比上面那一行宽得多，留在
      flex 里就会把工具条从一行挤成两行，画布跟着矮 27px。而画布一矮，价格轴就
      要重新铺一遍 —— 用户自己调好的缩放会被抹掉（正是「点一下图形，画布缩放
      就被重置」的来路，见 ChartPanel.draw / scale.setSize）。选中一个图形只是
      「看一眼、换个样式」，画布不该跟着动。所以它像下拉面板一样浮在画布上沿。
    -->
    <div
      v-else-if="store.editingAnnotation"
      class="absolute top-full left-2 z-40 mt-1 max-w-[calc(100%-1rem)] shadow-md"
    >
      <AnnotationEditTemplates :annotation="store.editingAnnotation" />
    </div>

    <div class="h-4 w-px bg-line" />

    <!--
      周期切换：只换画布看的那份 K 线，不动数据（聚合在前端现算）。
      收进下拉框：8 个按钮平铺会把工具条挤成两行，画布跟着矮一截（见文件头）。
    -->
    <div ref="tfBox" class="relative">
      <button
        type="button"
        :class="[btn, tfOpen ? btnActive : btnIdle]"
        :disabled="!canTimeframe"
        :title="tfHint"
        @click="tfOpen = !tfOpen"
      >
        周期
        <span class="tnum ml-1 rounded bg-accent-soft px-1 text-[11px] text-accent">{{
          store.timeframe
        }}</span>
        <span class="ml-0.5 text-[10px]">▾</span>
      </button>

      <div
        v-if="tfOpen"
        class="absolute top-full left-0 z-40 mt-1 w-[10.5rem] rounded border border-line bg-panel p-1 text-[13px] shadow-xl"
      >
        <button
          v-for="t in TIMEFRAMES"
          :key="t.id"
          type="button"
          :class="[
            'flex w-full items-center justify-between rounded px-2 py-1 text-left',
            store.timeframe === t.id ? btnActive : 'text-ink hover:bg-accent-soft',
          ]"
          @click="pickTimeframe(t.id)"
        >
          <span>{{ t.id }}</span>
          <span class="text-[11px] text-faint">{{ t.minutes }} 分钟</span>
        </button>
        <div class="mt-1 border-t border-line px-2 pt-1 text-[11px] leading-4 text-faint">
          用这一天的 5 分钟 K 线现聚合（前端算，不请求后端）；{{ tfMinutes }} 分钟一根即
          {{ tfMinutes / 5 }} 根合成一根。
        </div>
      </div>
    </div>

    <div class="h-4 w-px bg-line" />

    <!--
      【指标 ▾】（v0.20.0）：EMA20 与 Doji X + Blue ZigZag 的开关、参数全在里面。
      按钮上的数字 = 现在开着几个指标，收起状态下也能看出「图上有指标」。
    -->
    <div ref="indicatorBox" class="relative">
      <button
        type="button"
        :class="[btn, indicatorOpen ? btnActive : btnIdle]"
        title="指标：EMA20 / ATR / Doji X + Blue ZigZag / Bar Count（点开设置开关与参数）"
        @click="indicatorOpen = !indicatorOpen"
      >
        指标
        <span class="ml-0.5 text-[10px]">▾</span>
        <span
          v-if="activeIndicators"
          class="tnum ml-1 rounded bg-accent-soft px-1 text-[11px] text-accent"
          >{{ activeIndicators }}</span
        >
      </button>

      <div
        v-if="indicatorOpen"
        class="absolute top-full left-0 z-40 mt-1 w-[17rem] rounded border border-line bg-panel p-2 text-[13px] shadow-xl"
      >
        <label class="flex items-center gap-2 py-1">
          <input v-model="store.showEma" type="checkbox" class="h-3.5 w-3.5 accent-sky-600" />
          <span class="text-ink">EMA20</span>
          <span class="ml-auto text-[11px] text-faint">后端算好的均线</span>
        </label>

        <!--
          ATR（v0.22.0 只有开关，v0.23.0 起可调）：和 TradingView 内置的
          「Average true range」同一套参数 —— Length + Smoothing（RMA / SMA / EMA / WMA），
          默认 14 / RMA，和脚本里那两个 `input()` 的 defval 一致。

          它画在价格区下方一条独立副图里：波幅的量纲就是价格，和 30000 的报价共用一个
          纵轴会被压成贴底的直线。参数关掉时也能改 ——「先调好再打开」少一次来回。
        -->
        <label class="flex items-center gap-2 py-1" title="ATR：平均真实波幅（副图画在价格区下方，参数见下面两行）">
          <input
            type="checkbox"
            class="h-3.5 w-3.5 accent-sky-600"
            :checked="store.atr.show"
            @change="onAtrShow"
          />
          <span class="text-ink">ATR</span>
          <span class="ml-auto text-[11px] text-faint">下方独立副图</span>
        </label>
        <label class="flex items-center gap-2 py-1" title="Length：ATR 的平滑周期（Pine 的 length，1..1000）">
          <span class="w-[4.75rem] shrink-0 text-dim">Length</span>
          <input
            type="number"
            min="1"
            max="1000"
            step="1"
            :class="num"
            :value="store.atr.length"
            @change="onAtrLength"
          />
          <span class="text-[11px] text-faint">1 – 1000</span>
        </label>
        <label
          class="flex items-center gap-2 py-1"
          title="Smoothing：真实波幅的平滑方式（Pine 的 ta.rma / ta.sma / ta.ema / ta.wma；RMA 就是 Wilder 平滑）"
        >
          <span class="w-[4.75rem] shrink-0 text-dim">Smoothing</span>
          <select :class="sel" :value="store.atr.smoothing" @change="onAtrSmoothing">
            <option v-for="s in ATR_SMOOTHINGS" :key="s" :value="s">{{ s }}</option>
          </select>
        </label>

        <div class="my-1 h-px bg-line" />

        <label class="flex items-center gap-2 py-1">
          <input
            type="checkbox"
            class="h-3.5 w-3.5 accent-sky-600"
            :checked="store.indicator.show"
            @change="store.setIndicator({ show: ($event.target as HTMLInputElement).checked })"
          />
          <span class="text-ink">Doji X + ZigZag</span>
        </label>
        <div class="mb-1 pl-5 text-[11px] leading-4 text-faint">
          蓝点标出动量转折（上穿画最低点、下穿画最高点），✖ 标出十字星
        </div>

        <!--
          参数名沿用 Pine 脚本里的 input 名（Lag / Doji Size），
          这样对着原始脚本核数的时候不用再翻译一层。指标关掉时参数照样能改 ——
          「先把参数调好再打开」比「打开后才能改」少一次来回。
        -->
        <label
          class="flex items-center gap-2 py-1"
          title="Lag：EMA 周期，同时是高低点的回看窗口（Pine 的 lookback，2..50）"
        >
          <span class="w-14 shrink-0 text-dim">Lag</span>
          <input
            type="number"
            min="2"
            max="50"
            step="1"
            :class="num"
            :value="store.indicator.lookback"
            @change="onLag"
          />
          <span class="text-[11px] text-faint">2 – 50</span>
        </label>
        <label
          class="flex items-center gap-2 py-1"
          title="Doji Size：实体不超过振幅的这个比例就算十字星（Pine 的 dojiSize，0.01..1）"
        >
          <span class="w-14 shrink-0 text-dim">Doji</span>
          <input
            type="number"
            min="0.01"
            max="1"
            step="0.01"
            :class="num"
            :value="store.indicator.dojiSize"
            @change="onDojiSize"
          />
          <span class="text-[11px] text-faint">0.01 – 1</span>
        </label>

        <div class="my-1 h-px bg-line" />

        <!--
          Bar Count（K 线下方的「当天第几根」）：
          编号按**每张 CSV（每个交易日）从 #1 重来**、只标单数，口径全部在
          chart/barcount.ts 里；这里只有开关和颜色两件事。
        -->
        <label
          class="flex items-center gap-2 py-1"
          title="Bar Count：在每根 K 线下方标出「这是当天的第几根」"
        >
          <input
            type="checkbox"
            class="h-3.5 w-3.5 accent-sky-600"
            :checked="store.barCount.show"
            @change="store.setBarCount({ show: ($event.target as HTMLInputElement).checked })"
          />
          <span class="text-ink">Bar Count</span>
          <span class="ml-auto text-[11px] text-faint">K 线下方</span>
        </label>
        <label
          class="flex items-center gap-2 py-1"
          title="Bar Count 的数字颜色（只影响你自己的屏幕，不进存档）"
        >
          <span class="w-14 shrink-0 text-dim">颜色</span>
          <input
            type="color"
            class="h-6 w-10 cursor-pointer rounded border border-line bg-panel p-0.5"
            :value="store.barCount.color"
            @input="onBarCountColor"
          />
          <span class="font-mono text-[11px] text-faint">{{ store.barCount.color }}</span>
        </label>
        <div class="mb-1 pl-5 text-[11px] leading-4 text-faint">
          每张表（每个交易日）从 #1 重新数，只标单数 1、3、5…
        </div>

        <div class="mt-1 border-t border-line pt-1 text-[11px] text-faint">
          指标只影响显示，不进存档，也不广播给别人
        </div>
      </div>
    </div>

    <div class="h-4 w-px bg-line" />

    <!--
      这里**没有【下一根】/【下一天开盘】/【接管】/【机器人】/【录音】**：
      它们全都是「改了盘面要落库」的动作，而交易后端还没接通（撮合、挂单、成交都只在
      `TRADING-ENGINE-DESIGN.md` 里）。留着按钮只会得到一个点了没反应的界面 ——
      宁可现在就少几个按钮，等接口做出来再加回来。
      （唯一的例外【下一根】放在页面的状态条上：它只把账户的「最新时间」往后推一格，
       已经接通了，见 `TradeView.vue`。）
    -->

    <div class="ml-auto flex items-center gap-3 text-[12px] text-dim">
      <span v-if="store.instance" class="text-faint">{{ hasMoreHint }}</span>
      <span class="rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[11px] text-amber-700">
        看盘模式 · 下单未接通
      </span>
    </div>
  </div>
</template>
