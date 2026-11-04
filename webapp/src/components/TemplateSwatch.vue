<script setup lang="ts">
import { computed } from 'vue'

import { defaultLevels, resolveStyle } from '@/components/chart/annotations'
import type { AnnotationStyle } from '@/types/protocol'

/**
 * 模板的「一眼长什么样」缩略图：16×10 的一小条。
 *
 * 抽成组件是为了让**同一个模板在任何地方都长同一个样** —— 工具条上的模板条
 * （新建图形用）和编辑态的模板条（给已画的图形换样式用）都在列模板，
 * 两处各抄一份 SVG 的话，改一处配色另一处就会悄悄不一致。
 *
 * 只认样式，不认数据：斐波那契画四条水平、矩形画一个带填充的框、其余画一条线。
 */
const props = defineProps<{ kind: string; style?: AnnotationStyle }>()

const style = computed(() => resolveStyle({ kind: props.kind, style: props.style }))

/** 斐波那契是四条线四个颜色，其余图形只有「一个颜色」 */
const colors = computed(() =>
  props.kind === 'fib' ? (style.value.levels ?? defaultLevels()).map((lv) => lv.color) : [style.value.color],
)
</script>

<template>
  <svg width="16" height="10" viewBox="0 0 16 10" class="shrink-0">
    <template v-if="kind === 'fib'">
      <line
        v-for="(c, i) in colors"
        :key="i"
        x1="0"
        x2="16"
        :y1="1.5 + i * 2.4"
        :y2="1.5 + i * 2.4"
        :stroke="c"
        stroke-width="1.4"
      />
    </template>
    <template v-else-if="kind === 'rect'">
      <rect
        x="1"
        y="1"
        width="14"
        height="8"
        :stroke="colors[0]"
        :fill="style.fill"
        fill-opacity="0.35"
        stroke-width="1.2"
      />
    </template>
    <template v-else>
      <line
        x1="0"
        y1="5"
        x2="16"
        y2="5"
        :stroke="colors[0]"
        stroke-width="2"
        :stroke-dasharray="style.dash === 'dashed' ? '5 3' : ''"
      />
    </template>
  </svg>
</template>
