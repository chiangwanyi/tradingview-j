<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import EventLog from '@/components/EventLog.vue'
import OrderPanel from '@/components/OrderPanel.vue'
import PlaybackPanel from '@/components/PlaybackPanel.vue'
import { usePlaybackStore } from '@/stores/playback'
import { useReplayStore } from '@/stores/replay'

/**
 * 底部停靠区：模拟交易 / 操作记录 两个 Tab + 回放中的播放器。
 *
 * 两个面板都用 v-show 而不是 v-if —— 切 Tab 时不该丢掉滚动位置，
 * 操作记录也要能继续自动滚到最新一条。
 *
 * 可以整体收起（只留 Tab 条），这样想全力看盘时画布能再高 200px。
 *
 * v0.18.0：第三个 Tab 从【回放】改名【交易记录】—— 它本来就不是「录像」。
 * v0.19.0：又把**记录列表**搬走了，那个 Tab 回到只做【回放】这件事。
 * v0.20.0：**干脆不要这个 Tab 了**。平时它只是一行「现在没有在回放」的空文案，
 * 占着 Tab 条却没有任何能点的事；真正要用它的时候（从交易记录页点【回放】），
 * 播放器本来就应该自己顶上来。所以现在：
 *   · 没在回放 → Tab 条上只有【模拟交易】【操作记录】；
 *   · 正在回放 → Tab 条上多出一个红色的【回放中 x/y】，面板自动切到播放器，
 *     中途切去看持仓/流水，点这一枚红色的就回到播放器。
 */

const store = useReplayStore()
const pb = usePlaybackStore()
type DockTab = 'trade' | 'log' | 'player'
const tab = ref<DockTab>('trade')
const collapsed = ref(false)

const tabClass = (key: DockTab) =>
  [
    'whitespace-nowrap rounded-t px-3 py-1 text-[13px] transition',
    tab.value === key && !collapsed.value
      ? 'bg-elev text-ink shadow-[inset_0_1px_0_var(--c-line)]'
      : 'text-dim hover:text-ink',
  ].join(' ')

/** 回放中的那一枚：红色，和「实时」区分开；选中时同样抬成底色。 */
const playerTabClass = () =>
  [
    'whitespace-nowrap rounded-t px-3 py-1 text-[13px] transition',
    tab.value === 'player' && !collapsed.value
      ? 'bg-elev text-red-600 shadow-[inset_0_1px_0_var(--c-line)] dark:text-red-400'
      : 'text-red-600/70 hover:text-red-600 dark:text-red-400/70 dark:hover:text-red-400',
  ].join(' ')

const summary = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  const p = inst.position
  const pos =
    p.side === 'flat' ? '空仓' : `${p.side === 'long' ? '多' : '空'} ${Math.abs(p.quantity)}`
  return `${pos} · 挂单 ${store.openOrders.length} · 成交 ${inst.trades.length}`
})

/** 回放一开始就把播放器顶到当前面板：用户点的是「回放」，不该还要自己找播放键。 */
function enterPlayback(): void {
  tab.value = 'player'
  collapsed.value = false
}

// 两个时机都要管：从交易记录页跳回来时播放器已经在跑了，面板却还藏着，
// 用户会以为「点了没反应」；回放结束了（退出回放 / 播完）则把面板还给模拟交易，
// 否则会停在一个已经不存在的 Tab 上。
watch(
  () => pb.active,
  (on) => {
    if (on) enterPlayback()
    else if (tab.value === 'player') tab.value = 'trade'
  },
  { immediate: true },
)
</script>

<template>
  <section class="shrink-0 border-t border-line bg-panel">
    <div class="flex h-9 items-center gap-1 overflow-x-auto px-2">
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
        v-if="pb.active"
        type="button"
        :class="playerTabClass()"
        title="回到回放播放器（点【退出回放】结束这一条）"
        @click="enterPlayback"
      >
        回放中
        <span class="text-faint">({{ pb.step }}/{{ pb.total }})</span>
      </button>

      <span class="ml-auto hidden truncate text-[12px] text-faint sm:inline">{{ summary }}</span>

      <!--
        交易记录的正门：底部这一条放不下统计和图表，只留一个跳转。
        列表条数写在按钮上 —— 让「有没有新记录」不点也能看见。
      -->
      <RouterLink
        :to="{ name: 'records' }"
        class="ml-2 flex shrink-0 items-center gap-1 rounded border border-line px-2 py-0.5 text-[12px] text-dim transition hover:border-accent hover:text-accent"
        title="打开交易记录页：统计图表 + 全部明细（回放也从那里挑）"
      >
        交易记录 · 统计
        <span class="tnum rounded bg-elev px-1 text-[11px] text-faint">{{
          store.recordings.length
        }}</span>
        ↗
      </RouterLink>

      <button
        type="button"
        class="ml-2 rounded border border-line px-2 py-0.5 text-[12px] text-dim hover:border-accent hover:text-accent"
        @click="collapsed = !collapsed"
      >
        {{ collapsed ? '展开面板 ▲' : '收起面板 ▼' }}
      </button>
    </div>

    <div v-show="!collapsed" class="h-[14.5rem] border-t border-line">
      <OrderPanel v-show="tab === 'trade'" />
      <EventLog v-show="tab === 'log'" />
      <PlaybackPanel v-if="pb.active" v-show="tab === 'player'" />
    </div>
  </section>
</template>
