<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'

import AccountCard from '@/components/AccountCard.vue'
import AccountDialog from '@/components/AccountDialog.vue'
import Icon from '@/components/Icon.vue'
import * as api from '@/data/accounts'
import type { Account, AccountStatus } from '@/data/accounts'
import { errorText, refresh as refreshTemplates, store as templateStore } from '@/data/store'

/**
 * 账户总览。版式照参考图的首页：标题 + 一排筛选 + 账户卡片。
 *
 * 配色走全站浅色主题（`style.css`），参考图的深色底只借版式。
 * 参考图上的「Trading Days / 一致性百分比」需要按成交逐根重算，本页不编数字：
 * 卡片上放的是账户行里现成的余额 / 目标 / 回撤 / 一致性上限和阶段起点。
 */
const FILTERS: { key: AccountStatus | 'ALL'; label: string }[] = [
  { key: 'ALL', label: '全部' },
  { key: 'ACTIVE', label: '进行中' },
  { key: 'FAILED', label: '已失败' },
]

const filter = ref<AccountStatus | 'ALL'>('ALL')
const rows = ref<Account[]>([])
const loading = ref(true)
const error = ref('')

const list = computed(() =>
  filter.value === 'ALL' ? rows.value : rows.value.filter((a) => a.status === filter.value),
)
const counts = computed(() => ({
  ALL: rows.value.length,
  ACTIVE: rows.value.filter((a) => a.status === 'ACTIVE').length,
  FAILED: rows.value.filter((a) => a.status === 'FAILED').length,
}))

/**
 * 卡片左上角那格「模板名」（`LucidFlex 50K EVAL`）：账户行上只有 `templateId`，
 * 名字得从模板列表里查。列表本来就在拉（给新建弹窗用），所以这里只是一次本地查找。
 * 查不到（模板被删 / 还没加载回来）返回空串，卡片上那一格整颗不画。
 */
const tplName = (a: Account): string => api.templateName(a, templateStore.list)

function reload(): Promise<void> {
  return api
    .list()
    .then((l) => {
      rows.value = l
      error.value = ''
    })
    .catch((e: unknown) => {
      error.value = errorText(e)
    })
    .finally(() => {
      loading.value = false
    })
}

onMounted(() => {
  reload()
  // 弹窗要选模板，进页面先把模板列表拉一份（失败不挡账户列表，弹窗里会自己提示）
  refreshTemplates().catch(() => undefined)
})

// ---- 新建 ----
const drafting = ref(false)

function openNew(): void {
  error.value = ''
  drafting.value = true
}

function onCreate(payload: { templateId: string; symbolId: string; name: string }): void {
  api
    .open(payload.templateId, payload.symbolId, payload.name)
    .then(() => {
      drafting.value = false
      return reload()
    })
    .catch((e: unknown) => {
      error.value = errorText(e)
    })
}

// ---- 卡片上的入口 ----
/**
 * 详情是整页切换，而且**是一个真地址**（`/accounts/:id`）：刷新它、把它发给别人、
 * 从它按后退，都要落回同一个账户。所以这里直接推路由，不再往外壳 emit 一个 id ——
 * 「看的是哪个账户」只有 URL 这一个来源。
 */
const router = useRouter()

function openDetail(id: string): void {
  void router.push({ name: 'account-detail', params: { id } })
}

/**
 * 卡片上正在跑的写操作（结算 / 升级）：一次只跑一个，期间那颗按钮置灰并入文案。
 * 两个动作互斥 —— 它们都在改同一行账户，并发点只会让后一个读到半截状态。
 */
const pendingId = ref('')
const pendingKind = ref<'settle' | 'promote' | ''>('')

function act(id: string, kind: 'settle' | 'promote', fn: () => Promise<unknown>): void {
  if (pendingId.value) return
  pendingId.value = id
  pendingKind.value = kind
  error.value = ''
  fn()
    .then(() => reload())
    .catch((e: unknown) => {
      error.value = errorText(e)
    })
    .finally(() => {
      pendingId.value = ''
      pendingKind.value = ''
    })
}

function onSettle(id: string): void {
  act(id, 'settle', () => api.settleDay(id))
}

/**
 * 升级到下一阶段。这是一次**不可撤销**的迁移：换规则、余额重置回新阶段的账户规模、
 * 上一段阶段流水结掉。所以先问一句，并把「钱不带过去」写在问句里。
 *
 * 只在 `passed === true` 时才画这颗按钮（见 `AccountCard.vue`），
 * 所以这里不需要再判一次达标 —— 判了就是第三份口径。
 */
function onPromote(id: string): void {
  const a = rows.value.find((r) => r.id === id)
  if (!a) return
  const next = a.stage === 'EVAL' ? 'FUNDED' : 'PAYOUT'
  if (!confirm(`把「${a.name}」升到 ${next}？余额会重置回新阶段的账户规模（本阶段赚的钱不带过去）。`)) {
    return
  }
  act(id, 'promote', () => api.promote(id))
}

defineExpose({ openNew })
</script>

