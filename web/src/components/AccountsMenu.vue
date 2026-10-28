<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'

import { phaseClass, phaseLabel } from '@/lib/account'
import { usePlaybackStore } from '@/stores/playback'
import { useReplayStore } from '@/stores/replay'

/**
 * 右上角的「标的与考核账户」：一个按钮 + 一个折叠面板。
 *
 * 为什么从左侧栏搬到这里：复盘时 90% 的时间在看图，那一栏只占地方。
 * 需求里明确要求「以腾出画布空间为目标」，所以它默认收起，点开才有内容。
 *
 * v0.32.0：账户与考核账户合并，所以这里**只剩一个建户入口** ——
 * 【新建账户】【新增账户（最新）】【新增账户（开盘）】三个入口连同「时间点」输入框一起删掉了：
 * 考核账户必须从一个完整交易日的开盘开始，锚点只能由后端按停盘缺口挑（随机一个交易日）。
 *
 * 账户是**服务端**的概念：关掉页面不等于删户（随时能回来），只有主动删才消失。
 * 所以列表是服务端状态的投影，不是本地草稿。
 */

const store = useReplayStore()
const pb = usePlaybackStore()
const router = useRouter()

const open = ref(false)
const symbol = ref('')
const busy = ref(false)
/** 考核模板：首批只有 LucidFlex 50K，做成下拉是为了以后加更多不用改界面 */
const accountTemplate = ref('lucid-flex-50k')

const symbolInfo = computed(() => store.symbols.find((s) => s.symbol === symbol.value))

/**
 * 【新建考核账户】下面那行说明。
 *
 * 三件事必须提前说清：账户会随机落到某个交易日（不是今天）、
 * 只有 MES/MNQ/MGC 有手续费档位所以只能选它们、规则建户即冻结。
 */
const accountHint = computed(
  () =>
    '随机挑一个交易日开盘建户，规则在那一刻冻结。首批只支持 MES / MNQ / MGC（有手续费档位），' +
    '交易照常自动存进交易记录。',
)

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

/**
 * 回放中要切到考核账户：先安静地退出回放（不自动 join 旧账户，免得连发两条 join）。
 * 回放是只读的临时视图，它和「当前账户」是互斥的 —— 两者同时存在只会让顶栏自相矛盾。
 */
function leavePlayback(): void {
  if (pb.active) pb.close(false)
}

/** 进账户 = 换 URL（刷新/分享/返回键都对得上），join 由画布那一页按 :id 发出去 */
function enter(id: string): void {
  leavePlayback()
  void router.push({ name: 'replay', params: { id } })
}

/**
 * 【新建考核账户】：选一个规则模板，后端随机挑一个交易日的开盘首根建户。
 *
 * 锚点不由前端给：「哪一天算交易日」要看停盘缺口、开盘是 6 点还是 7 点要看美国夏令时，
 * 前端猜不出来。这条路上也没有「时间点」可填 —— 考核账户必须从一个完整交易日的开头开始，
 * 否则日终结算和当日封顶都无从谈起。
 */
function createAccount(): void {
  if (!symbol.value) return
  leavePlayback()
  busy.value = true
  store.createInstance(symbol.value, '', false, true, accountTemplate.value)
  window.setTimeout(() => {
    busy.value = false
  }, 600)
}

function remove(id: string): void {
  if (window.confirm('删除这个考核账户？标注、挂单、成交记录都会一起没掉（不影响 CSV 数据）。')) {
    store.deleteInstance(id)
  }
}

/**
 * 一行状态文字：机器人在跑时把策略名带上。
 * 列表里只存 id，名字从策略清单查 —— 清单没到就退回显示 id，总之不显示空白。
 */
function statusOf(inst: { operator: string; bot: boolean; botStrategy?: string }): string {
  if (inst.bot) {
    if (!inst.botStrategy) return '机器人'
    const name = store.strategies.find((s) => s.id === inst.botStrategy)?.name ?? inst.botStrategy
    return `机器人 · ${name}`
  }
  if (!inst.operator) return '无人操作'
  return inst.operator
}
</script>

