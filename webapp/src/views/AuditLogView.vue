<script setup lang="ts">
import { onMounted, ref } from 'vue'

import Icon from '@/components/Icon.vue'
import PagerBar from '@/components/PagerBar.vue'
import { ACTION_LABEL, auditEvents, clearAuditEvents, shortInstant } from '@/data/history'
import type { AuditRow } from '@/data/history'
import { errorText } from '@/data/api'

/**
 * 操作记录：`audit_event` 的全量分页浏览（**只读**）+ 一个清空按钮。
 *
 * 列表跨账户、按写入时间倒序 —— 这个页面回答的是「我到底干过些什么」。
 * 清空是两步确认（点一次变成「再点一下确认」），因为不可逆，而且会顺手把
 * 各账户的 seq 归零；交易记录不受影响（它锚在 trade.seq 上）。
 */

const SIZE = 50

const page = ref(0)
const total = ref(0)
const rows = ref<AuditRow[]>([])
const loading = ref(true)
const error = ref('')

function load(): Promise<void> {
  loading.value = true
  return auditEvents(page.value, SIZE)
    .then((p) => {
      rows.value = p.items
      total.value = p.total
      error.value = ''
    })
    .catch((e: unknown) => {
      error.value = errorText(e)
    })
    .finally(() => {
      loading.value = false
    })
}

function go(p: number): void {
  page.value = p
  load()
}

onMounted(load)

// ---- 清空（两步确认）----
const armed = ref(false)
const clearing = ref(false)

function onClearClick(): void {
  if (!armed.value) {
    armed.value = true
    return
  }
  armed.value = false
  clearing.value = true
  clearAuditEvents()
    .then(() => {
      page.value = 0
      return load()
    })
    .catch((e: unknown) => {
      error.value = errorText(e)
    })
    .finally(() => {
      clearing.value = false
    })
}

const ACTOR_LABEL: Record<string, string> = { human: '真人', bot: '机器人', system: '系统' }

const symbolTail = (id: string): string => id.split(':').pop() ?? id
</script>

<template>
  <section class="flex min-w-0 flex-1 flex-col overflow-y-auto">
    <header class="px-8 pt-10 pb-4">
      <div class="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 class="text-[26px] font-semibold tracking-tight text-ink">操作记录</h1>
          <p class="mt-1 text-[13px] text-dim">
            所有账户的每一步操作（挂单、画图、下一根、语音…），按写入时间倒序。只做查看。
          </p>
        </div>
        <button
          type="button"
          class="flex items-center gap-1.5 rounded-md border px-3 py-2 text-[13px] font-medium transition-colors disabled:cursor-default disabled:opacity-50"
          :class="
            armed
              ? 'border-red-300 bg-red-50 text-red-700 hover:bg-red-100'
              : 'border-line text-dim hover:bg-elev'
          "
          :disabled="clearing || total === 0"
          @click="onClearClick"
          @blur="armed = false"
        >
          <Icon name="trash" :size="15" />
          {{ clearing ? '清空中…' : armed ? '再点一下确认清空（不可撤销）' : '清空操作记录' }}
        </button>
      </div>
    </header>

    <p
      v-if="error"
      class="mx-8 mb-3 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-700"
    >
      {{ error }}
    </p>

    <div class="flex min-h-0 flex-1 flex-col px-8 pb-10">
      <div class="min-h-0 flex-1 overflow-auto rounded-lg border border-line bg-panel">
        <table class="w-full text-[13px]">
          <thead class="sticky top-0 bg-elev text-left text-faint">
            <tr>
              <th class="px-3 py-2 font-normal">时间</th>
              <th class="px-3 py-2 font-normal">账户</th>
              <th class="px-3 py-2 font-normal">标的</th>
              <th class="px-3 py-2 font-normal">K 线</th>
              <th class="px-3 py-2 font-normal">动作</th>
              <th class="px-3 py-2 text-right font-normal">序号</th>
              <th class="px-3 py-2 font-normal">谁</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="r in rows" :key="r.id" class="border-t border-line/60">
              <td class="tnum px-3 py-1.5 text-dim">{{ shortInstant(r.createdAt) }}</td>
              <td class="px-3 py-1.5 text-ink">
                {{ r.accountName ?? `已删账户 #${r.accountId.slice(0, 8)}` }}
              </td>
              <td class="px-3 py-1.5 text-dim">{{ symbolTail(r.symbolId) }}</td>
              <td class="tnum px-3 py-1.5 text-faint">{{ r.barTime.slice(5) }}</td>
              <td class="px-3 py-1.5 text-ink">{{ ACTION_LABEL[r.action] ?? r.action }}</td>
              <td class="tnum px-3 py-1.5 text-right text-faint">{{ r.seq }}</td>
              <td class="px-3 py-1.5 text-dim">{{ ACTOR_LABEL[r.actor] ?? r.actor }}</td>
            </tr>
          </tbody>
        </table>

        <p v-if="!rows.length" class="px-3 py-16 text-center text-[13px] text-faint">
          <template v-if="loading">加载中…</template>
          <template v-else>还没有操作记录。去交易页下一单、画个线，这里就有了。</template>
        </p>
      </div>

      <PagerBar :page="page" :size="SIZE" :total="total" @go="go" />
    </div>
  </section>
</template>
