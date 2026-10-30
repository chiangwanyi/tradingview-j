<script setup lang="ts">
import { reactive, ref, watch } from 'vue'

import Icon from './Icon.vue'
import { validate } from '@/data/rules'
import type { DrawdownType, Stage, Template } from '@/data/rules'

const props = defineProps<{
  /** 被编辑的原模板；新建时传 blank() 造出来的空壳。 */
  draft: Template
  /** 阶段在链上，建出来之后不许改 —— 和 PropFirmTemplate.redefine 一致。 */
  stageEditable: boolean
  /** 可选的「下一套」—— 不能指向自己。 */
  others: Template[]
  /** 后端拒绝的原因：前端校验过了也可能被拒（同一条规则两个落点，必然对不齐）。 */
  error: string
}>()
const emit = defineEmits<{ save: [Template]; close: [] }>()

const inputCls =
  'w-full rounded-md border border-line bg-panel px-2.5 py-1.5 text-[13px] text-ink outline-none transition-colors focus:border-accent/60 focus:ring-2 focus:ring-accent/15 disabled:bg-elev disabled:text-faint'
const labelCls = 'mb-1 block text-[11px] tracking-wider text-faint uppercase'

type Form = {
  name: string
  description: string
  stage: Stage
  nextTemplateId: string
  accountSize: string
  profitTarget: string
  maxDrawdown: string
  drawdownType: DrawdownType
  trailingLockLevel: string
  consistency: string
  minProfitDays: string
  profitDayThreshold: string
  tiers: Array<{ fromProfit: string; maxContracts: string }>
}

const s = (n: number | null) => (n == null ? '' : String(n))

const form = reactive<Form>(blankForm(props.draft))
const errors = ref<string[]>([])

function blankForm(t: Template): Form {
  const r = t.rules
  return {
    name: t.name,
    description: t.description,
    stage: t.stage,
    nextTemplateId: t.nextTemplateId ?? '',
    accountSize: s(r.accountSize),
    profitTarget: s(r.profitTarget),
    maxDrawdown: s(r.maxDrawdown),
    drawdownType: r.drawdownType,
    trailingLockLevel: s(r.trailingLockLevel),
    consistency: r.consistencyRatio == null ? '' : String(r.consistencyRatio * 100),
    minProfitDays: s(r.minProfitDays),
    profitDayThreshold: s(r.profitDayThreshold),
    tiers: r.positionTiers.map((x) => ({
      fromProfit: String(x.fromProfit),
      maxContracts: String(x.maxContracts),
    })),
  }
}

// 换了一个模板要重置表单，否则编辑第二张卡会看到上一张的残留
watch(
  () => props.draft,
  (t) => {
    Object.assign(form, blankForm(t))
    errors.value = []
  },
)

/** 空串和 NaN 都当「没填」—— 渲染成「—」，也让阶段校验去决定能不能存。 */
const num = (v: string): number | null => {
  const t = v.trim()
  if (t === '') return null
  const n = Number(t)
  return Number.isFinite(n) ? n : null
}

function submit(): void {
  const consistency = num(form.consistency)
  const draft: Template = {
    ...props.draft,
    name: form.name,
    description: form.description,
    stage: form.stage,
    nextTemplateId: form.nextTemplateId || null,
    rules: {
      accountSize: num(form.accountSize),
      profitTarget: num(form.profitTarget),
      maxDrawdown: num(form.maxDrawdown),
      drawdownType: form.drawdownType,
      trailingLockLevel: num(form.trailingLockLevel),
      consistencyRatio: consistency == null ? null : consistency / 100,
      minProfitDays: num(form.minProfitDays),
      profitDayThreshold: num(form.profitDayThreshold),
      positionTiers: form.tiers
        .filter((x) => x.maxContracts.trim() !== '')
        .map((x) => ({
          fromProfit: num(x.fromProfit) ?? 0,
          maxContracts: Number(num(x.maxContracts) ?? 0),
        })),
    },
  }

  const found = validate(draft)
  const badTier = draft.rules.positionTiers.find((x) => x.maxContracts <= 0)
  if (badTier) found.push('手数上限必须是正整数')
  errors.value = found
  if (found.length === 0) emit('save', draft)
}
</script>

