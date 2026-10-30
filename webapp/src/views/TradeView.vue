<script setup lang="ts">
import { onMounted, ref } from 'vue'

import ChartPanel from '@/components/ChartPanel.vue'
import ChartToolbar from '@/components/ChartToolbar.vue'
import Icon from '@/components/Icon.vue'
import TradeDock from '@/components/TradeDock.vue'
import TradeFloat from '@/components/TradeFloat.vue'
import * as accounts from '@/data/accounts'
import type { Account } from '@/data/accounts'
import { errorText } from '@/data/store'
import { useTradeStore } from '@/stores/trade'

/**
 * 交易页：一个账户 + 一块盘面。
 *
 * 入口只有一个 —— 账户卡片的【继续交易】，它用 `window.open` 开一个新标签页
 * （`/trade/:id`）。为什么要新标签页而不是在应用里切页：一人可能同时盯两三个账户，
 * 浏览器标签天然支持并排、各自记住缩放和工具，关掉一个也不影响另一个。
 *
 * ## 这一页现在能做什么、不能做什么
 *
 * 能：看真实的 K 线（**上一交易日整天 + 当天到账户记住的那一根**），缩放 / 平移 / 指标 / 周期切换、
 *     画线（趋势线/射线/矩形/箭头/斐波那契 + 模板）、按 nATR 算手数、把下单面板填好。
 *
 * **标的只有一个**：它在建账户时选定、写死在账户上，所以这一页没有标的选择器 ——
 * 只显示「已锁定」和是哪个品种（要换标的就新建账户）。
 *
 * **打开时停在哪一根**：账户的 `lastBarTime`（建户 = 起点，每步进一根就往后推一格）。
 * 这一步是「继续交易」的全部含义 —— 不是从头再来一遍，而是接着上次的位置。
 *
 * 不能：**下单、撤单、平仓**。撮合还在设计阶段（`README.md` §三），
 *     后端没有这些接口。所以那些按钮点下去只给一句明确提示，不产生假数据 ——
 *     账由后端算，前端先造一笔成交会让界面立刻和余额对不上。
 *
 * 画线只活在这个标签页的内存里：刷新即失。
 */

const props = defineProps<{ id: string }>()

const store = useTradeStore()

const account = ref<Account | null>(null)
const accountErr = ref('')
const loadingAccount = ref(true)

onMounted(async () => {
  document.title = '交易 · webapp'
  try {
    account.value = await accounts.get(props.id)
    document.title = `交易 · ${account.value.name}`
  } catch (e) {
    accountErr.value = errorText(e)
  } finally {
    loadingAccount.value = false
  }

  const code = account.value?.symbolId
  if (!code) {
    // 账户都没读到（或旧库里没有标的列）就别去猜一个标的来画 —— 猜错了就是另一张图
    if (account.value) store.noteError('这个账户没有绑定标的，无法确定看哪个品种。')
    return
  }

  await store.loadSymbols()
  const meta = store.symbols.find((s) => s.code === code)
  if (!meta) {
    store.noteError(
      `账户的标的 ${code} 不在标的清单里（data/_symbols.json 里没有它），读不到 K 线。`,
    )
    return
  }
  if (meta.days === 0) {
    store.noteError(`标的 ${code} 还没有任何 K 线文件：先跑抓取程序录一段行情。`)
    return
  }

  // 步进要落库到这个账户（store 只管记 id，账户本身是这一页的数据）
  store.bindAccount(props.id, account.value?.lastBarTime)

  /*
   * 停在哪一根：优先用账户的 lastBarTime（旧库里这一列可能空着 → 退到起点）。
   * 交给后端的就是这一个时刻 —— 它会把上一个交易日的整天接在前面当隔夜参照。
   * 那一天的日文件可能已经不在了（被清理 / 该标的只抓到更早的日期），
   * 这时退到磁盘上最新的一天并整天全给 —— 宁可多给，也不要给一块空盘面。
   */
  const cursor = account.value?.lastBarTime || account.value?.startBarTime || ''
  const cursorDay = cursor.slice(0, 10)
  const inside =
    cursorDay && meta.firstDay && meta.lastDay && cursorDay >= meta.firstDay && cursorDay <= meta.lastDay
  if (inside) {
    await store.load(code, cursor.slice(0, 16))
  } else {
    await store.load(code, meta.lastDay ? `${meta.lastDay} 23:59` : undefined)
  }
})
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
      <span v-if="loadingAccount" class="text-[13px] text-faint">账户加载中…</span>
      <span v-else-if="!account" class="text-[13px] text-red-600">账户加载失败：{{ accountErr }}</span>
    </header>

    <!-- 常驻说明：交易后端还没接通、画线不落库 —— 这两件事不写在页面上，用户会以为是自己用错了 -->
    <div
      class="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-amber-200 bg-amber-50 px-3 py-1 text-[12px] text-amber-800"
    >
      <span class="font-medium">看盘模式</span>
      <span>下单 / 撤单 / 平仓还没接通（撮合后端在做），按钮只给提示，不会产生任何数据。</span>
      <span class="text-amber-700">按 → 或【下一根】往后放一根，账户会记住停在哪一根。</span>
      <span class="text-amber-700">画线与模板只存在这个标签页里，刷新即失。</span>
      <span class="text-amber-700">本页标的由账户锁定（一个账户只做一个品种），要换标的请新建账户。</span>
      <span v-if="store.loading" class="ml-auto">加载中…</span>
      <template v-else-if="store.instance">
        <span class="tnum ml-auto text-amber-700">
          {{ store.instance.symbol }} · {{ store.currentTime || store.day }} · {{ store.bars.length }} 根
          <template v-if="store.hiddenBars > 0"> · 还有 {{ store.hiddenBars }} 根没到</template>
        </span>
        <button
          type="button"
          class="rounded border border-amber-300 bg-panel px-2 py-0.5 text-[12px] text-amber-800 transition hover:border-accent hover:text-accent disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="store.hiddenBars === 0"
          :title="
            store.hiddenBars === 0
              ? '这一天的 K 线已经走完：后面的行情要等换了日文件才接得上'
              : '推进一根 K 线（等同按 →），账户会记住停在哪一根'
          "
          @click="store.step()"
        >
          下一根 ›
        </button>
      </template>
    </div>

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
            <span class="text-faint">按【下一根】可以往后放一根。</span>
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
