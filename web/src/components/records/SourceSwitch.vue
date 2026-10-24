<script setup lang="ts">
import { computed } from 'vue'

import { SOURCE_BOT, SOURCE_HUMAN } from '@/lib/records'
import type { SourceOption } from '@/lib/records'

/**
 * 操作来源切换（v0.21.0）：全部 / 手动 / 机器人，机器人再细分到策略。
 *
 * 为什么是「两级」而不是一个长下拉：来源是一个**二分**（人还是机器），
 * 「哪个策略」是机器内部的细分。扁平成五项的话，「手动」和「Caveman」并列，
 * 看起来像两个同级的策略 —— 而这正是最容易被误读的地方。
 *
 * 选项**从数据现算**（见 lib/records.ts 的 sourceOptions）：没有记录的策略不占位，
 * 将来新注册一个策略会自动出现，这里不用改。
 */
const props = defineProps<{
  modelValue: string
  options: { all: number; human: number; bot: number; bots: SourceOption[] }
}>()

const emit = defineEmits<{ 'update:modelValue': [string] }>()

/** 当前是不是「机器人」这一档（含细分） */
const botActive = computed(() => props.modelValue === SOURCE_BOT || props.modelValue.startsWith('bot:'))

const seg =
  'rounded px-2.5 py-1 text-[13px] transition disabled:cursor-not-allowed disabled:opacity-40'
const segOn = 'bg-ink text-white shadow-sm'
const segOff = 'text-dim hover:bg-elev hover:text-ink'

function pick(key: string): void {
  // 再点一次当前项 = 取消（回到全部）。少一个「全部」按钮要记，也少一次误操作。
  emit('update:modelValue', props.modelValue === key ? '' : key)
}
</script>

<template>
  <div class="flex flex-wrap items-center gap-2">
    <div class="flex items-center gap-0.5 rounded-md border border-line bg-elev/60 p-0.5">
      <button
        type="button"
        :class="[seg, modelValue === '' ? segOn : segOff]"
        title="不限来源"
        @click="emit('update:modelValue', '')"
      >
        全部
        <span class="tnum ml-1 text-[11px] opacity-70">{{ options.all }}</span>
      </button>
      <button
        type="button"
        :class="[seg, modelValue === SOURCE_HUMAN ? segOn : segOff]"
        :disabled="options.human === 0"
        title="只看真人手动下的单"
        @click="pick(SOURCE_HUMAN)"
      >
        手动
        <span class="tnum ml-1 text-[11px] opacity-70">{{ options.human }}</span>
      </button>
      <button
        type="button"
        :class="[seg, botActive ? segOn : segOff]"
        :disabled="options.bot === 0"
        title="只看机器人下的单"
        @click="pick(SOURCE_BOT)"
      >
        机器人
        <span class="tnum ml-1 text-[11px] opacity-70">{{ options.bot }}</span>
      </button>
    </div>

    <!-- 机器人内部再分策略：只有站在「机器人」这一档时才出现，别的时候是噪音 -->
    <div
      v-if="botActive && options.bots.length > 1"
      class="flex flex-wrap items-center gap-1 text-[12px]"
    >
      <span class="text-faint">策略</span>
      <button
        type="button"
        class="rounded-full border px-2 py-0.5 transition"
        :class="
          modelValue === SOURCE_BOT
            ? 'border-ink bg-ink text-white'
            : 'border-line text-dim hover:border-accent hover:text-accent'
        "
        @click="emit('update:modelValue', SOURCE_BOT)"
      >
        全部策略
      </button>
      <button
        v-for="b in options.bots"
        :key="b.key"
        type="button"
        class="rounded-full border px-2 py-0.5 transition"
        :class="
          modelValue === b.key
            ? 'border-ink bg-ink text-white'
            : 'border-line text-dim hover:border-accent hover:text-accent'
        "
        @click="pick(b.key)"
      >
        {{ b.label }}
        <span class="tnum ml-1 opacity-70">{{ b.count }}</span>
      </button>
    </div>
  </div>
</template>