<template>
  <div class="fixed inset-0 z-20 grid place-items-center bg-ink/25 p-6" @click.self="emit('close')">
    <form
      class="max-h-[86vh] w-[720px] max-w-full overflow-y-auto rounded-xl border border-line bg-panel p-6 shadow-xl"
      @submit.prevent="submit"
    >
      <div class="mb-4 flex items-start justify-between">
        <div>
          <h2 class="text-[16px] font-semibold text-ink">
            {{ stageEditable ? '新建模板' : '编辑规则' }}
          </h2>
          <p class="mt-1 text-[12px] text-faint">
            一个模板只描述<strong class="font-medium text-dim">一个阶段</strong>；一套考核是几条模板串成的链。
          </p>
        </div>
        <button type="button" class="text-faint hover:text-ink" title="关闭" @click="emit('close')">
          <Icon name="close" :size="18" />
        </button>
      </div>

      <ul
        v-if="errors.length || error"
        class="mb-4 space-y-1 rounded-md border border-red-200 bg-red-50 px-3 py-2 text-[12px] text-red-700"
      >
        <li v-for="e in errors" :key="e">· {{ e }}</li>
        <li v-if="error">· {{ error }}</li>
      </ul>

      <div class="grid grid-cols-2 gap-4">
        <label class="block">
          <span :class="labelCls">模板名</span>
          <input v-model="form.name" :class="inputCls" placeholder="LucidFlex 50K EVAL" />
        </label>
        <label class="block">
          <span :class="labelCls">说明</span>
          <input v-model="form.description" :class="inputCls" placeholder="可留空" />
        </label>

        <label class="block">
          <span :class="labelCls">阶段</span>
          <select v-model="form.stage" :class="inputCls" :disabled="!stageEditable">
            <option value="EVAL">EVAL · 考核</option>
            <option value="FUNDED">FUNDED · 出金号</option>
            <option value="PAYOUT">PAYOUT · 出金</option>
          </select>
          <span v-if="!stageEditable" class="mt-1 block text-[11px] text-faint">
            阶段在链上，建出来之后不能改；要换阶段请新建一条。
          </span>
        </label>
        <label class="block">
          <span :class="labelCls">达标后接哪套</span>
          <select v-model="form.nextTemplateId" :class="inputCls">
            <option value="">终点（不再升）</option>
            <option v-for="o in others" :key="o.id" :value="o.id">{{ o.name }}</option>
          </select>
        </label>

        <label class="block">
          <span :class="labelCls">账户规模 (USD)</span>
          <input v-model="form.accountSize" :class="inputCls" inputmode="decimal" placeholder="50000" />
        </label>
        <label class="block">
          <span :class="labelCls">盈利目标 (USD)</span>
          <input
            v-model="form.profitTarget"
            :class="inputCls"
            inputmode="decimal"
            placeholder="FUNDED 留空"
          />
        </label>

        <label class="block">
          <span :class="labelCls">最大回撤 (USD)</span>
          <input v-model="form.maxDrawdown" :class="inputCls" inputmode="decimal" placeholder="2000" />
        </label>
        <label class="block">
          <span :class="labelCls">回撤口径</span>
          <select v-model="form.drawdownType" :class="inputCls">
            <option value="STATIC">STATIC · 静态</option>
            <option value="TRAILING_INTRADAY">TRAILING_INTRADAY · 跟踪日内</option>
            <option value="TRAILING_EOD">TRAILING_EOD · 跟踪日终</option>
          </select>
        </label>

        <label class="block">
          <span :class="labelCls">亏损线冻结水位 (USD)</span>
          <input
            v-model="form.trailingLockLevel"
            :class="inputCls"
            inputmode="decimal"
            placeholder="52100"
          />
        </label>
        <label class="block">
          <span :class="labelCls">一致性上限 (%)</span>
          <input v-model="form.consistency" :class="inputCls" inputmode="decimal" placeholder="40" />
        </label>

        <label class="block">
          <span :class="labelCls">需要几个盈利日</span>
          <input v-model="form.minProfitDays" :class="inputCls" inputmode="numeric" placeholder="5" />
        </label>
        <label class="block">
          <span :class="labelCls">盈利日门槛 (USD)</span>
          <input
            v-model="form.profitDayThreshold"
            :class="inputCls"
            inputmode="decimal"
            placeholder="150"
          />
        </label>
      </div>

      <div class="mt-5">
        <div class="mb-1.5 flex items-center justify-between">
          <span class="text-[11px] tracking-wider text-faint uppercase"
            >手数分档（净盈利起点 → 手数上限）</span
          >
          <button
            type="button"
            class="text-[12px] text-accent-ink hover:underline"
            @click="form.tiers.push({ fromProfit: '0', maxContracts: '' })"
          >
            + 加一档
          </button>
        </div>
        <p v-if="!form.tiers.length" class="text-[12px] text-faint">空 = 不限手数。</p>
        <div v-for="(tier, i) in form.tiers" :key="i" class="mb-1.5 flex items-center gap-2">
          <input v-model="tier.fromProfit" :class="inputCls" inputmode="decimal" placeholder="0" />
          <span class="text-faint">→</span>
          <input v-model="tier.maxContracts" :class="inputCls" inputmode="numeric" placeholder="40" />
          <button
            type="button"
            class="shrink-0 text-faint hover:text-red-600"
            title="删掉这一档"
            @click="form.tiers.splice(i, 1)"
          >
            <Icon name="trash" :size="16" />
          </button>
        </div>
      </div>

      <div class="mt-6 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md border border-line px-3.5 py-2 text-[13px] text-dim hover:bg-elev"
          @click="emit('close')"
        >
          取消
        </button>
        <button
          type="submit"
          class="rounded-md bg-accent px-3.5 py-2 text-[13px] font-medium text-white hover:brightness-95"
        >
          保存
        </button>
      </div>
    </form>
  </div>
</template>