<template>
  <div class="relative">
    <button
      type="button"
      class="flex items-center gap-2 rounded border border-line bg-panel px-2 py-1 text-[13px] text-ink hover:border-accent"
      :class="open ? 'border-accent' : ''"
      @click="open = !open"
    >
      <span>标的与考核账户</span>
      <span class="rounded bg-accent-soft px-1 text-[12px] text-accent">{{ store.instances.length }}</span>
      <span v-if="store.instance" class="max-w-[9rem] truncate text-dim">{{ store.instance.symbol }}</span>
      <span class="text-faint">{{ open ? '▲' : '▼' }}</span>
    </button>

    <!-- 点面板外面收起：用一个透明全屏遮罩，比给 document 加监听更好收拾 -->
    <div v-if="open" class="fixed inset-0 z-30" @click="open = false" />

    <div
      v-if="open"
      class="absolute right-0 z-40 mt-1 w-[24rem] rounded border border-line bg-panel p-3 text-ink shadow-2xl"
    >
      <div class="space-y-2">
        <label class="block text-[13px] text-dim">标的（来自 data/ 目录）</label>
        <select
          v-model="symbol"
          class="w-full rounded border border-line bg-surface px-2 py-1 text-[14px] text-ink"
        >
          <option v-if="store.symbols.length === 0" value="">（数据目录里还没有标的）</option>
          <option v-for="s in store.symbols" :key="s.symbol" :value="s.symbol">
            {{ s.symbol }} · {{ s.days }} 天
          </option>
        </select>
        <div v-if="symbolInfo" class="text-[12px] text-faint">
          {{ symbolInfo.firstDate }} ~ {{ symbolInfo.lastDate }} · 跳动
          {{ symbolInfo.tick }} · 目录 {{ symbolInfo.folder }}
        </div>

        <label class="block text-[13px] text-dim">考核模板</label>
        <select
          v-model="accountTemplate"
          class="w-full rounded border border-line bg-surface px-2 py-1 text-[14px] text-ink"
        >
          <option value="lucid-flex-50k">LucidFlex 50K · 目标 3,000 / 回撤 2,000 / 5 个盈利日</option>
        </select>

        <button
          type="button"
          class="w-full rounded bg-accent px-2 py-1.5 text-[14px] font-medium text-white hover:opacity-90 disabled:opacity-50"
          :disabled="busy || store.symbols.length === 0 || store.status !== 'open'"
          title="建一个 Prop Firm 考核账户：随机一个交易日开盘，按模板扣手续费、算余额和亏损线"
          @click="createAccount"
        >
          {{ busy ? '加载中…' : '新建考核账户' }}
        </button>
        <div class="text-[12px] text-faint">{{ accountHint }}</div>
      </div>

      <div
        v-if="pb.active"
        class="mt-2 rounded border border-amber-400 bg-amber-500/10 px-2 py-1 text-[12px] text-amber-700"
      >
        正在回放录像（只读）。新建或进入账户会先退出回放。
      </div>

      <div class="mt-3 flex items-center justify-between border-t border-line pt-2">
        <span class="text-[13px] text-dim">考核账户 {{ store.instances.length }} 个</span>
        <div class="flex items-center gap-2">
          <button type="button" class="text-[13px] text-accent hover:underline" @click="store.refresh()">
            刷新
          </button>
          <button
            v-if="store.instance"
            type="button"
            class="text-[13px] text-dim hover:text-ink"
            @click="store.leaveInstance()"
          >
            离开当前
          </button>
        </div>
      </div>

      <div class="mt-1 max-h-[46vh] overflow-auto">
        <div v-if="store.instances.length === 0" class="py-2 text-[13px] text-faint">
          还没有考核账户。选个标的、点「新建考核账户」开始。
        </div>
        <div
          v-for="inst in store.instances"
          :key="inst.id"
          class="border-t border-line/70 py-1.5 text-[13px] first:border-t-0"
        >
          <div class="flex items-center gap-2">
            <span class="truncate font-medium">{{ inst.symbol }}</span>
            <span class="ml-auto shrink-0 text-[12px] text-faint">{{ inst.id }}</span>
          </div>
          <div class="tnum mt-0.5 text-[12px] text-dim">{{ inst.anchor }}</div>
          <div class="mt-0.5 flex flex-wrap items-center gap-x-2 text-[12px] text-faint">
            <span>第 {{ inst.cursor + 1 }} 根</span>
            <span
              v-if="inst.accountPhase"
              class="rounded px-1 py-px text-[11px] font-medium"
              :class="phaseClass(inst.accountPhase)"
            >
              {{ phaseLabel(inst.accountPhase) }}
            </span>
            <span>· {{ inst.members }} 人</span>
            <span>· {{ statusOf(inst) }}</span>
            <span v-if="inst.trades > 0">· {{ inst.trades }} 笔</span>
            <span
              v-if="inst.realizedPnl !== 0"
              :class="inst.realizedPnl > 0 ? 'text-up' : 'text-down'"
            >
              · {{ inst.realizedPnl > 0 ? '+' : '' }}{{ inst.realizedPnl.toFixed(2) }}
            </span>
          </div>
          <div class="mt-0.5 flex items-center gap-2">
            <button
              v-if="store.instance?.id !== inst.id"
              type="button"
              class="text-[12px] text-accent hover:underline"
              @click="enter(inst.id)"
            >
              进入
            </button>
            <span v-else class="text-[12px] text-emerald-600">当前</span>
            <button
              type="button"
              class="ml-auto text-[12px] text-faint hover:text-red-500"
              title="只有创建者或当前操作者能删"
              @click="remove(inst.id)"
            >
              删除
            </button>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
