<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import ChartPanel from '@/components/ChartPanel.vue'
import ChartToolbar from '@/components/ChartToolbar.vue'
import Icon from '@/components/Icon.vue'
import TradeDock from '@/components/TradeDock.vue'
import TradeFloat from '@/components/TradeFloat.vue'
import * as accounts from '@/data/accounts'
import type { Account } from '@/data/accounts'
import { errorText } from '@/data/store'
import { drawdownLabel, pct, stageLabel, usd } from '@/data/rules'
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
 * 能：看那一天的真 K 线（缩放 / 平移 / 指标 / 周期切换）、画线（趋势线/射线/矩形/
 *     箭头/斐波那契 + 模板）、按 nATR 算手数、把下单面板填好。
 *
 * 不能：**下单、撤单、平仓、逐根推进**。撮合还在设计阶段（`TRADING-ENGINE-DESIGN.md`），
 *     后端没有任何交易接口。所以那些按钮点下去只给一句明确提示，不产生假数据 ——
 *     账由后端算，前端先造一笔成交会让界面立刻和余额对不上。
 *
 * 画线只活在这个标签页的内存里：刷新即失。页面顶部有一条常驻说明。
 */

const props = defineProps<{ id: string }>()

const store = useTradeStore()

const account = ref<Account | null>(null)
const accountErr = ref('')
const loadingAccount = ref(true)

/** 日期选择框：跟 store 里的 day 走，改了就去取那一天。 */
const dayDraft = computed({
  get: () => store.day,
  set: (v: string) => store.setDay(v),
})

/** 当前标的的日文件范围：日期框据此夹 min/max，免得选到 2019 年去查一个空目录。 */
const symbol = computed(() => store.symbols.find((s) => s.code === store.symbolCode) ?? null)

/** 账户起点那天（`startDayFile` 是文件名，如 `2024-03-04.csv`）。 */
function accountDay(a: Account): string {
  return a.startDayFile.replace(/\.csv$/i, '')
}

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

  await store.loadSymbols()
  const first = store.symbols.find((s) => s.days > 0) ?? store.symbols[0]
  if (!first) {
    store.noteError('没有任何标的：先跑抓取程序，data/_symbols.json 里才会出现标的。')
    return
  }
  // 起点优先用账户那一天（训练从哪根 K 线开始是账户的一部分），否则退到这个标的的最新一天
  const wanted = account.value ? accountDay(account.value) : ''
  const inside = wanted && first.firstDay && first.lastDay && wanted >= first.firstDay && wanted <= first.lastDay
  await store.load(first.code, inside ? wanted : first.lastDay)
})
</script>

<template>
  <div class="flex h-screen flex-col bg-surface text-ink">
    <header class="flex min-h-11 shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-line bg-panel px-3 py-1">
      <a
        href="/"
        class="flex items-center gap-1 whitespace-nowrap text-[13px] text-dim transition-colors hover:text-ink"
        title="回到账户总览（这个标签页可以直接关掉）"
      >
        <Icon name="left" :size="13" />账户
      </a>

      <template v-if="account">
        <h1 class="whitespace-nowrap text-[15px] font-semibold tracking-wide">{{ account.name }}</h1>
        <span class="rounded border border-line bg-elev px-1.5 py-0.5 text-[11px] tracking-wider text-dim">
          {{ stageLabel(account.stage) }}
        </span>
        <span
          class="tnum whitespace-nowrap text-[13px]"
          :class="account.balance >= account.baseBalance ? 'text-accent-ink' : 'text-red-600'"
          :title="`起点 ${usd(account.baseBalance)}`"
        >
          {{ usd(account.balance) }}
        </span>
        <span class="tnum whitespace-nowrap text-[12px] text-faint">
          回撤 {{ drawdownLabel(account.rules.drawdownType) }}
          {{ usd(account.rules.maxDrawdown) }} · 一致性
          {{ account.rules.consistencyRatio == null ? '—' : `≤${pct(account.rules.consistencyRatio)}` }}
        </span>
      </template>
      <span v-else-if="loadingAccount" class="text-[13px] text-faint">账户加载中…</span>
      <span v-else class="text-[13px] text-red-600">账户加载失败：{{ accountErr }}</span>

      <!-- 标的与日期：账户不绑定单一标的（可以同时做 MES / MNQ），所以这两个都得能换 -->
      <div class="ml-auto flex items-center gap-1.5 text-[12px]">
        <select
          class="rounded border border-line bg-panel px-1.5 py-1 text-[12px] text-ink"
          :value="store.symbolCode"
          title="标的：清单来自 data/_symbols.json（抓取程序维护）"
          @change="store.setSymbol(($event.target as HTMLSelectElement).value)"
        >
          <option v-for="s in store.symbols" :key="s.code" :value="s.code">
            {{ s.code }}（{{ s.days }} 天）
          </option>
          <option v-if="store.symbols.length === 0" value="">没有标的</option>
        </select>

        <input
          type="date"
          class="tnum rounded border border-line bg-panel px-1.5 py-0.5 text-[12px] text-ink"
          :value="dayDraft"
          :min="symbol?.firstDay"
          :max="symbol?.lastDay"
          title="看哪一天：一天一个 CSV，切换日期就是换一天"
          @change="dayDraft = ($event.target as HTMLInputElement).value"
        />

        <button
          v-if="symbol?.lastDay && store.day !== symbol.lastDay"
          type="button"
          class="rounded border border-line px-2 py-1 text-[12px] text-dim hover:border-accent hover:text-accent"
          @click="store.setDay(symbol.lastDay!)"
        >
          最新
        </button>
      </div>
    </header>

    <!-- 常驻说明：交易后端还没接通、画线不落库 —— 这两件事不写在页面上，用户会以为是自己用错了 -->
    <div
      class="flex shrink-0 flex-wrap items-center gap-x-3 gap-y-1 border-b border-amber-200 bg-amber-50 px-3 py-1 text-[12px] text-amber-800"
    >
      <span class="font-medium">看盘模式</span>
      <span>下单 / 撤单 / 平仓 / 逐根推进都还没接通（撮合后端在做），按钮只给提示，不会产生任何数据。</span>
      <span class="text-amber-700">画线与模板只存在这个标签页里，刷新即失。</span>
      <span v-if="store.loading" class="ml-auto">加载中…</span>
      <span v-else-if="store.instance" class="tnum ml-auto text-amber-700">
        {{ store.instance.symbol }} · {{ store.day }} · {{ store.bars.length }} 根
      </span>
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
            {{ store.symbolCode }} 在 {{ store.day }} 没有 K 线（休市，或那一天还没抓）。
            <span class="text-faint">换一个日期试试。</span>
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
