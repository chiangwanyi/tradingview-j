<script setup lang="ts">
import { nextTick, ref } from 'vue'

import AppSidebar from '@/components/AppSidebar.vue'
import PlaceholderView from '@/views/PlaceholderView.vue'
import TemplatesView from '@/views/TemplatesView.vue'

const active = ref('templates')

/** 只有「账户模板」有页面，其余导航项给个占位。 */
const LABEL: Record<string, string> = {
  accounts: '账户列表',
  leaderboard: '排行榜',
  payouts: '出金',
  certificates: '证书',
  platforms: '平台与附加项',
  profile: '我的资料',
}

const view = ref<InstanceType<typeof TemplatesView>>()

async function add(): Promise<void> {
  if (active.value !== 'templates') {
    active.value = 'templates'
    await nextTick()
  }
  view.value?.openNew()
}
</script>

<template>
  <div class="flex h-full min-h-0">
    <AppSidebar :active="active" @select="active = $event" @add="add" />
    <TemplatesView v-if="active === 'templates'" ref="view" />
    <PlaceholderView v-else :title="LABEL[active] ?? active" />
  </div>
</template>
