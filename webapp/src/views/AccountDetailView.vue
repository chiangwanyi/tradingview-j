<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import Icon from '@/components/Icon.vue'
import * as api from '@/data/accounts'
import { shortInstant, shortTime, targetProgress } from '@/data/accounts'
import type { Account, StageLog } from '@/data/accounts'
import { drawdownLabel, pct, stageLabel, usd } from '@/data/rules'
import { errorText } from '@/data/store'

/**
 * 账户详情（整页，不是抽屉）。卡片上收走的写操作全在这里：
 * 升级 / 改名 / 删除 + 阶段流水。
 *
 * 数据自己拉（`GET /api/pf-accounts/{id}` + `/{id}/stages`），不吃列表里那份拷件 ——
 * 从列表点进来时它可能已经是几十秒前的余额。
 *
 * 「继续交易」在卡片和这里都有：它开一个新标签页（`/trade/:id`），不改变本页状态 ——
 * 详情页留着不动，交易页在另一个标签里跑，两边都刷新也不会互相打断。
 */
const props = defineProps<{ id: string }>()
const emit = defineEmits<{ back: [] }>()

const a = ref<Account | null>(null)
const logs = ref<StageLog[]>([])
const loading = ref(true)
const err = ref('')
const busy = ref(false)

const progress = computed(() => (a.value ? targetProgress(a.value) : null))
const gained = computed(() => (a.value ? a.value.balance - a.value.baseBalance : 0))
/** 亏损线：日终峰值 − 最大回撤。没有回撤规则就不编一个数。 */
const lossLine = computed(() =>
  a.value && a.value.rules.maxDrawdown != null
    ? a.value.eodPeakBalance - a.value.rules.maxDrawdown
    : null,
)

/** 只刷新数据、不清空已有内容：改名 / 升级后原地换个数字，别整页闪一下。 */
function load(): Promise<void> {
  return Promise.all([api.get(props.id), api.stages(props.id)])
    .then(([acc, l]) => {
      a.value = acc
      logs.value = l
      err.value = ''
    })
    .catch((e: unknown) => {
      err.value = errorText(e)
    })
    .finally(() => {
      loading.value = false
    })
}

onMounted(load)

// ---- 写操作 ----
function run(fn: () => Promise<unknown>): void {
  busy.value = true
  err.value = ''
  fn()
    .catch((e: unknown) => {
      err.value = errorText(e)
    })
    .finally(() => {
      busy.value = false
    })
}

const editing = ref(false)
const draftName = ref('')

function startRename(): void {
  draftName.value = a.value?.name ?? ''
  editing.value = true
}

function submitRename(): void {
  const name = draftName.value.trim()
  if (!name || !a.value) {
    editing.value = false
    return
  }
  editing.value = false
  run(() => api.rename(a.value!.id, name).then(load))
}

function doPromote(): void {
  if (!a.value) return
  if (!confirm(`把「${a.value.name}」升到下一阶段？余额会重置回新阶段的账户规模。`)) return
  run(() => api.promote(a.value!.id).then(load))
}

function doRemove(): void {
  if (!a.value) return
  if (!confirm(`删掉「${a.value.name}」？成交、流水、图形都不会被删，只是这个账户不再列出。`)) return
  run(() => api.remove(a.value!.id).then(() => emit('back')))
}

/** 新标签页打开交易页（和卡片上那颗按钮同一个行为，两处入口一个目的）。 */
function openTrade(): void {
  if (!a.value) return
  window.open(`/trade/${encodeURIComponent(a.value.id)}`, '_blank', 'noopener')
}
</script>

