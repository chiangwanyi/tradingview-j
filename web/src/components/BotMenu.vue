<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { useReplayStore } from '@/stores/replay'
import type { StrategyParam } from '@/types/protocol'

/**
 * 机器人启动入口：选一个策略，设好手数和参数，点开始。
 *
 * 两种机器人共用一个按钮：
 *   - 不选策略 = 原来的「随机演示机器人」，步进 + 乱画线 + 乱下单，只用来验证链路；
 *   - 选了策略 = 策略机器人，每推进一根 K 线问策略一次，该开仓就开仓，
 *     并自动挂上止损止盈两张保护单、收盘前平掉。
 *
 * 这里只负责「把选择发给后端」：参数校验、策略是否存在、手数合不合法，
 * 全在后端（见 handleBotStart）。前端提前拦一遍只是为了让按钮别白点，
 * 真正的判定只能有一处 —— 否则两边迟早说的不是一回事。
 */

const store = useReplayStore()

const open = ref(false)
/** 选中的策略 id；空串 = 随机演示机器人 */
const picked = ref('')
/** 参数当前值。换策略时整份重置成该策略的出厂默认。 */
const params = ref<Record<string, number>>({})
const root = ref<HTMLElement | null>(null)

const canStart = computed(() => {
  const inst = store.instance
  if (!inst || inst.bot || store.playbackActive) return false
  return inst.operator === '' || store.isOperator
})

const pickedStrategy = computed(() => store.strategies.find((s) => s.id === picked.value) ?? null)

/** 策略清单还没到、或者后端一个策略都没注册时，面板里至少还有随机演示可选。 */
const options = computed(() => [
  {
    id: '',
    name: '随机演示',
    summary: '步进 + 乱画线 + 乱下单，只用来验证链路，不做任何判断。',
  },
  ...store.strategies.map((s) => ({ id: s.id, name: s.name, summary: s.summary })),
])

/** 0/1 开关型参数用勾选框，其余用数字框 —— 让「允许做多」别长得像个刻度。 */
function isToggle(p: StrategyParam): boolean {
  return p.min === 0 && p.max === 1 && p.step === 1
}

watch(pickedStrategy, (s) => {
  const next: Record<string, number> = {}
  for (const p of s?.params ?? []) next[p.key] = p.default
  params.value = next
})

function toggleOpen(): void {
  if (!canStart.value) return
  open.value = !open.value
  // 清单是连上时取的那一份；用户可能是之后才重连的，这里补一次不算浪费
  if (open.value && store.strategies.length === 0) store.listStrategies()
}

function start(): void {
  if (!canStart.value) return
  store.startBot({
    strategy: picked.value,
    quantity: store.orderQuantity,
    params: picked.value ? { ...params.value } : undefined,
  })
  open.value = false
}

function onDocClick(e: MouseEvent): void {
  if (!open.value) return
  if (root.value && !root.value.contains(e.target as Node)) open.value = false
}

onMounted(() => document.addEventListener('click', onDocClick))
onBeforeUnmount(() => document.removeEventListener('click', onDocClick))

const btn = 'rounded border px-2 py-1 text-[12px] transition disabled:cursor-not-allowed disabled:opacity-40'
</script>

