<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import * as accountsApi from '@/data/accounts'
import { symbols as loadSymbols } from '@/data/bars'
import type { SymbolBrief } from '@/data/bars'
import { errorText, refresh as refreshTemplates, store as templateStore } from '@/data/store'
import { BotRun } from '@/lib/bot/engine.ts'
import type { AccountRun, BotState } from '@/lib/bot/engine.ts'

/**
 * 机器人压测页。
 *
 * ## 它为什么存在
 *
 * 撮合、盈亏、撞线判定这些东西，单测覆盖的是「服务层怎么算」，覆盖不到
 * 「订单 → 成交 → 重放」这条接线。2026-11-03 的两次误判（账户「7」「夜色粽子」）
 * 正是接线错了、单测全绿。所以这一页的作用是：**开一堆真的 EVAL 账户，
 * 用和用户一样的下单消息把它们跑起来**，每根 K 线回来都独立重算一遍账去对。
 *
 * ## 它会写库
 *
 * 账户是真的（`POST /api/pf-accounts`），成交、流水、余额都是真的。所以：
 *
 * - 只用 EVAL 模板（考核阶段的判定逻辑最全）；
 * - 账户名统一带前缀，跑完可以一键删掉；
 * - 建议对着**另一个实例**跑（`BOT_BASE` / 另一个端口的后端），别在正用的库上开一堆。
 */

const symbolList = ref<SymbolBrief[]>([])
const templateId = ref('')
const symbolId = ref('')
const count = ref(6)
const bars = ref(120)
const seed = ref(20261103)
const concurrency = ref(3)

const error = ref('')
const running = ref(false)
const state = ref<BotState | null>(null)
const bot = ref<BotRun | null>(null)

let ticker = 0

/** 机器人跑在本页同源的后端上（开发时由 vite 代理转到 127.0.0.1:8791）。 */
const baseUrl = ''

const wsUrl = (): string => {
  const scheme = location.protocol === 'https:' ? 'wss' : 'ws'
  return `${scheme}://${location.host}/ws`
}

const evalTemplates = computed(() => templateStore.list.filter((t) => t.stage === 'EVAL'))
const usableSymbols = computed(() => symbolList.value.filter((s) => s.days > 0))

const runs = computed<AccountRun[]>(() => state.value?.runs ?? [])

const summary = computed(() => {
  const s = state.value
  if (!s) return null
  const failed = s.runs.filter((r) => r.status === 'FAILED_ACCOUNT').length
  const frozen = s.runs.filter((r) => r.status === 'FROZEN').length
  return {
    bars: s.totalBars,
    trades: s.totalTrades || s.runs.reduce((sum, r) => sum + r.trades, 0),
    orders: s.runs.reduce((sum, r) => sum + r.orders, 0),
    failed,
    frozen,
    violations: s.violations.length,
  }
})

/** 违例按种类计数：先看「哪一类在冒」比看一百条明细快。 */
const violationKinds = computed(() => {
  const map = new Map<string, number>()
  for (const v of state.value?.violations ?? []) map.set(v.kind, (map.get(v.kind) ?? 0) + 1)
  return [...map.entries()].sort((a, b) => b[1] - a[1])
})

onMounted(async () => {
  try {
    await refreshTemplates()
    templateId.value = evalTemplates.value[0]?.id ?? ''
  } catch (e) {
    error.value = `模板列表读取失败：${errorText(e)}`
  }
  try {
    symbolList.value = await loadSymbols()
    symbolId.value = usableSymbols.value[0]?.code ?? ''
  } catch (e) {
    error.value = `标的清单读取失败：${errorText(e)}`
  }
})

onBeforeUnmount(() => {
  window.clearInterval(ticker)
  bot.value?.stop()
})

async function start(): Promise<void> {
  if (running.value) return
  error.value = ''
  if (!templateId.value) {
    error.value = '还没有 EVAL 模板：先去「账户模板」建一套'
    return
  }
  if (!symbolId.value) {
    error.value = '还没有可交易的标的'
    return
  }

  const instance = new BotRun({
    baseUrl,
    wsUrl: wsUrl(),
    templateId: templateId.value,
    symbolId: symbolId.value,
    accounts: count.value,
    maxBars: bars.value,
    seed: seed.value,
    concurrency: concurrency.value,
    namePrefix: '机器人-',
  })
  bot.value = instance
  running.value = true
  state.value = null

  // 引擎里的 state 是就地改的普通对象，Vue 看不见它的变化 —— 这里按节拍拍一份快照，
  // 拿「复制」的代价换「不用把响应式塞进引擎」（引擎要能在 Node 里无头跑）
  ticker = window.setInterval(() => {
    state.value = JSON.parse(JSON.stringify(instance.state)) as BotState
  }, 600)

  try {
    await instance.run()
  } catch (e) {
    error.value = errorText(e)
  } finally {
    window.clearInterval(ticker)
    state.value = JSON.parse(JSON.stringify(instance.state)) as BotState
    running.value = false
  }
}

function stop(): void {
  bot.value?.stop()
}

