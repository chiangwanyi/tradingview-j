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
/**
 * 【录制】能不能点。
 *
 * 两个分支不一样：开始录制要求「有操作权 + 有 K 线」；结束录制只看操作权 ——
 * 万一录到一半操作权被交还了，还留在录制状态里的人至少能把它停下来保存。
 */
const canRecord = computed(() => {
  const inst = store.instance
  if (!inst || store.playbackActive || !store.canOperate) return false
  return store.isRecording || inst.bars.length > 0
})
const operator = computed(() => store.operatorLabel)
/** 当前是不是绘图工具 —— 模板条只在绘图工具下出现（v-if 用得到，也顺手做类型收窄） */
const drawKind = computed<DrawTool | null>(() => (isDrawTool(store.tool) ? store.tool : null))

const hasMoreHint = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  if (inst.hasMore) return `已加载 ${inst.bars.length + inst.hiddenBars} 根，还剩 ${inst.hiddenBars} 根未揭示`
  return `已加载 ${inst.bars.length + inst.hiddenBars} 根，数据目录里没有更晚的了`
})

const btn = 'rounded border border-line px-2 py-1 text-[12px] transition disabled:cursor-not-allowed disabled:opacity-40'
const btnIdle = 'bg-panel text-dim hover:border-accent hover:text-accent'
const btnActive = 'border-accent bg-accent-soft text-accent'
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

    <button
      type="button"
      class="rounded bg-emerald-600 px-3 py-1 text-[12px] font-medium text-white hover:bg-emerald-500 disabled:cursor-not-allowed disabled:opacity-40"
      :disabled="!canEdit || !store.instance || (store.instance?.bars.length ?? 0) === 0"
      title="揭示下一根 K 线（会按这根的最高/最低价撮合挂单）"
      @click="store.step()"
    >
      下一根 →
    </button>

    <!--
      录制：点一下开始（当前盘面 = 第 1 步），再点一下结束并落盘。
      按钮文案在录制中变成【录制中…】—— 这是用户确认「正在被录」的唯一显式信号，
      所以不加图标式的开关，免得有人以为它是个切换显示。
    -->
    <button
      type="button"
      :class="[
        btn,
        store.isRecording
          ? 'border-red-500 bg-red-500/10 font-medium text-red-600 dark:text-red-400'
          : 'border-red-400 text-red-600 hover:bg-red-500/10 dark:text-red-400',
      ]"
      :disabled="!canRecord"
      :title="
        store.isRecording
          ? '结束录制并保存到录像目录（录像里是操作事件，不是视频）'
          : '开始录制：当前盘面记为第 1 步，之后每次【下一根】加一步；画图、下单、机器人操作都会被记下'
      "
      @click="store.toggleRecording()"
    >
      <span
        v-if="store.isRecording"
        class="mr-1 inline-block h-2 w-2 animate-pulse rounded-full bg-red-500 align-middle"
      />
      {{ store.isRecording ? '录制中…' : '● 录制' }}
    </button>

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

    <div class="ml-auto flex items-center gap-3 text-[11px] text-dim">
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
