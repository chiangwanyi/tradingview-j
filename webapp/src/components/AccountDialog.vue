<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import Icon from './Icon.vue'
import { randomName } from '@/data/accounts'
import { symbols as loadSymbolList } from '@/data/bars'
import type { SymbolBrief } from '@/data/bars'
import { stageLabel, usd } from '@/data/rules'
import type { Template } from '@/data/rules'

/**
 * 开账户：选一套模板 + 起个名字 + **指定唯一标的**（+ 可选的起点年份）。
 *
 * 起点 K 线由后端在该标的的日文件里随机抽（当天 09:00 及之后第一根），余额和规则都是
 * 模板的副本。这里不提供「自定义余额」之类的入口：余额只能从成交重算出来后同步。
 *
 * 标的是三个输入里**唯一不可逆**的那个 —— 建完就锁死，交易页也只有一个标的，
 * 所以表单上写清楚，别让人以为以后还能换。
 *
 * 「起点年份」只是**缩小随机范围**：选 2024 就在 2024 的日文件里随便抽一天，
 * 不选就跨全部年份随机。它跟着标的变化（年份列表来自该标的磁盘上真实存在的文件），
 * 所以换标的时要把已选年份清掉，否则会带着上一年的选择去撞「这一年没有数据」的报错。
 */
const props = defineProps<{ templates: Template[]; error: string }>()
const emit = defineEmits<{
  save: [{ templateId: string; name: string; symbolId: string; year: number | null }]
  close: []
}>()

const templateId = ref(props.templates[0]?.id ?? '')
const name = ref('')
const local = ref('')

/** 骰子：词库在后端，点一下换一个名字；连点用 rolling 挡住，避免回来的是上一次的结果。 */
const rolling = ref(false)
async function roll(): Promise<void> {
  rolling.value = true
  try {
    name.value = (await randomName()).name
  } catch (e) {
    local.value = `随机名没拿到：${e instanceof Error ? e.message : String(e)}`
  } finally {
    rolling.value = false
  }
}

// ---- 标的：清单来自 data/_symbols.json（和交易页同一个接口，`days` 是磁盘上的日文件数） ----
const symbols = ref<SymbolBrief[]>([])
const symbolId = ref('')
const symbolErr = ref('')
const loadingSymbols = ref(true)

/** 有 K 线才建得出账户（后端要在它的日文件里抽起点），没有的仍然列出来但不可选。 */
const usable = computed(() => symbols.value.filter((s) => s.days > 0))

onMounted(async () => {
  try {
    symbols.value = await loadSymbolList()
    symbolId.value = usable.value[0]?.code ?? symbols.value[0]?.code ?? ''
  } catch (e) {
    symbolErr.value = `标的清单读取失败：${e instanceof Error ? e.message : String(e)}`
  } finally {
    loadingSymbols.value = false
  }
})

const chosen = computed(() => props.templates.find((t) => t.id === templateId.value) ?? null)
const chosenSymbol = computed(() => symbols.value.find((s) => s.code === symbolId.value) ?? null)

/**
 * 起点年份：`''` = 任意年份（完全随机），否则是那一年。
 *
 * 存字符串是因为 `<select>` 的 `v-model` 给回来的一定是字符串 ——
 * 在这里转成数字（提交时）比在下拉里塞对象简单，也不会出现 `0 === ''` 这种蠢问题。
 */
const yearPick = ref('')
const yearOptions = computed<number[]>(() => chosenSymbol.value?.years ?? [])

// 换标的 → 重选年份。上一个标的的年份列表对新标的不成立，留着只会换来一次「这一年没有数据」。
watch(symbolId, () => {
  yearPick.value = ''
})

// 模板列表是异步拉回来的：组件挂载时还是空的，等它到了再自动选中第一套，
// 否则下拉框停在空值上（看着像「没有模板」，其实只是没选中）。
watch(
  () => props.templates,
  (list) => {
    if (!templateId.value && list.length) templateId.value = list[0].id
  },
  { immediate: true },
)

function submit(): void {
  if (!templateId.value) {
    local.value = '先选一套模板（没有模板的话去左边「账户模板」建一套）'
    return
  }
  if (!symbolId.value) {
    local.value = '得指定一个标的：账户只做这一个品种，创建后不能换'
    return
  }
  if (!name.value.trim()) {
    local.value = '账户得有名字'
    return
  }
  local.value = ''
  emit('save', {
    templateId: templateId.value,
    symbolId: symbolId.value,
    name: name.value.trim(),
    year: yearPick.value ? Number(yearPick.value) : null,
  })
}

const inputCls =
  'w-full rounded-md border border-line bg-panel px-2.5 py-1.5 text-body text-ink outline-none transition-colors focus:border-accent/60 focus:ring-2 focus:ring-accent/15'