/** 删掉本次跑出来的账户：机器人建的户没有保留价值，留着只会把总览页塞满。 */
async function clean(): Promise<void> {
  const ids = runs.value.map((r) => r.id)
  if (ids.length === 0) return
  try {
    for (const id of ids) await accountsApi.remove(id)
    error.value = `已删除 ${ids.length} 个机器人账户`
    state.value = null
  } catch (e) {
    error.value = `删除失败：${errorText(e)}`
  }
}

const STATUS_TEXT: Record<string, string> = {
  PENDING: '等待',
  RUNNING: '在跑',
  DONE: '跑完',
  FAILED_ACCOUNT: '撞线失败',
  FROZEN: '已达标',
  STOPPED: '已停止',
  ERROR: '出错',
}

const money = (value: number): string => value.toFixed(2)
</script>

<template>
  <div class="flex-1 overflow-y-auto">
    <div class="mx-auto max-w-[1180px] px-8 py-7">
      <header class="flex items-end justify-between gap-6">
        <div>
          <h1 class="text-page font-semibold text-ink">机器人压测</h1>
          <p class="mt-2 max-w-[640px] text-body leading-relaxed text-dim">
            开一批真的 EVAL 账户，用<strong class="font-medium text-ink">和交易页一样的下单消息</strong>
            跑盘：每推进一根，就用收到的事实独立重算余额 / 权益 / 持仓 / 亏损线，和账户读数对账。
            它检验的是「订单 → 成交 → 重放」这条接线，不是策略收益。
          </p>
        </div>
        <div class="flex shrink-0 items-center gap-2">
          <button
            v-if="!running"
            type="button"
            class="rounded-md border border-accent/40 bg-accent-soft px-4 py-2 text-body font-medium text-accent-ink transition-colors hover:bg-accent-soft/70"
            @click="start"
          >
            开始压测
          </button>
          <button
            v-else
            type="button"
            class="rounded-md border border-line bg-elev px-4 py-2 text-body font-medium text-dim transition-colors hover:bg-panel"
            @click="stop"
          >
            停止
          </button>
          <button
            type="button"
            class="rounded-md border border-line px-3 py-2 text-body text-dim transition-colors hover:bg-elev disabled:opacity-40"
            :disabled="running || runs.length === 0"
            @click="clean"
          >
            删除本次账户
          </button>
        </div>
      </header>

      <!-- 参数 -->
      <section class="mt-6 rounded-lg border border-line bg-panel p-5">
        <div class="grid grid-cols-2 gap-x-6 gap-y-4 lg:grid-cols-5">
          <label class="flex flex-col gap-1.5">
            <span class="text-label font-medium text-faint">模板（只列 EVAL）</span>
            <select v-model="templateId" class="rounded-md border border-line bg-elev px-2.5 py-1.5 text-body text-ink">
              <option v-if="evalTemplates.length === 0" value="">没有 EVAL 模板</option>
              <option v-for="t in evalTemplates" :key="t.id" :value="t.id">{{ t.name }}</option>
            </select>
          </label>
          <label class="flex flex-col gap-1.5">
            <span class="text-label font-medium text-faint">交易标的</span>
            <select v-model="symbolId" class="rounded-md border border-line bg-elev px-2.5 py-1.5 text-body text-ink">
              <option v-for="s in usableSymbols" :key="s.code" :value="s.code">
                {{ s.code }}（{{ s.days }} 天）
              </option>
            </select>
          </label>
          <label class="flex flex-col gap-1.5">
            <span class="text-label font-medium text-faint">账户数</span>
            <input
              v-model.number="count"
              type="number"
              min="1"
              max="40"
              class="num rounded-md border border-line bg-elev px-2.5 py-1.5 text-body text-ink"
            />
          </label>
          <label class="flex flex-col gap-1.5">
            <span class="text-label font-medium text-faint">每户最多推进（根）</span>
            <input
              v-model.number="bars"
              type="number"
              min="5"
              max="600"
              class="num rounded-md border border-line bg-elev px-2.5 py-1.5 text-body text-ink"
            />
          </label>
          <label class="flex flex-col gap-1.5">
            <span class="text-label font-medium text-faint">随机种子</span>
            <input
              v-model.number="seed"
              type="number"
              class="num rounded-md border border-line bg-elev px-2.5 py-1.5 text-body text-ink"
            />
          </label>
        </div>
        <p class="mt-3 text-note leading-relaxed text-faint">
          账户是真的：会往当前后端的库里写账户和成交流水（名字统一带「机器人-」前缀，跑完可一键删除）。
          账户越多、根数越多越慢 —— 每推进一根，后端都要从阶段起点重放一遍账。
        </p>
      </section>

      <p v-if="error" class="mt-4 rounded-md border border-down/30 bg-down-soft px-3 py-2 text-body text-down">
        {{ error }}
      </p>

      <!-- 汇总 -->
      <section v-if="summary" class="mt-6 grid grid-cols-2 gap-3 md:grid-cols-6">
        <div class="rounded-lg border border-line bg-panel px-4 py-3">
          <p class="text-label font-medium text-faint">推进</p>
          <p class="num mt-1 text-metric-sm font-semibold text-ink">{{ summary.bars }}</p>
        </div>
        <div class="rounded-lg border border-line bg-panel px-4 py-3">
          <p class="text-label font-medium text-faint">成交</p>
          <p class="num mt-1 text-metric-sm font-semibold text-ink">{{ summary.trades }}</p>
        </div>
        <div class="rounded-lg border border-line bg-panel px-4 py-3">
          <p class="text-label font-medium text-faint">下单</p>
          <p class="num mt-1 text-metric-sm font-semibold text-ink">{{ summary.orders }}</p>
        </div>
        <div class="rounded-lg border border-line bg-panel px-4 py-3">
          <p class="text-label font-medium text-faint">撞线</p>
          <p class="num mt-1 text-metric-sm font-semibold text-ink">{{ summary.failed }}</p>
        </div>
        <div class="rounded-lg border border-line bg-panel px-4 py-3">
          <p class="text-label font-medium text-faint">达标冻结</p>
          <p class="num mt-1 text-metric-sm font-semibold text-ink">{{ summary.frozen }}</p>
        </div>
        <div class="rounded-lg border border-line bg-panel px-4 py-3">
          <p class="text-label font-medium text-faint">违例</p>
          <p class="num mt-1 text-metric-sm font-semibold" :class="summary.violations ? 'text-down' : 'text-ink'">
            {{ summary.violations }}
          </p>
        </div>
      </section>

      <!-- 账户 -->
      <section v-if="runs.length" class="mt-6 overflow-hidden rounded-lg border border-line bg-panel">
        <table class="w-full border-collapse text-body">
          <thead>
            <tr class="border-b border-line text-label text-faint">
              <th class="px-4 py-2.5 text-left font-medium">账户</th>
              <th class="px-3 py-2.5 text-left font-medium">状态</th>
              <th class="px-3 py-2.5 text-right font-medium">根</th>
              <th class="px-3 py-2.5 text-right font-medium">成交</th>
              <th class="px-3 py-2.5 text-right font-medium">余额</th>
              <th class="px-3 py-2.5 text-right font-medium">权益</th>
              <th class="px-3 py-2.5 text-right font-medium">亏损线</th>
              <th class="px-4 py-2.5 text-right font-medium">违例</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="run in runs" :key="run.id" class="border-b border-line/60 last:border-0">
              <td class="px-4 py-2 text-ink">{{ run.name }}</td>
              <td class="px-3 py-2" :class="run.status === 'FAILED_ACCOUNT' ? 'text-down' : 'text-dim'">
                {{ STATUS_TEXT[run.status] ?? run.status }}
                <span v-if="run.status === 'FAILED_ACCOUNT' && run.failBar" class="ml-1 text-note text-faint">
                  {{ run.failBar }}
                </span>
              </td>
              <td class="num px-3 py-2 text-right text-dim">{{ run.bars }}</td>
              <td class="num px-3 py-2 text-right text-dim">{{ run.trades }}</td>
              <td class="num px-3 py-2 text-right text-ink">{{ money(run.balance) }}</td>
              <td class="num px-3 py-2 text-right text-dim">{{ money(run.equity) }}</td>
              <td class="num px-3 py-2 text-right text-dim">{{ money(run.lossLine) }}</td>
              <td class="num px-4 py-2 text-right" :class="run.violations.length ? 'text-down' : 'text-faint'">
                {{ run.violations.length }}
              </td>
            </tr>
          </tbody>
        </table>
      </section>

      <!-- 违例 -->
      <section v-if="state && state.violations.length" class="mt-6 rounded-lg border border-down/30 bg-panel p-5">
        <h2 class="text-section font-medium text-ink">对账违例</h2>
        <div class="mt-3 flex flex-wrap gap-2">
          <span
            v-for="[kind, n] in violationKinds"
            :key="kind"
            class="num rounded-full border border-down/30 bg-down-soft px-2.5 py-1 text-note text-down"
          >
            {{ kind }} × {{ n }}
          </span>
        </div>
        <ul class="mt-4 space-y-1.5">
          <li v-for="(v, i) in state.violations.slice(0, 40)" :key="i" class="text-note leading-relaxed text-dim">
            <span class="num font-medium text-down">{{ v.kind }}</span>
            <span class="mx-1.5 text-faint">|</span>{{ v.account }}
            <span class="num mx-1.5 text-faint">{{ v.barTime }}</span>
            {{ v.detail }}
          </li>
        </ul>
        <p v-if="state.violations.length > 40" class="mt-2 text-note text-faint">
          只显示前 40 条，共 {{ state.violations.length }} 条。
        </p>
      </section>

      <p v-else-if="state && state.done" class="mt-6 rounded-lg border border-line bg-panel px-4 py-3 text-body text-dim">
        这一轮没有任何违例：后端算出的余额、权益、持仓、亏损线，和机器人按协议事实重算的结果逐根一致。
      </p>
    </div>
  </div>
</template>
