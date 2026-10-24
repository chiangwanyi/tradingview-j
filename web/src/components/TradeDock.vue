<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import EventLog from '@/components/EventLog.vue'
import OrderPanel from '@/components/OrderPanel.vue'
import PlaybackPanel from '@/components/PlaybackPanel.vue'
import { usePlaybackStore } from '@/stores/playback'
import { useReplayStore } from '@/stores/replay'

/**
 * 底部停靠区：模拟交易 / 操作记录 / 回放 三个 Tab（需求要求「以 Tab 切换显示」）。
 *
 * 两个面板都用 v-show 而不是 v-if —— 切 Tab 时不该丢掉滚动位置，
 * 操作记录也要能继续自动滚到最新一条。
 *
 * 可以整体收起（只留 Tab 条），这样想全力看盘时画布能再高 180px。
 *
 * v0.18.0：第三个 Tab 从【回放】改名【交易记录】—— 它本来就不是「录像」。
 * v0.19.0：又把**记录列表**搬走了，这个 Tab 回到只做【回放】这件事：
 * 列表、统计、图表都在 `/records`（见 views/RecordsView.vue）。底部这 180px
 * 放不下「看清一段时间的战绩」，却刚好够放「把这一笔演一遍」。
 */

const store = useReplayStore()
const pb = usePlaybackStore()
type DockTab = 'trade' | 'log' | 'rec'
const tab = ref<DockTab>('trade')
const collapsed = ref(false)

const tabClass = (key: DockTab) =>
  [
    'rounded-t px-3 py-1 text-[12px] transition',
    tab.value === key && !collapsed.value
      ? 'bg-elev text-ink shadow-[inset_0_1px_0_var(--c-line)]'
      : 'text-dim hover:text-ink',
  ].join(' ')

const summary = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  const p = inst.position
  const pos =
    p.side === 'flat' ? '空仓' : `${p.side === 'long' ? '多' : '空'} ${Math.abs(p.quantity)}`
  return `${pos} · 挂单 ${store.openOrders.length} · 成交 ${inst.trades.length}`
})

/** 回放一开始就自动切到这个 Tab：用户点的是「回放」，不该还要自己找播放键。 */
function enterPlayback(): void {
  tab.value = 'rec'
  collapsed.value = false
}

// 从交易记录页点【回放】跳回来时，Tab 条可能停在其他地方 —— 播放器已经在跑了，
// 面板却还藏着，用户会以为「点了没反应」。所以由「开始回放」这个事实来切 Tab。
watch(
  () => pb.active,
  (on) => {
    if (on) enterPlayback()
  },
  { immediate: true },
)
</script>

<template>
  <section class="shrink-0 border-t border-line bg-panel">
    <div class="flex h-8 items-center gap-1 px-2">
      <button
        type="button"
        :class="tabClass('trade')"
        @click="((tab = 'trade'), (collapsed = false))"
      >
        模拟交易
      </button>
      <button type="button" :class="tabClass('log')" @click="((tab = 'log'), (collapsed = false))">
        操作记录
        <span class="text-faint">({{ store.log.length }})</span>
      </button>
      <button
        type="button"
        :class="tabClass('rec')"
        :title="pb.active ? '当前正在回放的那一条' : '回放一条交易记录（重新演在画布上）'"
        @click="enterPlayback"
      >
        回放
        <span v-if="pb.active" class="text-faint">({{ pb.step }}/{{ pb.total }})</span>
      </button>

      <span class="ml-auto truncate text-[11px] text-faint">{{ summary }}</span>

      <!--
        交易记录的正门：底部这一条放不下统计和图表，只留一个跳转。
        列表条数写在按钮上 —— 让「有没有新记录」不点也能看见。
      -->
      <RouterLink
        :to="{ name: 'records' }"
        class="ml-2 flex shrink-0 items-center gap-1 rounded border border-line px-1.5 py-0.5 text-[11px] text-dim transition hover:border-accent hover:text-accent"
        title="打开交易记录页：统计图表 + 全部明细（回放也从那里挑）"
      >
        交易记录 · 统计
        <span class="tnum rounded bg-elev px-1 text-[10px] text-faint">{{
          store.recordings.length
        }}</span>
        ↗
      </RouterLink>

      <button
        type="button"
        class="ml-2 rounded border border-line px-1.5 py-0.5 text-[11px] text-dim hover:border-accent hover:text-accent"
        @click="collapsed = !collapsed"
      >
        {{ collapsed ? '展开面板 ▲' : '收起面板 ▼' }}
      </button>
    </div>

    <div v-show="!collapsed" class="h-[12.75rem] border-t border-line">
      <OrderPanel v-show="tab === 'trade'" />
      <EventLog v-show="tab === 'log'" />
      <PlaybackPanel v-show="tab === 'rec'" />
    </div>
  </section>
</template>
