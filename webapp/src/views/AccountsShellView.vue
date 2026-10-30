<script setup lang="ts">
import { nextTick, ref } from 'vue'

import AppSidebar from '@/components/AppSidebar.vue'
import AccountDetailView from '@/views/AccountDetailView.vue'
import AccountsView from '@/views/AccountsView.vue'
import PlaceholderView from '@/views/PlaceholderView.vue'
import TemplatesView from '@/views/TemplatesView.vue'

/**
 * 账户总览的外壳：左菜单 + 右内容。
 *
 * 路由上它就是 `/`。交易页是**另一个路由**（`/trade/:id`），不是这个外壳里的一层 ——
 * 它由账户卡片上的【继续交易】开成一个新标签页，和总览互不影响。
 * 所以这里不需要「进交易页」的状态，只用 `active` + `detailId` 管自己这一半。
 */

/** 进应用先看账户：模板是配置，账户才是每天要看的东西。 */
const active = ref('accounts')

/** 详情是账户页里的一层（整页替换列表，不弹窗），存 id 不存对象 —— 切走再回来要重新拉最新余额。 */
const detailId = ref<string | null>(null)

function select(key: string): void {
  active.value = key
  detailId.value = null
}

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
  // 从详情页点「+」要先退回列表，否则弹窗开在详情页下面看不见
  if (active.value !== 'accounts' || detailId.value) {
    active.value = 'accounts'
    detailId.value = null
    await nextTick()
  }
  accounts.value?.openNew()
}
</script>

<template>
  <div class="flex h-full min-h-0">
    <AppSidebar :active="active" @select="select" @add="add" />
    <AccountsView v-if="active === 'accounts' && !detailId" ref="accounts" @detail="detailId = $event" />
    <AccountDetailView
      v-else-if="active === 'accounts' && detailId"
      :key="detailId"
      :id="detailId"
      @back="detailId = null"
    />
    <TemplatesView v-else-if="active === 'templates'" ref="templates" />
    <PlaceholderView v-else :title="LABEL[active] ?? active" />
  </div>
</template>
