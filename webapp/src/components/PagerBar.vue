<script setup lang="ts">
import Icon from '@/components/Icon.vue'

/**
 * 分页条。【操作记录】【交易记录】两张表共用。
 *
 * 只有「上一页 / 下一页 + 第几页 / 共几条」：这两张表是用来回翻的，
 * 不需要跳页输入框之类的控件。
 */
const props = defineProps<{ page: number; size: number; total: number }>()
const emit = defineEmits<{ go: [page: number] }>()

const pages = () => Math.max(1, Math.ceil(props.total / props.size))
</script>

<template>
  <div class="flex items-center justify-between gap-3 border-t border-line pt-3 text-[13px] text-dim">
    <span class="tnum">
      共 {{ total }} 条 · 第 {{ Math.min(page + 1, pages()) }} / {{ pages() }} 页
    </span>
    <div class="flex gap-2">
      <button
        type="button"
        class="flex items-center gap-1 rounded-md border border-line px-2.5 py-1.5 transition-colors hover:bg-elev disabled:cursor-default disabled:text-faint disabled:hover:bg-transparent"
        :disabled="page <= 0"
        @click="emit('go', page - 1)"
      >
        <Icon name="left" :size="14" />上一页
      </button>
      <button
        type="button"
        class="flex items-center gap-1 rounded-md border border-line px-2.5 py-1.5 transition-colors hover:bg-elev disabled:cursor-default disabled:text-faint disabled:hover:bg-transparent"
        :disabled="page + 1 >= pages()"
        @click="emit('go', page + 1)"
      >
        下一页<Icon name="right" :size="14" />
      </button>
    </div>
  </div>
</template>
