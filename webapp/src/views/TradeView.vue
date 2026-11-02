<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch } from 'vue'

import ChartPanel from '@/components/ChartPanel.vue'
import ChartToolbar from '@/components/ChartToolbar.vue'
import DrawToolbarFloat from '@/components/DrawToolbarFloat.vue'
import Icon from '@/components/Icon.vue'
import TradeDock from '@/components/TradeDock.vue'
import { usd } from '@/data/rules'
import { useTradeStore } from '@/stores/trade'

/**
 * 交易页：一个账户 + 一块盘面。
 *
 * 入口只有一个 —— 账户卡片的【继续交易】，它用 `window.open` 开一个新标签页
 * （`/trade/:id`）。为什么要新标签页而不是在应用里切页：一人可能同时盯两三个账户，
 * 浏览器标签天然支持并排、各自记住缩放和工具，关掉一个也不影响另一个。
 *
 * ## 这一页的数据从哪来
 *
 * 全部来自一条 WebSocket（`/ws`，见 `data/socket.ts` 和 `stores/trade.ts` 的 `connect`）：
 * 进页面发一条 `hello{accountId}`，后端回一份快照（账户名 + 标的 + K 线窗口 +
 * 还有没有下一根），之后每次【下一根】都是一条广播。**这一页没有 HTTP 请求** ——
 * 账户停在哪个位置、窗口从哪一天开始、下一根是哪根，这三件事互相牵连，
 * 分给几个接口去取就会出现「A 的结果配 B 的参数」。
 *
 * ## 下单做什么、不做什么
 *
 * 市价 / 限价 / 止损的提交、撤单、改价、一键退仓都走这条 WebSocket：**发一条意图，
 * 后端撮合并落库，广播回来照抄**（见 `stores/trade.ts`）。本根挂的单从下一根才撮合，
 * 手续费按成交那一刻的费率快照进成交 —— 前端一处钱都不算，只排版。
 *
 * 画线只活在这个标签页的内存里：后端还没接 `annotation_*`，刷新即失。
 */

const props = defineProps<{ id: string }>()

const store = useTradeStore()

/**
 * 距亏损线还剩多少 = 权益 − 亏损线。
 *
 * 用**权益**而不是余额：持仓浮亏已经在扣了，只盯余额会在盘中给出一个偏乐观的缓冲。
 * 这个减法在前端做是安全的 —— 两个数都来自同一次重放，没有口径问题；
 * 但「亏损线在哪」不能自己算（EOD 追踪有一道冻结水位，见 `data/rules.ts` 的 `lossLine`）。
 */
const remaining = computed(() => {
  const pf = store.pf
  return pf ? pf.equity - pf.lossLine : null
})

/**
 * 顶栏上的盘面概况：`标的 · 当前这根的时间 · 这一天加载了几根`。
 *
 * 原来这一串挂在工具条最右边（`ChartToolbar` 的 `ml-auto` 那一块），2026-11-02 挪到了
 * 账户名旁边：它描述的是「这个账户现在在看哪张图」，和账户名是同一件事的两半，
 * 分在两个角落会让人先在屏幕上找一遍。
 */
const sceneLine = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  const rest = store.restOfDay > 0 ? `（这一天还剩 ${store.restOfDay} 根）` : ''
  return `${inst.symbol} · ${store.currentTime || store.day} · ${inst.bars.length} 根${rest}`
})

/** 连接状态的小圆点：绿=通、黄=正在连、红=断了在重连。断线时用户必须一眼看出来。 */
const linkDot = computed(() =>
  store.socketState === 'open'
    ? 'bg-emerald-500'
    : store.socketState === 'connecting'
      ? 'bg-amber-400'
      : 'bg-red-400',
)
const linkText = computed(() =>
  store.socketState === 'open'
    ? '已连接'
    : store.socketState === 'connecting'
      ? '连接中…'
      : '重连中…',
)
const linkTitle = computed(() =>
  store.socketState === 'open'
    ? '交易服务已连接（WebSocket /ws）'
    : '连接断了，正在自动重连；重连成功后会按后端的样子重建盘面',
)

onMounted(() => {
  document.title = '交易 · webapp'
  // 连上就开始：账户名要等快照回来才知道，所以标题在 watch 里补
  store.connect(props.id)
})

// 断线的时候标签页上要能看出来（切回来一眼就知道「现在看到的不是实时的」）
watch(
  () => store.accountName,
  (name) => {
    document.title = name ? `交易 · ${name}` : '交易 · webapp'
  },
  { immediate: true },
)

onBeforeUnmount(() => store.disconnect())
</script>

