<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

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
/** 详情是整页切换，交给 App 管；这里只把 id 往上递。 */
const emit = defineEmits<{ detail: [id: string] }>()

defineExpose({ openNew })
</script>

<template>
  <section class="flex min-w-0 flex-1 flex-col overflow-y-auto">
    <header class="px-8 pt-10 pb-4">
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 class="text-[26px] font-semibold tracking-tight text-ink">账户总览</h1>
          <p class="mt-1 text-[13px] text-dim">这里是你所有 Prop Firm 考核账户的进度。</p>
        </div>
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-md bg-accent px-3 py-2 text-[13px] font-medium text-white transition-colors hover:bg-accent-ink"
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
          class="rounded-md px-3.5 py-1.5 text-[13px] transition-colors"
          :class="filter === f.key ? 'bg-panel font-medium text-ink shadow-sm' : 'text-dim hover:text-ink'"
          @click="filter = f.key"
        >
          {{ f.label }}
          <span class="tnum text-[11px] text-faint">{{ counts[f.key] }}</span>
        </button>
      </div>
    </header>

    <p
      v-if="error"
      class="mx-8 mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-700"
    >
      {{ error }}
    </p>

    <div class="px-8 pb-10">
      <div v-if="list.length" class="grid grid-cols-1 gap-4 md:grid-cols-2 xl:grid-cols-3">
        <AccountCard v-for="a in list" :key="a.id" :a="a" @detail="emit('detail', $event)" />
      </div>

      <p v-else class="py-16 text-center text-[13px] text-faint">
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
