<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { RouterLink, useRouter } from 'vue-router'

import { money, phaseClass, phaseLabel } from '@/lib/account'
import { useReplayStore } from '@/stores/replay'

/**
 * 首页 = 考核账户总览（v0.32.0 的骨架版）。
 *
 * 为什么要有这一页：账户与考核账户合并之后，「我手上几个户、哪个快达标、哪个已经失败」
 * 是要一屏看完的东西，而画布只装得下一块盘面。所以 `/` 让给总览，画布挪到 `/account/:id`。
 *
 * **这一版的数据是假的**（`MOCK_ACCOUNTS`）：P1 换完协议、P2 把 `/api/accounts` 接上之后
 * 只换数据源，模板不动。字段故意按未来的摘要形状写（余额/目标/亏损线/交易天数/盈利日），
 * 接线时不用回头改布局。
 *
 * 配色只用项目现有的浅色 token（surface/panel/line/ink/dim/faint/accent/up/down），
 * 参考图那套深色不跟 —— 全项目只有一套浅色配色。
 */

const store = useReplayStore()
const router = useRouter()

interface MockAccount {
  id: string
  symbol: string
  template: string
  type: 'lucid-flex'
  phase: 'eval' | 'funded' | 'payout' | 'breached'
  initial: number
  balance: number
  target: number
  floor: number
  tradingDays: number
  profitDays: number
  profitDaysNeed: number
  dayNet: number
  dailyLimit: number
  closedAt?: string
}

// 假数据：三个户覆盖 eval / funded / breached 三种卡面，方便对样式。
const MOCK_ACCOUNTS: MockAccount[] = [
  {
    id: 'a1b2c3',
    symbol: 'MNQ',
    template: 'LucidFlex 50K',
    type: 'lucid-flex',
    phase: 'eval',
    initial: 50000,
    balance: 50120,
    target: 53000,
    floor: 48000,
    tradingDays: 36,
    profitDays: 2,
    profitDaysNeed: 5,
    dayNet: 80,
    dailyLimit: 1200,
  },
  {
    id: '5c8e01',
    symbol: 'MES',
    template: 'LucidFlex 50K',
    type: 'lucid-flex',
    phase: 'funded',
    initial: 50000,
    balance: 50860,
    target: 50860,
    floor: 48860,
    tradingDays: 12,
    profitDays: 5,
    profitDaysNeed: 5,
    dayNet: 0,
    dailyLimit: 1200,
  },
  {
    id: '77ae02',
    symbol: 'MGC',
    template: 'LucidFlex 50K',
    type: 'lucid-flex',
    phase: 'breached',
    initial: 50000,
    balance: 47980,
    target: 53000,
    floor: 48000,
    tradingDays: 9,
    profitDays: 1,
    profitDaysNeed: 5,
    dayNet: 0,
    dailyLimit: 1200,
    closedAt: '2026-10-21',
  },
]

const accounts = ref<MockAccount[]>(MOCK_ACCOUNTS)

/** 类型筛选：现在只有 LucidFlex 一种模板，留着这一条是为了以后加模板不用重排版 */
const typeFilter = ref<'all' | 'lucid-flex'>('all')
/** 状态筛选：进行中 = eval/funded，已通过 = payout，失败 = breached */
const statusFilter = ref<'active' | 'passed' | 'failed' | 'all'>('all')

const STATUS_OF: Record<MockAccount['phase'], 'active' | 'passed' | 'failed'> = {
  eval: 'active',
  funded: 'active',
  payout: 'passed',
  breached: 'failed',
}

const filtered = computed(() =>
  accounts.value.filter(
    (a) =>
      (typeFilter.value === 'all' || a.type === typeFilter.value) &&
      (statusFilter.value === 'all' || STATUS_OF[a.phase] === statusFilter.value),
  ),
)

const summary = computed(() => {
  const active = accounts.value.filter((a) => STATUS_OF[a.phase] === 'active').length
  const failed = accounts.value.filter((a) => STATUS_OF[a.phase] === 'failed').length
  return `${accounts.value.length} 个考核账户 · ${active} 个进行中 · ${failed} 个已失败`
})

/** 昵称先只在本机生效：服务端改名要等 P1 一起加（现在 `me.name` 是连接时分配的）。 */
const nickname = ref('')
watch(
  () => store.me?.name,
  (name) => {
    if (!nickname.value && name) nickname.value = name
  },
  { immediate: true },
)

