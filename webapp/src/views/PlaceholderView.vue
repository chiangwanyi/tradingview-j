<script setup lang="ts">
defineProps<{ title: string }>()
</script>

<template>
  <section class="flex min-w-0 flex-1 flex-col items-center justify-center gap-2">
    <h1 class="text-[20px] font-semibold text-ink">{{ title }}</h1>
    <p class="text-[13px] text-faint">这里还没做。右侧这块区域就是留给它的。</p>
  </section>
</template>
