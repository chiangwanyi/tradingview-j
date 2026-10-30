<script setup lang="ts">
import { nextTick, ref } from 'vue'

import AppSidebar from '@/components/AppSidebar.vue'
import AccountsView from '@/views/AccountsView.vue'
import PlaceholderView from '@/views/PlaceholderView.vue'
import TemplatesView from '@/views/TemplatesView.vue'

/** 进应用先看账户：模板是配置，账户才是每天要看的东西。 */
const active = ref('accounts')

/** 有页面的两项接了 view，其余导航项给个占位。 */
const LABEL: Record<string, string> = {
  leaderboard: '排行榜',
  payouts: '出金',
  certificates: '证书',
  platforms: '平台与附加项',
  profile: '我的资料',
}

const templates = ref<InstanceType<typeof TemplatesView>>()
const accounts = ref<InstanceType<typeof AccountsView>>()

/** 左边栏那个「+」按当前页面分流：在模板页建模板，其余一律建账户。 */
async function add(): Promise<void> {
  if (active.value === 'templates') {
    templates.value?.openNew()
    return
  }
  if (active.value !== 'accounts') {
    active.value = 'accounts'
    await nextTick()
  }
  accounts.value?.openNew()
}
</script>

<template>
  <div class="flex h-full min-h-0">
    <AppSidebar :active="active" @select="active = $event" @add="add" />
    <AccountsView v-if="active === 'accounts'" ref="accounts" />
    <TemplatesView v-else-if="active === 'templates'" ref="templates" />
    <PlaceholderView v-else :title="LABEL[active] ?? active" />
  </div>
</template>