onMounted(() => {
  // 连上 WS：进卡片时直接 join，不用等页面切过去再连一次（connect 本身幂等）
  store.connect()
})

/**
 * 进画布。
 *
 * 现在**不带 id**：卡片上是假数据，带着假 id 跳过去只会换来一句「账户不存在」。
 * P2 接上真实摘要之后改成 `params: { id }` 即可 —— 画布那边已经按 URL 里的 id 发 join。
 */
function open(): void {
  void router.push({ name: 'replay' })
}

function create(): void {
  window.alert('新建考核账户还没接线：P1 会把右上角那张「标的与考核账户」面板接上来。')
}
</script>

<template>
  <div class="flex h-screen bg-surface text-ink">
    <aside class="flex w-[172px] shrink-0 flex-col border-r border-line bg-panel px-3 py-4">
      <div class="px-1">
        <div class="text-[15px] font-semibold tracking-wide">Prop Firm 模拟</div>
        <div class="text-[11px] text-faint">考核账户总览</div>
      </div>

      <nav class="mt-4 space-y-0.5 text-[13px]">
        <RouterLink
          :to="{ name: 'accounts' }"
          class="block rounded px-2 py-1.5 font-medium"
          :class="'bg-accent-soft text-accent'"
        >
          考核账户
        </RouterLink>
        <RouterLink
          :to="{ name: 'records' }"
          class="block rounded px-2 py-1.5 text-dim hover:bg-elev hover:text-ink"
        >
          交易记录
          <span class="tnum ml-0.5 text-[11px] text-faint">{{ store.recordings.length }}</span>
        </RouterLink>
        <RouterLink
          :to="{ name: 'replay' }"
          class="block rounded px-2 py-1.5 text-dim hover:bg-elev hover:text-ink"
        >
          复盘画布
        </RouterLink>
      </nav>

      <div class="mt-auto space-y-1 px-1 text-[11px] text-faint">
        <div class="flex items-center gap-1.5">
          <span
            class="h-1.5 w-1.5 rounded-full"
            :class="
              store.status === 'open'
                ? 'bg-emerald-500'
                : store.status === 'connecting'
                  ? 'bg-amber-400'
                  : 'bg-red-500'
            "
          />
          {{ store.status === 'open' ? '已连接' : store.status === 'connecting' ? '连接中…' : '已断开' }}
        </div>
        <div class="tnum">v{{ store.me?.version ?? '' }}</div>
      </div>
    </aside>

    <main class="min-w-0 flex-1 overflow-y-auto">
      <div class="mx-auto max-w-[1180px] px-6 py-5">
        <header class="flex flex-wrap items-start gap-3">
          <div class="min-w-0 flex-1">
            <h1 class="text-[20px] font-semibold">欢迎{{ nickname ? `，${nickname}` : '' }}</h1>
            <p class="mt-0.5 text-[13px] text-dim">{{ summary }}</p>
          </div>

          <!-- 对应参考图右上角的 Set Nickname：一个卡片的形状先立住，改名等 P1 接后端 -->
          <div class="w-[190px] shrink-0 rounded-lg border border-line bg-panel p-2.5">
            <label class="block text-[12px] font-medium">昵称</label>
            <p class="mb-1 text-[11px] text-faint">顶栏与交易记录里显示的名字</p>
            <input
              v-model="nickname"
              type="text"
              placeholder="没填就用连接时分配的名字"
              class="w-full rounded border border-line bg-surface px-1.5 py-1 text-[12px] text-ink placeholder:text-faint"
            />
          </div>
        </header>

        <div class="mt-4 flex flex-wrap items-center gap-1.5 border-b border-line pb-3">
          <span class="mr-0.5 text-[11px] text-faint">类型</span>
          <button
            v-for="opt in [
              { id: 'all', label: '全部' },
              { id: 'lucid-flex', label: 'LucidFlex' },
            ] as const"
            :key="opt.id"
            type="button"
            class="rounded-full border px-2 py-0.5 text-[12px] transition"
            :class="
              typeFilter === opt.id
                ? 'border-accent bg-accent-soft text-accent'
                : 'border-line text-dim hover:border-accent hover:text-accent'
            "
            @click="typeFilter = opt.id"
          >
            {{ opt.label }}
          </button>

          <span class="mx-1 h-3.5 w-px bg-line" />

          <span class="mr-0.5 text-[11px] text-faint">状态</span>
          <button
            v-for="opt in [
              { id: 'active', label: '进行中' },
              { id: 'passed', label: '已通过' },
              { id: 'failed', label: '失败' },
              { id: 'all', label: '全部' },
            ] as const"
            :key="opt.id"
            type="button"
            class="rounded-full border px-2 py-0.5 text-[12px] transition"
            :class="
              statusFilter === opt.id
                ? 'border-accent bg-accent-soft text-accent'
                : 'border-line text-dim hover:border-accent hover:text-accent'
            "
            @click="statusFilter = opt.id"
          >
            {{ opt.label }}
          </button>

          <button
            type="button"
            class="ml-auto rounded bg-accent px-3 py-1.5 text-[13px] font-medium text-white hover:opacity-90"
            @click="create"
          >
            + 新建考核账户
          </button>
        </div>

        <div v-if="filtered.length === 0" class="py-16 text-center text-[13px] text-faint">
          没有符合筛选条件的账户。
        </div>

        <div v-else class="mt-4 grid gap-3 md:grid-cols-2 xl:grid-cols-3">
          <article
            v-for="a in filtered"
            :key="a.id"
            class="flex flex-col rounded-lg border border-line bg-panel p-4"
            :class="a.phase === 'breached' ? 'opacity-80' : ''"
          >
            <header class="flex flex-wrap items-center gap-1.5">
              <span class="text-[14px] font-semibold">{{ a.symbol }}</span>
              <span class="text-[12px] text-dim">{{ a.template }}</span>
              <span class="rounded px-1.5 py-px text-[11px] font-medium" :class="phaseClass(a.phase)">
                {{ phaseLabel(a.phase) }}
              </span>
              <span class="tnum ml-auto text-[11px] text-faint">#{{ a.id }}</span>
            </header>

            <div class="mt-3 grid grid-cols-3 gap-2">
              <div>
                <div class="text-[11px] text-faint">余额</div>
                <div class="tnum text-[15px] font-semibold">{{ money(a.balance) }}</div>
              </div>
              <div>
                <div class="text-[11px] text-faint">目标</div>
                <div class="tnum text-[15px] font-semibold">
                  {{ a.phase === 'eval' ? money(a.target) : '—' }}
                </div>
              </div>
              <div>
                <div class="text-[11px] text-faint">亏损线</div>
                <div class="tnum text-[15px] font-semibold text-down">{{ money(a.floor) }}</div>
              </div>
            </div>

            <div class="mt-2 flex flex-wrap gap-x-3 gap-y-0.5 text-[12px] text-dim">
              <span>交易 {{ a.tradingDays }} 天</span>
              <!-- EVAL 阶段不攒盈利日（那是 FUNDED 的事），写出来只会让人以为「还要等 5 天」 -->
              <span v-if="a.phase !== 'eval'">盈利日 {{ a.profitDays }}/{{ a.profitDaysNeed }}</span>
              <span v-if="a.phase !== 'breached'">
                当日
                <span class="tnum" :class="a.dayNet > 0 ? 'text-up' : a.dayNet < 0 ? 'text-down' : ''">
                  {{ a.dayNet > 0 ? '+' : '' }}{{ money(a.dayNet) }}
                </span>
                / 上限 {{ money(a.dailyLimit) }}
              </span>
              <span v-else class="text-down">触及亏损线 · {{ a.closedAt }}</span>
            </div>

            <!--
              存活的户给【继续】（接着这个户往下做，进度不丢）；
              已经爆掉的户只剩【查看详情】—— 规则不允许重开，给个「重置」只会点出一句拒绝。
            -->
            <footer class="mt-3 pt-1">
              <button
                v-if="a.phase !== 'breached'"
                type="button"
                class="w-full rounded border border-accent bg-accent-soft px-2 py-1 text-[12px] font-medium text-accent hover:bg-accent hover:text-white"
                @click="open()"
              >
                继续
              </button>
              <button
                v-else
                type="button"
                class="w-full rounded border border-line px-2 py-1 text-[12px] text-dim hover:border-accent hover:text-accent"
                @click="open()"
              >
                查看详情
              </button>
            </footer>
          </article>
        </div>

        <p class="mt-4 text-[11px] text-faint">
          骨架版：上面三条是假数据；新建 / 继续 / 查看详情等 P1 换完协议后接线。
        </p>
      </div>
    </main>
  </div>
</template>
