<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import { useReplayStore } from '@/stores/replay'

/**
 * 左侧：选标的 + 建实例 + 实例列表。
 *
 * 需求里的「实例」是服务端的概念：选标的和时间 -> 后端加载 K 线并记为一个实例。
 * 关掉页面不等于删实例（可以随时回来），只有主动删才消失。所以这里的列表是
 * 服务端状态的投影，不是本地草稿。
 */

const store = useReplayStore()

const symbol = ref('')
const timeText = ref('')
const busy = ref(false)

const currentSymbolInfo = computed(() => store.symbols.find((s) => s.symbol === symbol.value))

onMounted(() => {
  store.refresh()
})

// 标的名录第一次到达时默认选一个，免得用户以为下拉框坏了
watch(
  () => store.symbols,
  (list) => {
    if (!symbol.value && list.length > 0) symbol.value = list[0].symbol
  },
  { immediate: true },
)

function create(): void {
  if (!symbol.value) return
  busy.value = true
  store.createInstance(symbol.value, timeText.value.trim())
  window.setTimeout(() => {
    busy.value = false
  }, 600)
}

function remove(id: string): void {
  if (window.confirm('删除这个实例？标注、挂单、成交记录都会一起没掉（不影响 CSV 数据）。')) {
    store.deleteInstance(id)
  }
}

function statusOf(operator: string, bot: boolean): string {
  if (bot) return '机器人操作中'
  if (!operator) return '无人操作'
  return `${operator} 操作中`
}
</script>

<template>
  <div class="flex h-full flex-col">
    <div class="border-b border-slate-800 px-3 py-2 text-xs font-semibold tracking-wide text-slate-300">
      标的与实例
    </div>

    <div class="space-y-2 border-b border-slate-800 p-3">
      <label class="block text-[11px] text-slate-400">标的（来自 data/ 目录）</label>
      <select
        v-model="symbol"
        class="w-full rounded border border-slate-700 bg-slate-900 px-2 py-1 text-xs text-slate-200"
      >
        <option v-if="store.symbols.length === 0" value="">（数据目录里还没有标的）</option>
        <option v-for="s in store.symbols" :key="s.symbol" :value="s.symbol">
          {{ s.symbol }} · {{ s.days }} 天
        </option>
      </select>
      <div v-if="currentSymbolInfo" class="text-[10px] text-slate-500">
        {{ currentSymbolInfo.firstDate }} ~ {{ currentSymbolInfo.lastDate }} · 目录
        {{ currentSymbolInfo.folder }}
      </div>

      <label class="block text-[11px] text-slate-400">时间点（留空 = 随机）</label>
      <input
        v-model="timeText"
        type="text"
        placeholder="2026-10-16 12:00"
        class="w-full rounded border border-slate-700 bg-slate-900 px-2 py-1 font-mono text-xs text-slate-200 placeholder:text-slate-600"
      />
      <div class="text-[10px] text-slate-500">
        加载这个时间点及之前的 400 根 K 线作为画布，之后的行情逐根揭示。
      </div>

      <button
        type="button"
        class="w-full rounded bg-sky-600 px-2 py-1.5 text-xs font-medium text-white hover:bg-sky-500 disabled:bg-slate-700"
        :disabled="busy || store.symbols.length === 0 || store.status !== 'open'"
        @click="create"
      >
        {{ busy ? '加载中…' : '新建实例' }}
      </button>
    </div>

    <div class="flex items-center justify-between border-b border-slate-800 px-3 py-2">
      <span class="text-[11px] text-slate-400">实例 {{ store.instances.length }} 个</span>
      <button type="button" class="text-[11px] text-sky-400 hover:text-sky-300" @click="store.refresh()">
        刷新
      </button>
    </div>

    <div class="min-h-0 flex-1 overflow-auto">
      <div v-if="store.instances.length === 0" class="p-3 text-[11px] text-slate-500">
        还没有实例。选个标的、点「新建实例」开始复盘。
      </div>
      <div
        v-for="inst in store.instances"
        :key="inst.id"
        class="border-b border-slate-800/70 px-3 py-2 text-[11px]"
        :class="store.instance?.id === inst.id ? 'bg-sky-500/10' : 'hover:bg-slate-800/40'"
      >
        <div class="flex items-center gap-2">
          <span class="truncate font-medium text-slate-200">{{ inst.symbol }}</span>
          <span class="ml-auto shrink-0 text-[10px] text-slate-500">{{ inst.id }}</span>
        </div>
        <div class="mt-0.5 font-mono text-[10px] text-slate-400">{{ inst.anchor }}</div>
        <div class="mt-1 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px] text-slate-500">
          <span>第 {{ inst.cursor + 1 }} 根</span>
          <span>· {{ inst.members }} 人在看</span>
          <span :class="inst.bot ? 'text-sky-300' : ''">· {{ statusOf(inst.operator, inst.bot) }}</span>
          <span v-if="inst.trades > 0">· {{ inst.trades }} 笔成交</span>
          <span v-if="inst.realizedPnl !== 0" :class="inst.realizedPnl > 0 ? 'text-red-400' : 'text-emerald-400'">
            · {{ inst.realizedPnl > 0 ? '+' : '' }}{{ inst.realizedPnl.toFixed(2) }}
          </span>
        </div>
        <div class="mt-1 flex items-center gap-2">
          <button
            v-if="store.instance?.id !== inst.id"
            type="button"
            class="text-[10px] text-sky-400 hover:text-sky-300"
            @click="store.joinInstance(inst.id)"
          >
            进入
          </button>
          <span v-else class="text-[10px] text-emerald-400">当前</span>
          <button
            type="button"
            class="ml-auto text-[10px] text-slate-500 hover:text-red-400"
            title="只有创建者或当前操作者能删"
            @click="remove(inst.id)"
          >
            删除
          </button>
        </div>
      </div>
    </div>

    <div v-if="store.instance" class="border-t border-slate-800 p-3">
      <button
        type="button"
        class="w-full rounded border border-slate-700 px-2 py-1 text-[11px] text-slate-300 hover:border-slate-500"
        @click="store.leaveInstance()"
      >
        退出当前实例（保留在列表里）
      </button>
    </div>
  </div>
</template>
