<script setup lang="ts">
import { ref, watch } from 'vue'

import { useReplayStore } from '@/stores/replay'

/**
 * 右下角的错误提示（交易记录页专用）。
 *
 * 为什么不用 alert / 全局弹窗：这一页的错误几乎都是「这件事没做成」——
 * 标签没存住、记录删不掉。用户需要知道，但不该被打断（他可能正在连着打几个标签）。
 * 所以是一条会自己消失的条，位置固定在右下角，不挡日历和列表。
 *
 * 消息来源只有一处：`store.lastError`（服务端回的 error）。`errorKey` 每来一条
 * 新的就 +1 —— 同一个错误连发两次也要重新计时，否则第二次会看起来「没反应」。
 */
const store = useReplayStore()

const text = ref('')
let timer = 0

watch(
  () => store.errorKey,
  () => {
    text.value = store.lastError
    if (timer) window.clearTimeout(timer)
    timer = window.setTimeout(() => {
      text.value = ''
    }, 8000)
  },
)

function close(): void {
  text.value = ''
  if (timer) window.clearTimeout(timer)
}
</script>

<template>
  <Transition
    enter-active-class="transition duration-200"
    enter-from-class="translate-y-2 opacity-0"
    leave-active-class="transition duration-150"
    leave-to-class="opacity-0"
  >
    <div
      v-if="text"
      class="fixed right-4 bottom-4 z-50 flex max-w-[min(28rem,90vw)] items-start gap-2 rounded-lg border border-down/30 bg-down-soft px-3 py-2 text-[13px] text-down shadow-lg"
      role="status"
    >
      <span class="mt-px shrink-0 font-semibold">没做成</span>
      <span class="min-w-0 break-words">{{ text }}</span>
      <button
        type="button"
        class="ml-1 shrink-0 text-down/60 transition hover:text-down"
        title="关掉"
        @click="close"
      >
        ×
      </button>
    </div>
  </Transition>
</template>
