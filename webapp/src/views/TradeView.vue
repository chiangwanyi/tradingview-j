<script setup lang="ts">
import { onBeforeUnmount, onMounted, watch } from 'vue'

import ChartPanel from '@/components/ChartPanel.vue'
import ChartToolbar from '@/components/ChartToolbar.vue'
import Icon from '@/components/Icon.vue'
import TradeDock from '@/components/TradeDock.vue'
import TradeFloat from '@/components/TradeFloat.vue'
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
    <!-- 只留一个返回入口：顶栏上原来那一排（账户名 / 阶段 / 余额 / 回撤 / 标的 / 日期）全部去掉，
         盘面本身才是这一页的主体 -->
    <header class="flex min-h-9 shrink-0 items-center gap-3 border-b border-line bg-panel px-3 py-1">
      <a
        href="/"
        class="flex items-center gap-1 whitespace-nowrap text-[13px] text-dim transition-colors hover:text-ink"
        title="回到账户总览（这个标签页可以直接关掉）"
      >
        <Icon name="left" :size="13" />返回账户
      </a>
      <span v-if="store.accountName" class="truncate text-[13px] text-ink">{{ store.accountName }}</span>
      <span v-if="store.loading" class="text-[13px] text-faint">盘面加载中…</span>
      <span
        v-else-if="store.socketState === 'closed'"
        class="text-[13px] text-amber-700"
        title="连接断了，正在自动重连；重连成功后会按后端的样子重建盘面"
      >
        连接断开，重连中…
      </span>
    </header>

    <main class="relative flex min-h-0 flex-1 flex-col">
      <ChartToolbar />
      <div class="relative min-h-0 flex-1">
        <ChartPanel />

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

    <!-- 下单浮窗：可拖动、可折叠；这里没有回放，所以不用像 web/ 那样按条件隐藏 -->
    <TradeFloat />
  </div>
</template>
