<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'

import Icon from '@/components/Icon.vue'
import PlanCard from '@/components/PlanCard.vue'
import TemplateDialog from '@/components/TemplateDialog.vue'
import { blank, errorText, refresh, remove, save, store } from '@/data/store'
import type { Template } from '@/data/rules'

/** 参考图顶部的四段切换，这里当作阶段筛选（模板本身只有阶段这一个分类）。 */
const FILTERS = [
  { key: 'ALL', label: '全部' },
  { key: 'EVAL', label: 'EVAL' },
  { key: 'FUNDED', label: 'FUNDED' },
  { key: 'PAYOUT', label: 'PAYOUT' },
] as const

const FEATURES = ['EVAL 必须有盈利目标', 'FUNDED 不带目标', '删模板不动已开账户']

const filter = ref<(typeof FILTERS)[number]['key']>('ALL')
const list = computed(() =>
  filter.value === 'ALL' ? store.list : store.list.filter((t) => t.stage === filter.value),
)

const nameOf = (id: string | null): string | null =>
  id ? (store.list.find((t) => t.id === id)?.name ?? null) : null

// 数据在后端，进页面先拉一遍；失败时把原因摊在页面上，不要只剩一张空列表
const loading = ref(true)
const error = ref('')

onMounted(() => {
  refresh()
    .catch((e: unknown) => {
      error.value = errorText(e)
    })
    .finally(() => {
      loading.value = false
    })
})

// ---- 编辑 ----
const draft = ref<Template | null>(null)
const stageEditable = ref(false)

function openNew(): void {
  filter.value = 'ALL'
  draft.value = blank()
  stageEditable.value = true
  error.value = ''
}

function openEdit(t: Template): void {
  draft.value = { ...t, rules: { ...t.rules } }
  stageEditable.value = false
  error.value = ''
}

function onSave(t: Template): void {
  save(t)
    .then(() => {
      draft.value = null
      error.value = ''
    })
    .catch((e: unknown) => {
      error.value = errorText(e)
    })
}

function onRemove(t: Template): void {
  if (!confirm(`删掉「${t.name}」？已用它的规则开出来的账户不受影响。`)) return
  remove(t.id).catch((e: unknown) => {
    error.value = errorText(e)
  })
}

defineExpose({ openNew })

// ---- 横向翻页 ----
const CARD_STEP = 280 // 卡片宽度 + 间距，和 PlanCard 的 w-[268px] + gap-3 对齐
const track = ref<HTMLElement | null>(null)
const page = ref(0)
const pages = computed(() => Math.max(1, Math.ceil(list.value.length / 3)))

function shift(dir: -1 | 1): void {
  track.value?.scrollBy({ left: dir * CARD_STEP * 3, behavior: 'smooth' })
}

function onScroll(): void {
  const el = track.value
  if (el) page.value = Math.min(pages.value - 1, Math.round(el.scrollLeft / el.clientWidth))
}

function goPage(i: number): void {
  const el = track.value
  if (el) el.scrollTo({ left: i * el.clientWidth, behavior: 'smooth' })
}
</script>

<template>
  <section class="flex min-w-0 flex-1 flex-col overflow-y-auto">
    <header class="px-8 pt-10 pb-1">
      <h1 class="text-center text-[26px] font-semibold tracking-tight text-ink">账户模板</h1>

      <div class="mt-5 flex justify-center">
        <div class="inline-flex gap-0.5 rounded-lg border border-line bg-elev p-1">
          <button
            v-for="f in FILTERS"
            :key="f.key"
            type="button"
            class="rounded-md px-4 py-1.5 text-[13px] transition-colors"
            :class="
              filter === f.key
                ? 'bg-accent font-medium text-white'
                : 'text-dim hover:text-ink'
            "
            @click="filter = f.key; page = 0"
          >
            {{ f.label }}
          </button>
        </div>
      </div>

      <ul class="mt-4 flex flex-wrap justify-center gap-x-6 gap-y-1 text-[13px] text-dim">
        <li v-for="f in FEATURES" :key="f" class="flex items-center gap-1.5">
          <span class="text-accent-ink"><Icon name="check" :size="14" /></span>{{ f }}
        </li>
      </ul>
    </header>

    <p
      v-if="error"
      class="mx-8 mt-4 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-700"
    >
      {{ error }}
    </p>

    <div class="relative flex flex-1 flex-col justify-center pt-6 pb-6">
      <template v-if="list.length">
        <button
          type="button"
          class="absolute top-1/2 left-2 z-10 grid size-8 -translate-y-1/2 place-items-center rounded-full border border-line bg-panel text-dim shadow-sm hover:text-ink"
          title="往前"
          @click="shift(-1)"
        >
          <Icon name="left" :size="16" />
        </button>
        <button
          type="button"
          class="absolute top-1/2 right-2 z-10 grid size-8 -translate-y-1/2 place-items-center rounded-full border border-line bg-panel text-dim shadow-sm hover:text-ink"
          title="往后"
          @click="shift(1)"
        >
          <Icon name="right" :size="16" />
        </button>
      </template>

      <div
        ref="track"
        class="flex snap-x snap-mandatory gap-3 overflow-x-auto px-12 pb-3"
        @scroll="onScroll"
      >
        <PlanCard
          v-for="t in list"
          :key="t.id"
          :t="t"
          :next-name="nameOf(t.nextTemplateId)"
          @edit="openEdit(t)"
          @remove="onRemove(t)"
        />

        <p v-if="!list.length" class="w-full py-16 text-center text-[13px] text-faint">
          {{ loading ? '加载中…' : '这个阶段还没有模板。' }}
        </p>
      </div>

      <div v-if="pages > 1" class="mt-4 flex justify-center gap-1.5">
        <button
          v-for="i in pages"
          :key="i"
          type="button"
          class="h-[3px] w-6 rounded-full transition-colors"
          :class="page === i - 1 ? 'bg-accent' : 'bg-line-strong'"
          :aria-label="`第 ${i} 页`"
          @click="goPage(i - 1)"
        />
      </div>
    </div>

    <TemplateDialog
      v-if="draft"
      :draft="draft"
      :stage-editable="stageEditable"
      :others="store.list.filter((t) => t.id !== draft!.id)"
      :error="error"
      @save="onSave"
      @close="draft = null"
    />
  </section>
</template>