<template>
  <section class="flex min-w-0 flex-1 flex-col overflow-y-auto">
    <header class="px-8 pt-10 pb-4">
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 class="text-page font-semibold text-ink">账户总览</h1>
          <p class="mt-1 text-body text-dim">这里是你所有 Prop Firm 考核账户的进度。</p>
        </div>
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-md bg-accent px-3 py-2 text-lead font-medium text-white transition-colors hover:bg-accent-ink"
          @click="openNew"
        >
          <Icon name="plus" :size="15" />新建账户
        </button>
      </div>

      <div class="mt-5 inline-flex gap-0.5 rounded-lg border border-line bg-elev p-1">
        <button
          v-for="f in FILTERS"
          :key="f.key"
          type="button"
          class="rounded-md px-3.5 py-1.5 text-body transition-colors"
          :class="filter === f.key ? 'bg-panel font-medium text-ink shadow-sm' : 'text-dim hover:text-ink'"
          @click="filter = f.key"
        >
          {{ f.label }}
          <span class="num text-note font-normal text-faint">{{ counts[f.key] }}</span>
        </button>
      </div>
    </header>

    <!--
      公共公告栏（2026-11-01）。

      规则<b>写死在前端</b>：现在只有这一条、也不会按账户或模板变，做成后端配置就得先想清楚
      「谁能改」「改了历史账户怎么办」。等真要出多套时段时，再把这里换成接口返回的列表。

      文案和后端是对齐的：口径唯一来源是 `PropFirmClock`（交易日 [D 06:00, D+1 04:00)）、
      执行点是 `TradeDeskService#step`（走到 04:00 强平）与 `requireTradable`（禁区内拒单）。
      这里改了文案而没改规则，或者反过来，都会让用户看到一套、遇到另一套。
    -->
    <section class="px-8 pb-4">
      <div class="rounded-lg border border-line bg-panel">
        <div class="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2.5">
          <span class="h-3.5 w-1 rounded-full bg-accent"></span>
          <h2 class="text-section font-semibold text-ink">交易时段公告</h2>
          <span class="rounded bg-accent-soft px-1.5 py-0.5 text-note font-medium text-accent-ink">全体账户生效</span>
          <span class="ml-auto text-note text-faint">日终 04:00 · 开盘 06:00（北京时间）</span>
        </div>

        <dl class="grid gap-x-8 gap-y-3 px-4 py-3.5 text-body lg:grid-cols-2">
          <div class="flex gap-3">
            <dt class="w-[5rem] shrink-0 text-faint">交易日</dt>
            <dd class="min-w-0 text-dim">
              <b class="font-medium text-ink">06:00 开盘，次日 04:00 收盘</b>
              —— 跨零点，不是日历日。凌晨 03:10 那笔算前一天。
            </dd>
          </div>
          <div class="flex gap-3">
            <dt class="w-[5rem] shrink-0 text-faint">禁止交易</dt>
            <dd class="min-w-0 text-dim">
              <b class="font-medium text-ink">04:00 – 06:00</b>
              （含 04:00 与 06:00 本身）。这 2 小时不能挂单、不能市价开仓。
            </dd>
          </div>
          <div class="flex gap-3">
            <dt class="w-[5rem] shrink-0 text-faint">可下单</dt>
            <dd class="min-w-0 text-dim">
              <b class="num font-medium text-ink">每天 06:05 起，最晚 03:55</b>
              —— 5 分钟一根 K 线，两头的边界各占掉一根。
            </dd>
          </div>
          <div class="flex gap-3">
            <dt class="w-[5rem] shrink-0 text-faint">日终强平</dt>
            <dd class="min-w-0 text-dim">
              盘面走到 <b class="font-medium text-ink">04:00</b> 那根时，
              <b class="font-medium text-ink">按该根收盘价</b>市价平掉全部持仓、撤销全部挂单。
            </dd>
          </div>
          <div class="flex gap-3">
            <dt class="w-[5rem] shrink-0 text-faint">禁区内成交</dt>
            <dd class="min-w-0 text-dim">
              04:00 那一根即便高低点扫过挂单，也<b class="font-medium text-ink">一笔都不会成交</b>，
              挂单一律作废。
            </dd>
          </div>
          <div class="flex gap-3">
            <dt class="w-[5rem] shrink-0 text-faint">仍然允许</dt>
            <dd class="min-w-0 text-dim">
              撤单、一键退仓随时可用 —— 它们只会减少风险，禁交易时段也不例外。
            </dd>
          </div>
        </dl>

        <p class="border-t border-line px-4 py-2 text-note text-faint">
          以上规则由后端强制：禁交易时段的下单会被接口直接拒绝，交易页的下单按钮也会提前置灰。
        </p>
      </div>
    </section>

    <p
      v-if="error"
      class="mx-8 mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-note text-red-700"
    >
      {{ error }}
    </p>

    <div class="px-8 pb-10">
      <!-- 3 列（2026-11-01 回到 3 列）：字号收到约 1.35 倍后，431px 的卡刚好一行放得下
           「最新K线 / 已进行 N 日 / 起始时间」这几个小标签，2 列反而把 654px 的卡撑得大片留白 -->
      <div v-if="list.length" class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        <AccountCard
          v-for="a in list"
          :key="a.id"
          :a="a"
          :tpl-name="tplName(a)"
          :busy="pendingId === a.id"
          :pending="pendingId === a.id ? pendingKind : ''"
          @detail="openDetail"
          @settle="onSettle"
          @promote="onPromote"
        />
      </div>

      <p v-else class="py-16 text-center text-body text-faint">
        <template v-if="loading">加载中…</template>
        <template v-else-if="filter !== 'ALL'">这个筛选下没有账户。</template>
        <template v-else>还没有账户。点右上角「新建账户」，先选一套模板。</template>
      </p>
    </div>

    <AccountDialog
      v-if="drafting"
      :templates="templateStore.list"
      :error="error"
      @save="onCreate"
      @close="drafting = false"
    />
  </section>
</template>