<template>
  <div ref="root" class="relative">
    <!-- 跑起来之后按钮变成「停止 XXX」：用户随时知道怎么让它停下来 -->
    <button
      v-if="!store.botRunning"
      type="button"
      :class="[btn, 'border-sky-600 text-sky-600 hover:bg-sky-600/10 dark:text-sky-400']"
      :disabled="!canStart"
      title="选一个策略让机器人自己跑：自动步进、判断开仓、挂止损止盈、收盘前平仓"
      @click.stop="toggleOpen"
    >
      启动机器人 ▾
    </button>
    <button
      v-else
      type="button"
      class="rounded bg-sky-600 px-2 py-1 text-[12px] text-white hover:bg-sky-500"
      :title="`正在跑：${store.botStrategyLabel || '随机演示'}。停止后持仓保留、保护单撤掉，操作权交回给你`"
      @click="store.stopBot()"
    >
      停止{{ store.botStrategyLabel ? ' · ' + store.botStrategyLabel : '机器人' }}
    </button>

    <div
      v-if="open"
      class="absolute right-0 top-full z-40 mt-1 max-h-[70vh] w-[23rem] overflow-y-auto rounded-md border border-line bg-panel p-3 shadow-xl"
    >
      <div class="mb-2 text-[11px] font-medium text-dim">选择策略</div>

      <button
        v-for="opt in options"
        :key="opt.id || 'random'"
        type="button"
        :class="[
          'mb-1 flex w-full items-start gap-2 rounded border px-2 py-1.5 text-left transition',
          picked === opt.id ? 'border-accent bg-accent-soft' : 'border-line hover:border-accent',
        ]"
        @click="picked = opt.id"
      >
        <span
          class="mt-[3px] h-2.5 w-2.5 shrink-0 rounded-full border"
          :class="picked === opt.id ? 'border-accent bg-accent' : 'border-faint'"
        />
        <span class="min-w-0">
          <span class="block text-[12px] text-ink">{{ opt.name }}</span>
          <span class="block text-[11px] leading-snug text-dim">{{ opt.summary }}</span>
        </span>
      </button>

      <!--
        参数：默认值就是出厂值，改了只影响这一次启动（后端不存）。
        数值在发出去之前不夹范围 —— 后端 Merge 会夹，前端夹一遍只会让用户
        不知道自己填的数到底被改成了多少。
      -->
      <div v-if="pickedStrategy" class="mt-2 rounded border border-line bg-elev px-2 py-1.5">
        <div class="mb-1 text-[11px] text-faint">运行参数（只影响这一次启动）</div>
        <div class="space-y-1">
          <label
            v-for="p in pickedStrategy.params"
            :key="p.key"
            class="flex items-center justify-between gap-2"
            :title="p.help"
          >
            <span class="text-[11px] text-dim">{{ p.label }}</span>
            <!--
              开关型参数不用 v-model：checkbox 的 v-model 收的是 boolean，
              而参数在协议里是数字（1/0）。两种绑定同时挂在同一个值上，
              谁先谁后取决于事件顺序，读起来也说不清最终是什么。
              这里改成「读 number、写 number」一条路。
            -->
            <input
              v-if="isToggle(p)"
              type="checkbox"
              :checked="params[p.key] >= 0.5"
              class="h-3.5 w-3.5 accent-sky-600"
              @change="params[p.key] = ($event.target as HTMLInputElement).checked ? 1 : 0"
            />
            <input
              v-else
              v-model.number="params[p.key]"
              type="number"
              :min="p.min"
              :max="p.max"
              :step="p.step"
              class="w-16 rounded border border-line bg-surface px-1 py-0.5 text-right text-[11px] text-ink"
            />
          </label>
        </div>
      </div>

      <div class="mt-2 flex items-center justify-between gap-2 border-t border-line pt-2">
        <label class="flex items-center gap-1.5 text-[11px] text-dim">
          每笔手数
          <input
            v-model.number="store.orderQuantity"
            type="number"
            min="1"
            step="1"
            class="w-14 rounded border border-line bg-surface px-1 py-0.5 text-right text-[11px] text-ink"
          />
        </label>
        <button
          type="button"
          class="rounded bg-sky-600 px-3 py-1 text-[12px] font-medium text-white hover:bg-sky-500 disabled:cursor-not-allowed disabled:opacity-40"
          :disabled="!canStart"
          @click="start"
        >
          开始
        </button>
      </div>

      <p class="mt-2 text-[10px] leading-snug text-faint">
        撮合是零滑点、零手续费的：市价单按当根收盘价成交，跳空时按开盘价。
        这里跑出来的盈亏是「最有利假设」，不是实盘能拿到的结果。
      </p>
    </div>
  </div>
</template>
