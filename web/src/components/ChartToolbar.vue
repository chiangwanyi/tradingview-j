<script setup lang="ts">
import { computed } from 'vue'

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
 * 点空白/点 K 线就退出。两个按钮删掉之后，工具栏上不会再有「当前在哪个模式」这种
 * 需要用户自己记、又容易和实际行为不一致的开关。
 *
 * 射线是唯一**单击即完成**的：点一下就是一条水平朝右的射线（预览跟着鼠标走），
 * 因为「水平线 + 价位」是复盘里最常用的一笔，两次单击纯属多余动作。
 * 其余四种是两次单击。绘图工具选中后，右边会跟出一排模板 ——
 * 那是这个工具自己的样式预设（各工具互不相干）。
 *
 * v0.16.0 起这一行多了【🎙 录音】：语音讲解从录制的子功能提升为**一级功能**，
 * 和五种绘图工具平起平坐 —— 拿笔是「在这根 K 线上画点什么」，拿麦克风是
 * 「在这根 K 线上讲两句」，两件事都发生在当前这根 K 线上，都**不需要先开录像**。
 *
 * v0.18.0 删掉了【● 录制】：交易记录改成开仓自动开始、平仓自动结束，
 * 用户不需要（也不应该）记得按一个开关 —— 见 views/RecordsView.vue。
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

    <button
      type="button"
      :class="[btn, store.showEma ? 'border-accent text-accent' : btnIdle]"
      title="显示/隐藏 EMA20（指标值由后端算好，这里只是开关）"
      @click="store.showEma = !store.showEma"
    >
      EMA20
    </button>

    <div class="h-4 w-px bg-line" />

    <!--
      Doji X + Blue ZigZag：一键开关 + 两个参数。
      参数名沿用 Pine 脚本里的 input 名（Lag / Doji Size），
      这样对着原始脚本核数的时候不用再翻译一层。
    -->
    <div class="flex items-center gap-1">
      <button
        type="button"
        :class="[btn, store.indicator.show ? btnActive : btnIdle]"
        title="Doji X + Blue ZigZag：蓝点标出动量转折（上穿画最低点、下穿画最高点），✖ 标出十字星（阳线在上、阴线在下）"
        @click="store.setIndicator({ show: !store.indicator.show })"
      >
        Doji X + ZigZag
      </button>
      <label
        class="flex items-center gap-1 text-[12px] text-dim"
        title="Lag：EMA 周期，同时是高低点的回看窗口（Pine 的 lookback，2..50）"
      >
        Lag
        <input
          type="number"
          min="2"
          max="50"
          step="1"
          :class="num"
          :value="store.indicator.lookback"
          @change="onLag"
        />
      </label>
      <label
        class="flex items-center gap-1 text-[12px] text-dim"
        title="Doji Size：实体不超过振幅的这个比例就算十字星（Pine 的 dojiSize，0.01..1）"
      >
        Doji
        <input
          type="number"
          min="0.01"
          max="1"
          step="0.01"
          :class="num"
          :value="store.indicator.dojiSize"
          @change="onDojiSize"
        />
      </label>
    </div>

    <div class="h-4 w-px bg-line" />

    <button
      type="button"
      class="rounded bg-emerald-600 px-3 py-1 text-[13px] font-medium text-white hover:bg-emerald-500 disabled:cursor-not-allowed disabled:opacity-40"
      :disabled="!canEdit || !store.instance || (store.instance?.bars.length ?? 0) === 0"
      title="揭示下一根 K 线（会按这根的最高/最低价撮合挂单）"
      @click="store.step()"
    >
      下一根 →
    </button>

    <!--
      这里**没有【录制】按钮**（v0.18.0 删掉了）：交易记录是自动的 ——
      仓位从空仓变成有仓的那一刻开始记，回到空仓的那一刻收尾。
      录制不再是用户要记得去做的一件事，自然也不该有一个要记得去按的开关。
    -->

    <button type="button" :class="[btn, btnIdle]" :disabled="!store.isOperator" @click="store.releaseControl()">
      交还操作权
    </button>
    <button
      type="button"
      :class="[btn, 'border-amber-500 text-amber-600 hover:bg-amber-500/10 dark:text-amber-400']"
      :disabled="store.isOperator || !store.instance || (store.instance?.operator ?? '') !== '' || store.playbackActive"
      @click="store.takeControl()"
    >
      接管
    </button>

    <!-- 机器人的启动入口单独一个组件：里面要选策略、调参数，塞进这一行太挤 -->
    <BotMenu />

    <div class="ml-auto flex items-center gap-3 text-[12px] text-dim">
      <span v-if="store.playbackActive" class="text-red-600 dark:text-red-400">回放模式（只读）</span>
      <span v-else-if="store.instance">
        操作权：<b :class="store.isOperator ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">
          {{ operator || '无人' }}
        </b>
      </span>
      <span v-if="store.instance" class="text-faint">{{ hasMoreHint }}</span>
    </div>
  </div>
</template>
