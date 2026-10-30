<script setup lang="ts">
import { computed, ref } from 'vue'

import Icon from './Icon.vue'
import {
  promote as promoteApi,
  shortInstant,
  shortTime,
  stages as stagesApi,
  targetProgress,
} from '@/data/accounts'
import type { Account, StageLog } from '@/data/accounts'
import { drawdownLabel, pct, stageLabel, usd } from '@/data/rules'

/**
 * 一张考核卡。版式照参考图的账户卡：身份 → 三个状态小标签 → 四个数字 → 进度条 → 动作。
 *
 * 卡片自己拉阶段流水（只有点开才拉）：它只属于这一个账户，放到列表层统一管理会让
 * 每一张卡的展开状态和一份全局缓存互相追。
 */
const props = defineProps<{ a: Account }>()
const emit = defineEmits<{ rename: [id: string, name: string]; remove: [id: string]; changed: [] }>()

const progress = computed(() => targetProgress(props.a))
const gained = computed(() => props.a.balance - props.a.baseBalance)
/** 亏损线：跟踪日终口径下就是「日终峰值 − 最大回撤」。没有回撤规则就不编一个数。 */
const lossLine = computed(() =>
  props.a.rules.maxDrawdown == null ? null : props.a.eodPeakBalance - props.a.rules.maxDrawdown,
)

// ---- 改名 ----
const editing = ref(false)
const draftName = ref('')
const err = ref('')

function startRename(): void {
  draftName.value = props.a.name
  editing.value = true
  err.value = ''
}

function submitRename(): void {
  emit('rename', props.a.id, draftName.value.trim())
  editing.value = false
}

function stopRename(): void {
  editing.value = false
  err.value = ''
}

// ---- 阶段流水 ----
const logs = ref<StageLog[] | null>(null)
const loadingLogs = ref(false)

function toggleLogs(): void {
  if (logs.value) {
    logs.value = null
    return
  }
  loadingLogs.value = true
  err.value = ''
  stagesApi(props.a.id)
    .then((l) => {
      logs.value = l
    })
    .catch((e: unknown) => {
      err.value = e instanceof Error ? e.message : String(e)
    })
    .finally(() => {
      loadingLogs.value = false
    })
}

// ---- 动作 ----
const busy = ref(false)

function run(fn: () => Promise<unknown>): void {
  busy.value = true
  err.value = ''
  fn()
    .then(() => emit('changed'))
    .catch((e: unknown) => {
      err.value = e instanceof Error ? e.message : String(e)
    })
    .finally(() => {
      busy.value = false
    })
}

function doRemove(): void {
  if (confirm(`删掉「${props.a.name}」？成交、流水、图形都不会被删，只是这个账户不再列出。`)) {
    emit('remove', props.a.id)
  }
}

function doPromote(): void {
  if (!confirm(`把「${props.a.name}」升到下一阶段？余额会重置回新阶段的账户规模。`)) return
  run(() => promoteApi(props.a.id))
}
</script>