const labelCls = 'mb-1 block text-note font-medium text-faint uppercase'
</script>

<template>
  <div class="fixed inset-0 z-20 flex items-center justify-center bg-ink/25 p-6" @click.self="emit('close')">
    <div class="w-full max-w-md rounded-xl border border-line bg-panel p-5 shadow-lg">
      <div class="flex items-start justify-between">
        <div>
          <h2 class="text-section font-semibold text-ink">新建考核账户</h2>
          <p class="mt-0.5 text-note text-faint">
            规则在这一刻从模板复制进账户，标的一刻定死 —— 之后都不能换。
          </p>
        </div>
        <button type="button" class="text-faint hover:text-ink" title="关闭" @click="emit('close')">
          <Icon name="close" :size="16" />
        </button>
      </div>

      <div class="mt-4 space-y-3">
        <div>
          <label :class="labelCls" for="tpl">用哪套模板</label>
          <select id="tpl" v-model="templateId" :class="inputCls">
            <option v-for="t in props.templates" :key="t.id" :value="t.id">
              {{ t.name }} · {{ stageLabel(t.stage) }}
            </option>
          </select>
          <p v-if="chosen" class="mt-1 text-note text-faint num">
            规模 {{ usd(chosen.rules.accountSize) }} · 目标 {{ usd(chosen.rules.profitTarget) }} · 回撤
            {{ usd(chosen.rules.maxDrawdown) }}
            <span v-if="chosen.nextTemplateId == null"> · 这条链到此为止</span>
          </p>
          <p v-else-if="!props.templates.length" class="mt-1 text-note text-red-700">
            还没有模板。先去「账户模板」建一套 EVAL，账户才有规则可签。
          </p>
        </div>

        <div>
          <label :class="labelCls" for="symbol">交易标的（必填，创建后不可更改）</label>
          <select id="symbol" v-model="symbolId" :class="inputCls" :disabled="loadingSymbols">
            <option v-if="loadingSymbols" value="">标的加载中…</option>
            <option
              v-for="s in symbols"
              :key="s.code"
              :value="s.code"
              :disabled="s.days === 0"
            >
              {{ s.code }}{{ s.days === 0 ? '（还没有 K 线，不可选）' : `（${s.days} 天）` }}
            </option>
            <option v-if="!loadingSymbols && symbols.length === 0" value="">没有标的</option>
          </select>
          <p v-if="symbolErr" class="mt-1 text-note text-red-700">{{ symbolErr }}</p>
          <p v-else-if="!loadingSymbols && usable.length === 0" class="mt-1 text-note text-red-700">
            没有任何标的带 K 线。先跑抓取程序，data/_symbols.json 和 CSV 都齐了才能建账户。
          </p>
          <p v-else class="mt-1 text-note text-faint">
            这个账户只能交易它，交易页不会提供切换标的的入口。
            <span v-if="chosenSymbol?.firstDay" class="num">
              可用区间 {{ chosenSymbol.firstDay }} ~ {{ chosenSymbol.lastDay }}
            </span>
          </p>
        </div>

        <div>
          <label :class="labelCls" for="year">起点年份（可选）</label>
          <select
            id="year"
            v-model="yearPick"
            :class="inputCls"
            :disabled="!yearOptions.length"
          >
            <option value="">任意年份（完全随机）</option>
            <option v-for="y in yearOptions" :key="y" :value="String(y)">{{ y }} 年</option>
          </select>
          <p class="mt-1 text-note text-faint">
            {{
              yearOptions.length
                ? '只在选定的这一年里随机抽一天开盘；选「任意年份」就跨年随机。'
                : '这个标的还没有 K 线，先跑抓取程序。'
            }}
          </p>
        </div>

        <div>
          <label :class="labelCls" for="name">账户名</label>
          <div class="flex gap-2">
            <input id="name" v-model="name" :class="inputCls" placeholder="例如 LucidFlex 50K 第一次" />
            <button
              type="button"
              class="shrink-0 rounded-md border border-line px-2 text-faint transition-colors hover:border-accent/60 hover:text-accent disabled:opacity-50"
              title="随机生成一个名字"
              :disabled="rolling"
              @click="roll"
            >
              <Icon name="dice" :size="16" />
            </button>
          </div>
        </div>

        <p v-if="local || props.error" class="rounded border border-red-200 bg-red-50 px-2 py-1.5 text-note text-red-700">
          {{ local || props.error }}
        </p>
      </div>

      <div class="mt-5 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md border border-line px-3 py-1.5 text-body text-dim hover:border-line-strong"
          @click="emit('close')"
        >
          取消
        </button>
        <button
          type="button"
          class="rounded-md bg-accent px-3 py-1.5 text-body font-medium text-white hover:bg-accent-ink"
          @click="submit"
        >
          开账户
        </button>
      </div>
    </div>
  </div>
</template>
