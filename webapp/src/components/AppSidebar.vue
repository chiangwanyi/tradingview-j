<script setup lang="ts">
import Icon from './Icon.vue'

/** 7 个导航项，和参考图同样多条数；前两个接了页面，其余是占位。 */
const NAV = [
  { key: 'accounts', label: '账户总览', icon: 'chart' },
  { key: 'templates', label: '账户模板', icon: 'grid' },
  { key: 'leaderboard', label: '排行榜', icon: 'trophy' },
  { key: 'payouts', label: '出金', icon: 'dollar' },
  { key: 'certificates', label: '证书', icon: 'award' },
  { key: 'platforms', label: '平台与附加项', icon: 'pass' },
  { key: 'profile', label: '我的资料', icon: 'user' },
] as const

defineProps<{ active: string }>()
const emit = defineEmits<{ select: [key: string]; add: [] }>()
</script>

<template>
  <aside class="flex w-[212px] shrink-0 flex-col border-r border-line bg-panel">
    <!-- 标志 -->
    <div class="flex items-center gap-2.5 px-5 pt-6 pb-6">
      <span class="grid size-9 place-items-center rounded-full bg-ink">
        <span class="size-4 rounded-full border-[1.5px] border-white/85" />
      </span>
      <span class="leading-none">
        <span class="block text-[15px] font-bold tracking-[0.14em] text-ink">LUCID</span>
        <span class="mt-1 block text-[9px] tracking-[0.34em] text-faint">TRADING</span>
      </span>
    </div>

    <nav class="space-y-0.5 px-3">
      <button
        v-for="item in NAV"
        :key="item.key"
        type="button"
        class="flex w-full items-center gap-2.5 rounded-md border px-2.5 py-2 text-left text-[13px] transition-colors"
        :class="
          item.key === active
            ? 'border-accent/25 bg-accent-soft font-medium text-accent-ink'
            : 'border-transparent text-dim hover:bg-elev'
        "
        @click="emit('select', item.key)"
      >
        <Icon :name="item.icon" :size="15" />
        {{ item.label }}
      </button>
    </nav>

    <div class="px-3 pt-3">
      <button
        type="button"
        class="flex w-full items-center justify-center gap-2 rounded-md border border-accent/40 bg-accent-soft/70 py-2 text-[13px] font-medium text-accent-ink transition-colors hover:bg-accent-soft"
        @click="emit('add')"
      >
        <Icon name="plus" :size="15" />
        {{ active === 'templates' ? '新建模板' : '新建账户' }}
      </button>
    </div>

    <!-- 底部只留「退出」；Discord / 支持 / FAQ / 背景 / 收起 已按需求删掉 -->
    <div class="mt-auto px-3 pb-4">
      <div
        aria-disabled="true"
        title="未接入"
        class="flex cursor-default items-center gap-2 border-t border-line pt-3 text-[13px] text-dim"
      >
        <Icon name="logout" :size="15" />退出
      </div>
    </div>
  </aside>
</template>