<template>
  <article class="flex flex-col rounded-xl border border-line bg-panel shadow-[0_1px_2px_rgba(15,23,42,0.04)]">
    <header class="flex items-start justify-between gap-2 px-4 pt-4">
      <div class="min-w-0">
        <input
          v-if="editing"
          v-model="draftName"
          class="w-full rounded-md border border-line bg-panel px-2 py-1 text-[14px] font-semibold text-ink outline-none focus:border-accent/60"
          @keyup.enter="submitRename"
          @keyup.esc="stopRename"
        />
        <h3 v-else class="truncate text-[14px] font-semibold tracking-wide text-ink">
          {{ a.name }}
        </h3>
        <p class="mt-0.5 truncate text-[11px] text-faint tnum">起点 {{ shortTime(a.startBarTime) }}</p>
      </div>
      <span
        class="shrink-0 rounded border border-line bg-elev px-1.5 py-0.5 text-[10px] tracking-wider text-dim"
        >{{ stageLabel(a.stage) }}</span
      >
    </header>

    <!-- 三个状态小标签：回撤口径 / 在考核中或已失败 / 本阶段起点 -->
    <div class="mt-2.5 flex flex-wrap gap-1 px-4">
      <span class="rounded-sm border border-line px-1.5 py-0.5 text-[9px] tracking-wider text-faint uppercase">
        {{ drawdownLabel(a.rules.drawdownType) }}
      </span>
      <span
        class="rounded-sm border px-1.5 py-0.5 text-[9px] tracking-wider uppercase"
        :class="
          a.status === 'ACTIVE'
            ? 'border-accent/30 bg-accent-soft text-accent-ink'
            : 'border-red-200 bg-red-50 text-red-700'
        "
      >
        {{ a.status === 'ACTIVE' ? '进行中' : '已失败' }}
      </span>
      <span class="rounded-sm border border-line px-1.5 py-0.5 text-[9px] tracking-wider text-faint uppercase">
        本阶段自 {{ shortInstant(a.stageStartedAt) }}
      </span>
    </div>

    <div class="mt-3 grid grid-cols-2 gap-x-3 gap-y-3 border-t border-line px-4 py-3.5">
      <div>
        <p class="text-[10px] tracking-wider text-faint uppercase">账户余额</p>
        <p class="tnum text-[17px] leading-tight font-semibold text-ink">{{ usd(a.balance) }}</p>
        <p class="tnum text-[10px] text-faint">
          起点 {{ usd(a.baseBalance) }} ·
          <span :class="gained >= 0 ? 'text-accent-ink' : 'text-red-600'">
            {{ gained >= 0 ? '+' : '' }}{{ usd(gained) }}
          </span>
        </p>
      </div>
      <div>
        <p class="text-[10px] tracking-wider text-faint uppercase">盈利目标</p>
        <p class="tnum text-[17px] leading-tight font-semibold text-ink">
          {{ usd(a.rules.profitTarget) }}
        </p>
        <p class="tnum text-[10px] text-faint">
          {{ a.rules.profitTarget == null ? '本阶段无目标' : `还差 ${usd(Math.max(0, a.rules.profitTarget - gained))}` }}
        </p>
      </div>
      <div>
        <p class="text-[10px] tracking-wider text-faint uppercase">最大回撤</p>
        <p class="tnum text-[13px] font-medium text-ink">{{ usd(a.rules.maxDrawdown) }}</p>
        <p class="tnum text-[10px] text-faint">亏损线 {{ usd(lossLine) }}</p>
      </div>
      <div>
        <p class="text-[10px] tracking-wider text-faint uppercase">一致性上限</p>
        <p class="tnum text-[13px] font-medium text-ink">
          {{ a.rules.consistencyRatio == null ? '—' : `≤${pct(a.rules.consistencyRatio)}` }}
        </p>
        <p class="tnum text-[10px] text-faint">
          {{ a.rules.minProfitDays == null ? '不查盈利日' : `盈利日 ${a.rules.minProfitDays} 天` }}
        </p>
      </div>
    </div>

    <!-- 进度条只在有目标时出现：FUNDED / PAYOUT 没有「差多少达标」这回事，画一根空槽会误导 -->
    <div v-if="progress != null" class="px-4 pb-3">
      <div class="h-1 w-full overflow-hidden rounded-full bg-elev">
        <div class="h-full rounded-full bg-accent" :style="{ width: `${progress * 100}%` }" />
      </div>
      <div class="mt-1 flex justify-between text-[9px] tracking-wider text-faint uppercase tnum">
        <span>起点 {{ usd(a.baseBalance) }}</span>
        <span>{{ pct(progress) }}</span>
        <span>目标 {{ usd(a.baseBalance + (a.rules.profitTarget ?? 0)) }}</span>
      </div>
    </div>

    <p v-if="err" class="mx-4 mb-2 rounded border border-red-200 bg-red-50 px-2 py-1 text-[11px] text-red-700">
      {{ err }}
    </p>

    <!-- 阶段流水：行数就是身份（1 行 = 建户时就在这个阶段，2 行 = 考过来的） -->
    <ul v-if="logs" class="mx-4 mb-3 space-y-1 rounded-lg border border-line bg-elev px-3 py-2">
      <li v-for="l in logs" :key="l.id" class="flex items-baseline justify-between gap-2 text-[11px]">
        <span class="text-dim">
          {{ stageLabel(l.stage) }}
          <span class="text-faint">{{ shortInstant(l.startedAt) }}</span>
        </span>
        <span class="tnum text-faint">
          {{ l.endedAt ? (l.netPnl == null ? '—' : `净额 ${usd(l.netPnl)}`) : '进行中' }}
        </span>
      </li>
    </ul>
    <p v-else-if="loadingLogs" class="mx-4 mb-3 text-[11px] text-faint">读取流水…</p>

    <div class="mt-auto grid grid-cols-2 gap-1.5 px-4 pb-4">
      <template v-if="editing">
        <button
          type="button"
          class="rounded-md border border-accent/40 py-1.5 text-[12px] font-medium text-accent-ink hover:bg-accent-soft"
          @click="submitRename"
        >
          保存
        </button>
        <button
          type="button"
          class="rounded-md border border-line py-1.5 text-[12px] text-dim hover:border-line-strong"
          @click="stopRename"
        >
          取消
        </button>
      </template>
      <template v-else>
        <button
          type="button"
          class="flex items-center justify-center gap-1 rounded-md border border-accent/40 py-1.5 text-[12px] font-medium text-accent-ink transition-colors hover:bg-accent-soft disabled:cursor-not-allowed disabled:border-line disabled:text-faint disabled:hover:bg-transparent"
          :disabled="busy || a.status === 'FAILED' || a.stage === 'PAYOUT'"
          :title="a.stage === 'PAYOUT' ? '已是最后阶段' : 'EVAL → FUNDED → PAYOUT'"
          @click="doPromote"
        >
          <Icon name="check" :size="13" />升级
        </button>
        <button
          type="button"
          class="rounded-md border border-line py-1.5 text-[12px] text-dim transition-colors hover:border-line-strong"
          @click="toggleLogs"
        >
          {{ logs ? '收起流水' : '阶段流水' }}
        </button>
        <button
          type="button"
          class="rounded-md border border-line py-1.5 text-[12px] text-dim transition-colors hover:border-line-strong"
          @click="startRename"
        >
          改名
        </button>
        <button
          type="button"
          class="flex items-center justify-center gap-1 rounded-md border border-line py-1.5 text-[12px] text-faint transition-colors hover:border-red-200 hover:text-red-700"
          :disabled="busy"
          @click="doRemove"
        >
          <Icon name="trash" :size="13" />删除
        </button>
      </template>
    </div>
  </article>
</template>
