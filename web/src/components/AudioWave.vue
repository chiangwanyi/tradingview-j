<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import { cachedPeaks, clipPeaks } from '@/audio/waveform'

/**
 * 一段录音的波形（响度包络）。
 *
 * 用 SVG 而不是 canvas：这种东西同时会有好几个挂在页面上（本步可能有好几条），
 * canvas 每个都要自己的绘制循环和像素比处理，SVG 交给浏览器一次画完就行 ——
 * 90 个点的路径对它是小事。
 *
 * 数据来自 `audio/waveform.ts` 的缓存：同一个 URL 在 HUD 和回放列表里各画一次，
 * 只解码一次。
 */
const props = withDefaults(
  defineProps<{
    url: string
    /** 正在播放：换成琥珀色，和「哪个在响」对上 */
    active?: boolean
  }>(),
  { active: false },
)

const peaks = ref<number[] | null>(cachedPeaks(props.url))

watch(
  () => props.url,
  (url) => {
    peaks.value = cachedPeaks(url)
    if (!peaks.value && url) void load(url)
  },
  { immediate: true },
)

async function load(url: string): Promise<void> {
  const out = await clipPeaks(url)
  // 中途换了 url（列表刷新）就丢掉这次结果
  if (props.url === url) peaks.value = out
}

/** 上下对称的包络路径：从左到右画上沿，再倒着画下沿，闭合成一块。 */
const d = computed(() => {
  const p = peaks.value
  if (!p || p.length === 0) return ''
  const mid = 50
  let top = ''
  let bottom = ''
  for (let i = 0; i < p.length; i++) {
    const x = (((i + 0.5) / p.length) * 100).toFixed(2)
    // 有下限：完全静音也留一条细线，否则「录到了但没声音」看起来像「没录上」
    const h = Math.max(1.6, Math.min(1, p[i]) * 46).toFixed(2)
    top += `${i === 0 ? 'M' : 'L'}${x},${(mid - Number(h)).toFixed(2)}`
    bottom = `L${x},${(mid + Number(h)).toFixed(2)}` + bottom
  }
  return `${top}${bottom}Z`
})
</script>

<template>
  <svg class="h-full w-full" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden="true">
    <path v-if="d" :d="d" :class="active ? 'fill-amber-500' : 'fill-current'" />
    <line
      v-else
      x1="0"
      y1="50"
      x2="100"
      y2="50"
      class="stroke-current opacity-30"
      stroke-width="1"
      vector-effect="non-scaling-stroke"
    />
  </svg>
</template>
