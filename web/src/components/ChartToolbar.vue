<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import AnnotationEditTemplates from '@/components/AnnotationEditTemplates.vue'
import { ATR_SMOOTHINGS, type AtrSmoothing } from '@/components/chart/atr'
import AnnotationTemplates from '@/components/AnnotationTemplates.vue'
import BotMenu from '@/components/BotMenu.vue'
import { TOOL_LABEL, isDrawTool, useReplayStore, type DrawTool } from '@/stores/replay'

/**
 * 图表上方的操作条。
 *
 * 工具按钮只改本地工具状态；真正会改数据的动作（步进、接管、机器人）一律发消息给后端，
 * 由后端校验、执行、广播回来 —— 所以这里的按钮只是「请求」，不是「直接生效」。
 *
 * 工具只剩一组：**五种绘图工具**。指针不再是按钮（它就是默认状态：画完自动交回、
 * Esc 收起画笔、点空白退出编辑），「修改」更不是按钮 —— 画布上点一下图形就进入编辑，
 * 点空白/点 K 线就退出。
 *
 * 射线是唯一**单击即完成**的：点一下就是一条水平朝右的射线（预览跟着鼠标走）。
 *
 * v0.20.0 的两处收纳：
 *   - **指标收进下拉框**。EMA20 和 Doji X + ZigZag 原来平铺在工具条上，占掉近一半
 *     宽度（还带两个数字输入框），而复盘时它们一整天都不会改一次。现在只有一个
 *     【指标 ▾】，点开才是开关和参数。
 *   - **编辑图形时多一条模板条**（AnnotationEditTemplates）：选中图形后直接列出
 *     同类模板，点一下就换样式 —— 不用「先选模板再重画」。
 */
const store = useReplayStore()

const drawTools: DrawTool[] = ['trend', 'ray', 'rect', 'arrow', 'fib']

const drawHint: Record<DrawTool, string> = {
  trend: '趋势线：两次单击（起点 → 终点），按住 Shift 画水平线',
  ray: '射线：单击一下就画出一条水平朝右的射线；按住 Shift 改成两次单击画斜射线',
  rect: '矩形：两次单击定两个对角点',
  arrow: '箭头线：两次单击，终点带箭头；Shift 画水平箭头',
  fib: '斐波那契回撤：两次单击定价格区间，四条水平线 -1 / 0 / 1 / 2 自动排开',
}

const canEdit = computed(() => store.canOperate)
const operator = computed(() => store.operatorLabel)

/**
 * 【🎙 录音】能不能点。
 *
 * 和绘图按钮不一样：它**不看操作权** —— 打开浮窗的观众只是想听听别人讲了什么。
 * 真正要不要操作权由浮窗里的录音键和服务端决定（见 store.canVoice）。
 * 回放时灰掉：那段历史是只读的，往上面挂语音没有意义。
 */
const canVoicePanel = computed(() => !!store.instance && !store.playbackActive)
const voiceHint = computed(() => {
  if (store.playbackActive) return '回放是只读的：退出回放后才能录音'
  if (!store.instance) return '先进入一个实例，才能在某根 K 线上录语音'
  if (!store.canOperate) return '语音讲解：每根 K 线最多 9 条（现在是观看状态，可以听、不能录）'
  return '语音讲解：空格开始/结束录音，挂在这根 K 线上（每根最多 9 条；随时能录，不必等开仓）'
})
/** 当前是不是绘图工具 —— 模板条只在绘图工具下出现（v-if 用得到，也顺手做类型收窄） */
const drawKind = computed<DrawTool | null>(() => (isDrawTool(store.tool) ? store.tool : null))

const hasMoreHint = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  if (inst.hasMore) return `已加载 ${inst.bars.length + inst.hiddenBars} 根，还剩 ${inst.hiddenBars} 根未揭示`
  return `已加载 ${inst.bars.length + inst.hiddenBars} 根，数据目录里没有更晚的了`
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

/** 点下拉框外面就收起：面板是浮在画布上的，不收起会一直挡住 K 线 */
function onDocPointerDown(ev: PointerEvent): void {
  if (!indicatorOpen.value) return
  const box = indicatorBox.value
  if (box && !box.contains(ev.target as Node)) indicatorOpen.value = false
}

/** Esc 也收起（和画布上「Esc 退一层」的习惯一致；这里只退下拉框这一层） */
function onDocKeyDown(ev: KeyboardEvent): void {
  if (ev.key === 'Escape' && indicatorOpen.value) indicatorOpen.value = false
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
  <div class="flex flex-wrap items-center gap-2 border-b border-line px-2 py-1.5">
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
    -->
    <AnnotationEditTemplates
      v-else-if="store.editingAnnotation"
      :annotation="store.editingAnnotation"
    />

    <!--
      语音讲解（v0.16.0）：**和上面的绘图工具同级**，不再是录制的子功能。
      点一下拿起麦克风（浮窗打开、空格开始录），再点一下放下。
      入口对所有人开放 —— 观众也要能听讲解；能不能**录**由浮窗里和服务端一起判
      （要操作权、不能在回放里），所以这里只按「有没有盘面」灰掉。
    -->
    <button
      type="button"
      :class="[btn, store.voiceOpen ? btnActive : btnIdle]"
      :disabled="!canVoicePanel"
      :title="voiceHint"
      @click="store.toggleVoicePanel()"
    >
      🎙 录音
      <span class="tnum ml-1 text-[11px]">{{ store.voiceClips.length }}/{{ store.voiceMax }}</span>
    </button>

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

    <button
      type="button"
      class="rounded bg-emerald-600 px-3 py-1 text-[13px] font-medium text-white hover:bg-emerald-500 disabled:cursor-not-allowed disabled:opacity-40"
      :disabled="!canEdit || !store.instance || (store.instance?.bars.length ?? 0) === 0"
      title="揭示下一根 K 线（会按这根的最高/最低价撮合挂单）；键盘 → 也是这个动作"
      @click="store.step()"
    >
      下一根 →
    </button>

    <!--
      这里**没有【录制】按钮**（v0.18.0 删掉了）：交易记录是自动的 ——
      仓位从空仓变成有仓的那一刻开始记，回到空仓的那一刻收尾。
    -->

    <button type="button" :class="[btn, btnIdle]" :disabled="!store.isOperator" @click="store.releaseControl()">
      交还操作权
    </button>
    <button
      type="button"
      :class="[btn, 'border-amber-500 text-amber-600 hover:bg-amber-500/10']"
      :disabled="store.isOperator || !store.instance || (store.instance?.operator ?? '') !== '' || store.playbackActive"
      @click="store.takeControl()"
    >
      接管
    </button>

    <!-- 机器人的启动入口单独一个组件：里面要选策略、调参数，塞进这一行太挤 -->
    <BotMenu />

    <div class="ml-auto flex items-center gap-3 text-[12px] text-dim">
      <span v-if="store.playbackActive" class="text-down">回放模式（只读）</span>
      <span v-else-if="store.instance">
        操作权：<b :class="store.isOperator ? 'text-up' : 'text-amber-600'">
          {{ operator || '无人' }}
        </b>
      </span>
      <span v-if="store.instance" class="text-faint">{{ hasMoreHint }}</span>
    </div>
  </div>
</template>
