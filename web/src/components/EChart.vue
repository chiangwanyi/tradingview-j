<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref, watch } from 'vue'

import echarts from '@/lib/chart'
import type { EChartsCoreOption } from 'echarts/core'

/**
 * 一块 ECharts 画布的最小封装。
 *
 * 只做三件事：挂载时 init、option 变了就 setOption、容器尺寸变了就 resize；
 * 卸载时 dispose。**不接管业务** —— 每张图的 option 都由
 * `components/records/chart-options.ts` 那些纯函数算出来，这里不认识交易。
 *
 * `setOption` 第二个参数固定 true（notMerge）：这几张图的数据量会随筛选条件整批换掉，
 * 合并旧数据会留下「上一份筛选里的柱子」。图表本来就不大，重设一遍比清残留便宜。
 *
 * resize 用 ResizeObserver 而不是监听 window：卡片宽度会随网格布局（响应式列数）变，
 * 窗口大小没变的时候图表照样需要重排。
 */
const props = defineProps<{
  option: EChartsCoreOption
  /** 容器高度，默认撑满父级 */
  height?: string
}>()

const el = ref<HTMLDivElement | null>(null)
let chart: ReturnType<typeof echarts.init> | null = null
let ro: ResizeObserver | null = null

onMounted(() => {
  if (!el.value) return
  chart = echarts.init(el.value, undefined, { renderer: 'canvas' })
  chart.setOption(props.option)
  ro = new ResizeObserver(() => chart?.resize())
  ro.observe(el.value)
})

watch(
  () => props.option,
  (opt) => {
    // 数据全空时 setOption 会让图表保留上一次的坐标轴范围，先 clear 更干净
    chart?.clear()
    chart?.setOption(opt)
  },
  { deep: true },
)

onBeforeUnmount(() => {
  ro?.disconnect()
  ro = null
  chart?.dispose()
  chart = null
})
</script>

<template>
  <div ref="el" class="w-full" :style="{ height: height ?? '100%' }" />
</template>