<template>
  <section class="flex min-w-0 flex-1 flex-col overflow-y-auto">
    <header class="px-8 pt-7 pb-4">
      <button
        type="button"
        class="flex items-center gap-1 text-[13px] text-dim transition-colors hover:text-ink"
        @click="emit('back')"
      >
        <Icon name="left" :size="14" />返回账户总览
      </button>

      <div v-if="a" class="mt-3 flex flex-wrap items-center gap-2">
        <input
          v-if="editing"
          v-model="draftName"
          class="w-[220px] rounded-md border border-line bg-panel px-2 py-1 text-[22px] font-semibold text-ink outline-none focus:border-accent/60"
          @keyup.enter="submitRename"
          @keyup.esc="editing = false"
        />
        <h1 v-else class="text-[24px] font-semibold tracking-tight text-ink">{{ a.name }}</h1>

        <span class="rounded border border-line bg-elev px-1.5 py-0.5 text-[11px] tracking-wider text-dim">
          {{ stageLabel(a.stage) }}
        </span>
        <span
          class="rounded border px-1.5 py-0.5 text-[11px] tracking-wider uppercase"
          :class="
            a.status === 'ACTIVE'
              ? 'border-accent/30 bg-accent-soft text-accent-ink'
              : 'border-red-200 bg-red-50 text-red-700'
          "
        >
          {{ a.status === 'ACTIVE' ? '进行中' : '已失败' }}
        </span>
        <span class="rounded border border-line px-1.5 py-0.5 text-[11px] tracking-wider text-faint uppercase">
          {{ drawdownLabel(a.rules.drawdownType) }}
        </span>

        <template v-if="editing">
          <button
            type="button"
            class="ml-1 rounded-md border border-accent/40 px-3 py-1 text-[13px] font-medium text-accent-ink hover:bg-accent-soft"
            @click="submitRename"
          >
            保存
          </button>
          <button
            type="button"
            class="rounded-md border border-line px-3 py-1 text-[13px] text-dim hover:border-line-strong"
            @click="editing = false"
          >
            取消
          </button>
        </template>
      </div>
      <p v-else class="mt-3 text-[13px] text-faint">{{ loading ? '加载中…' : '账户不存在或已删除。' }}</p>
    </header>

    <p v-if="err" class="mx-8 mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-700">
      {{ err }}
    </p>

    <div v-if="a" class="px-8 pb-10">
      <!-- 四个数字：口径和卡片一致 -->
      <div class="grid grid-cols-2 gap-x-8 gap-y-4 rounded-xl border border-line bg-panel px-5 py-4 lg:grid-cols-4">
        <div>
          <p class="text-[11px] tracking-wider text-faint uppercase">账户余额</p>
          <p class="tnum text-[22px] leading-tight font-semibold text-ink">{{ usd(a.balance) }}</p>
          <p class="tnum text-[11px] text-faint">
            起点 {{ usd(a.baseBalance) }} ·
            <span :class="gained >= 0 ? 'text-accent-ink' : 'text-red-600'">
              {{ gained >= 0 ? '+' : '' }}{{ usd(gained) }}
            </span>
          </p>
        </div>
        <div>
          <p class="text-[11px] tracking-wider text-faint uppercase">盈利目标</p>
          <p class="tnum text-[22px] leading-tight font-semibold text-ink">
            {{ usd(a.rules.profitTarget) }}
          </p>
          <p class="tnum text-[11px] text-faint">
            {{ a.rules.profitTarget == null ? '本阶段无目标' : `还差 ${usd(Math.max(0, a.rules.profitTarget - gained))}` }}
          </p>
        </div>
        <div>
          <p class="text-[11px] tracking-wider text-faint uppercase">最大回撤</p>
          <p class="tnum text-[18px] font-medium text-ink">{{ usd(a.rules.maxDrawdown) }}</p>
          <p class="tnum text-[11px] text-faint">亏损线 {{ usd(lossLine) }}</p>
        </div>
        <div>
          <p class="text-[11px] tracking-wider text-faint uppercase">一致性上限</p>
          <p class="tnum text-[18px] font-medium text-ink">
            {{ a.rules.consistencyRatio == null ? '—' : `≤${pct(a.rules.consistencyRatio)}` }}
          </p>
          <p class="tnum text-[11px] text-faint">
            {{ a.rules.minProfitDays == null ? '不查盈利日' : `盈利日 ${a.rules.minProfitDays} 天` }}
          </p>
        </div>
      </div>

      <div v-if="progress != null" class="mt-3 rounded-xl border border-line bg-panel px-5 py-4">
        <div class="flex items-baseline justify-between text-[12px] text-dim">
          <span>本阶段进度</span>
          <span class="tnum text-ink">{{ pct(progress) }}</span>
        </div>
        <div class="mt-2 h-1.5 w-full overflow-hidden rounded-full bg-elev">
          <div class="h-full rounded-full bg-accent" :style="{ width: `${progress * 100}%` }" />
        </div>
        <div class="mt-1.5 flex justify-between text-[11px] tracking-wider text-faint uppercase tnum">
          <span>起点 {{ usd(a.baseBalance) }}</span>
          <span>目标 {{ usd(a.baseBalance + (a.rules.profitTarget ?? 0)) }}</span>
        </div>
      </div>

      <!-- 时间与来源：账户的「身份」都在这里，卡片上放不下 -->
      <dl
        class="mt-3 grid grid-cols-1 gap-x-8 gap-y-2.5 rounded-xl border border-line bg-panel px-5 py-4 text-[13px] sm:grid-cols-2"
      >
        <div class="flex justify-between gap-4">
          <dt class="text-faint">起点 K 线</dt>
          <dd class="tnum text-ink">{{ shortTime(a.startBarTime) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">本阶段自</dt>
          <dd class="tnum text-ink">{{ shortInstant(a.stageStartedAt) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">建户时刻</dt>
          <dd class="tnum text-ink">{{ shortInstant(a.startedAt) }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">起始日文件</dt>
          <dd class="truncate text-ink">{{ a.startDayFile }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">模板</dt>
          <dd class="truncate text-ink" :title="a.templateId">{{ a.templateId }}</dd>
        </div>
        <div class="flex justify-between gap-4">
          <dt class="text-faint">账户 ID</dt>
          <dd class="truncate text-ink" :title="a.id">{{ a.id }}</dd>
        </div>
      </dl>

      <!-- 阶段流水：行数就是身份（1 行 = 建户时就在这个阶段，2 行 = 考过来的） -->
      <div class="mt-3 rounded-xl border border-line bg-panel px-5 py-4">
        <p class="text-[13px] font-medium text-ink">阶段流水</p>
        <table v-if="logs.length" class="mt-2 w-full text-[12px]">
          <thead>
            <tr class="text-left text-[11px] tracking-wider text-faint uppercase">
              <th class="py-1.5 pr-4 font-normal">阶段</th>
              <th class="py-1.5 pr-4 font-normal">进入</th>
              <th class="py-1.5 pr-4 font-normal">结束</th>
              <th class="py-1.5 pr-4 text-right font-normal">基础余额</th>
              <th class="py-1.5 pr-4 text-right font-normal">期末余额</th>
              <th class="py-1.5 pr-4 text-right font-normal">净额</th>
              <th class="py-1.5 font-normal">怎么结束的</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="l in logs" :key="l.id" class="border-t border-line">
              <td class="py-1.5 pr-4 text-dim">{{ stageLabel(l.stage) }}</td>
              <td class="tnum py-1.5 pr-4 text-ink">{{ shortInstant(l.startedAt) }}</td>
              <td class="tnum py-1.5 pr-4 text-ink">{{ l.endedAt ? shortInstant(l.endedAt) : '进行中' }}</td>
              <td class="tnum py-1.5 pr-4 text-right text-dim">{{ usd(l.baseBalance) }}</td>
              <td class="tnum py-1.5 pr-4 text-right text-dim">{{ usd(l.balanceAtEnd) }}</td>
              <td
                class="tnum py-1.5 pr-4 text-right"
                :class="l.netPnl == null ? 'text-faint' : l.netPnl >= 0 ? 'text-accent-ink' : 'text-red-600'"
              >
                {{ usd(l.netPnl) }}
              </td>
              <td class="py-1.5 text-faint">{{ l.endReason ?? '—' }}</td>
            </tr>
          </tbody>
        </table>
        <p v-else class="mt-2 text-[12px] text-faint">没有阶段流水。</p>
      </div>

      <!-- 动作：继续交易 + 卡片上收过来的三个 -->
      <div class="mt-3 flex flex-wrap gap-2">
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-md bg-accent px-4 py-2 text-[13px] font-medium text-white transition-colors hover:bg-accent-ink"
          title="在新标签页打开这个账户的 K 线画布"
          @click="openTrade"
        >
          <Icon name="right" :size="14" />继续交易
        </button>
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-md border border-line px-4 py-2 text-[13px] text-dim transition-colors hover:border-line-strong hover:text-ink disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="busy || a.status === 'FAILED' || a.stage === 'PAYOUT'"
          :title="a.stage === 'PAYOUT' ? '已是最后阶段' : 'EVAL → FUNDED → PAYOUT'"
          @click="doPromote"
        >
          <Icon name="check" :size="14" />升级
        </button>
        <button
          type="button"
          class="rounded-md border border-line px-4 py-2 text-[13px] text-dim transition-colors hover:border-line-strong hover:text-ink"
          :disabled="busy"
          @click="startRename"
        >
          改名
        </button>
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-md border border-line px-4 py-2 text-[13px] text-faint transition-colors hover:border-red-200 hover:text-red-700"
          :disabled="busy"
          @click="doRemove"
        >
          <Icon name="trash" :size="14" />删除
        </button>
      </div>

      <!-- 占位：还没接的东西写清楚，别让人以为加载失败 -->
      <div class="mt-6 rounded-xl border border-dashed border-line px-5 py-4">
        <p class="text-[13px] font-medium text-dim">这一页还差几块</p>
        <ul class="mt-1.5 list-disc space-y-1 pl-5 text-[12px] text-faint">
          <li>成交记录与逐笔盈亏 <span class="text-faint">（`audit_event` 已落库，接口还没做）</span></li>
          <li>当前规则快照的完整字段（单笔手数上限、日亏损上限等）</li>
          <li>交易页里的下单 / 撤单 / 逐根推进 <span class="text-faint">（撮合后端还没接通，见 `TRADING-ENGINE-DESIGN.md`）</span></li>
        </ul>
      </div>
    </div>
  </section>
</template>