<template>
  <div class="flex h-screen flex-col bg-surface text-ink">
    <!-- 顶栏分两段：左边是「我在看什么」（返回、账户名、标的 / 时间 / 根数、连接状态、
         阶段状态），右边是「我有多危险」（余额、亏损线、还剩多少缓冲）。
         盘面概况原来挂在工具条右端，2026-11-02 挪到账户名旁边 —— 它和账户名本来是一件事。
         右边那三个数是**唯一**不该被藏起来的：缺了它们，盘中就不知道自己还有多危险。 -->
    <header class="flex min-h-9 shrink-0 items-center gap-3 border-b border-line bg-panel px-3 py-1">
      <a
        href="/"
        class="flex items-center gap-1 whitespace-nowrap text-[13px] text-dim transition-colors hover:text-ink"
        title="回到账户总览（这个标签页可以直接关掉）"
      >
        <Icon name="left" :size="13" />返回账户
      </a>
      <span v-if="store.accountName" class="truncate text-[13px] text-ink">{{ store.accountName }}</span>

      <!-- 盘面概况（标的 / 当前 K 线时间 / 根数）：和账户名挨着 —— 说的就是「这个账户在看哪张图」 -->
      <span
        v-if="store.instance"
        class="tnum truncate whitespace-nowrap text-[12px] text-faint"
        :title="`当前标的 ${store.symbolCode} · 盘面走到 ${store.currentTime || store.day}`"
        >{{ sceneLine }}</span
      >

      <span v-if="store.loading" class="text-[13px] text-faint">盘面加载中…</span>

      <!-- 连接状态（绿点=通）：放在账户名这一侧，断线一眼可见 -->
      <span
        class="flex shrink-0 items-center gap-1.5 whitespace-nowrap text-[12px]"
        :class="store.socketState === 'open' ? 'text-faint' : 'text-amber-700'"
        :title="linkTitle"
      >
        <span class="h-1.5 w-1.5 rounded-full" :class="linkDot" />
        {{ linkText }}
      </span>

      <!-- 考核状态：失败 / 已达标待升级都意味着「不能再下单」，必须在按按钮之前就看到 -->
      <span
        v-if="store.pf?.status === 'FAILED'"
        class="rounded-sm border border-red-200 bg-red-50 px-1.5 py-0.5 text-[11px] tracking-wider text-red-700"
        :title="store.pf?.hint ?? '权益已触及亏损线'"
        >已失败</span
      >
      <span
        v-else-if="store.pf?.frozen"
        class="rounded-sm border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-[11px] tracking-wider text-amber-800"
        :title="store.pf?.hint ?? '已达成本阶段升级条件'"
        >已达标 · 待升级</span
      >

      <!-- 三个考核读数：数字全部由后端算（它才是唯一口径），这里只排版 -->
      <div v-if="store.pf" class="ml-auto flex items-center gap-3 whitespace-nowrap text-[12px] tnum">
        <span class="text-faint">余额</span>
        <span class="text-[13px] font-semibold text-ink">{{ usd(store.pf.balance) }}</span>
        <span class="text-faint">亏损线</span>
        <span class="text-[13px] text-dim">{{ usd(store.pf.lossLine) }}</span>
        <span class="text-faint">距亏损线</span>
        <span
          class="text-[13px] font-semibold"
          :class="remaining != null && remaining <= 0 ? 'text-red-600' : 'text-ink'"
          :title="store.pf.hint ?? ''"
        >
          {{ remaining == null ? '—' : `${remaining < 0 ? '-' : ''}${usd(Math.abs(remaining))}` }}
        </span>
      </div>
    </header>

    <main class="relative flex min-h-0 flex-1 flex-col">
      <!-- 工具条：周期 / 指标在左，下单条在同一行的右端（`OrderBar` 由 `ChartToolbar` 排）
           下划线由这个包裹层出，这样「有没有盘面」都不会多出一条缝。 -->
      <div class="shrink-0 border-b border-line">
        <ChartToolbar />
      </div>
      <div class="relative min-h-0 flex-1">
        <ChartPanel />
        <DrawToolbarFloat />

        <!-- 一天都没有 K 线（休市 / 没抓）时别留一块白板，说清楚是什么情况 -->
        <div
          v-if="store.instance && store.bars.length === 0"
          class="pointer-events-none absolute inset-0 flex items-center justify-center"
        >
          <p class="rounded border border-line bg-panel/95 px-4 py-3 text-[13px] text-dim shadow">
            {{ store.symbolCode }} 在 {{ store.day }} 这一屏还没有 K 线（休市、没抓，或者账户的位置还在这一天之前）。
          </p>
        </div>
      </div>

      <TradeDock />
    </main>

    <!-- 轻提示：本地动作（存模板、画线提示）的结果 -->
    <div
      v-if="store.notice"
      :key="store.noticeKey"
      class="absolute right-2 bottom-2 z-40 max-w-lg rounded border border-line bg-panel px-3 py-2 text-[13px] text-dim shadow-lg"
    >
      <div class="flex items-start gap-3">
        <span class="flex-1">{{ store.notice }}</span>
        <button type="button" class="text-faint hover:text-ink" @click="store.clearNotice()">关闭</button>
      </div>
    </div>

    <!-- 错误：后端没接通这类事必须说清楚，不能静默 -->
    <div
      v-if="store.lastError"
      :key="store.errorKey"
      class="absolute right-2 bottom-2 z-50 max-w-md rounded border border-red-300 bg-red-50/95 px-3 py-2 text-[13px] text-red-800 shadow-lg"
    >
      <div class="flex items-start gap-3">
        <span class="flex-1">{{ store.lastError }}</span>
        <button type="button" class="text-red-500 hover:opacity-70" @click="store.clearError()">关闭</button>
      </div>
    </div>
  </div>
</template>
