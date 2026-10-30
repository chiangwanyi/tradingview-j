<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import Icon from './Icon.vue'
import { stageLabel, usd } from '@/data/rules'
import type { Template } from '@/data/rules'

/**
 * 开账户：选一套模板 + 起个名字。
 *
 * 只有两个输入 —— 起点 K 线由后端随机抽（当天 09:00 及之后第一根），余额和规则都是
 * 模板的副本。这里不提供「自定义余额」之类的入口：余额只能从成交重算出来后同步。
 */
const props = defineProps<{ templates: Template[]; error: string }>()
const emit = defineEmits<{ save: [{ templateId: string; name: string }]; close: [] }>()

const templateId = ref(props.templates[0]?.id ?? '')
const name = ref('')
const local = ref('')

const chosen = computed(() => props.templates.find((t) => t.id === templateId.value) ?? null)

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
  if (!name.value.trim()) {
    local.value = '账户得有名字'
    return
  }
  local.value = ''
  emit('save', { templateId: templateId.value, name: name.value.trim() })
}

const inputCls =
  'w-full rounded-md border border-line bg-panel px-2.5 py-1.5 text-[13px] text-ink outline-none transition-colors focus:border-accent/60 focus:ring-2 focus:ring-accent/15'
const labelCls = 'mb-1 block text-[11px] tracking-wider text-faint uppercase'
</script>

<template>
  <div class="fixed inset-0 z-20 flex items-center justify-center bg-ink/25 p-6" @click.self="emit('close')">
    <div class="w-full max-w-md rounded-xl border border-line bg-panel p-5 shadow-lg">
      <div class="flex items-start justify-between">
        <div>
          <h2 class="text-[15px] font-semibold text-ink">新建考核账户</h2>
          <p class="mt-0.5 text-[12px] text-faint">规则在这一刻从模板复制进账户，之后改模板不影响它。</p>
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
          <p v-if="chosen" class="mt-1 text-[11px] text-faint tnum">
            规模 {{ usd(chosen.rules.accountSize) }} · 目标 {{ usd(chosen.rules.profitTarget) }} · 回撤
            {{ usd(chosen.rules.maxDrawdown) }}
            <span v-if="chosen.nextTemplateId == null"> · 这条链到此为止</span>
          </p>
          <p v-else-if="!props.templates.length" class="mt-1 text-[11px] text-red-700">
            还没有模板。先去「账户模板」建一套 EVAL，账户才有规则可签。
          </p>
        </div>

        <div>
          <label :class="labelCls" for="name">账户名</label>
          <input id="name" v-model="name" :class="inputCls" placeholder="例如 LucidFlex 50K 第一次" />
        </div>

        <p v-if="local || props.error" class="rounded border border-red-200 bg-red-50 px-2 py-1.5 text-[12px] text-red-700">
          {{ local || props.error }}
        </p>
      </div>

      <div class="mt-5 flex justify-end gap-2">
        <button
          type="button"
          class="rounded-md border border-line px-3 py-1.5 text-[13px] text-dim hover:border-line-strong"
          @click="emit('close')"
        >
          取消
        </button>
        <button
          type="button"
          class="rounded-md bg-accent px-3 py-1.5 text-[13px] font-medium text-white hover:bg-accent-ink"
          @click="submit"
        >
          开账户
        </button>
      </div>
    </div>
  </div>
</template>
