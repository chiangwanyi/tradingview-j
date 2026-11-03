<script setup lang="ts">
import { computed, nextTick, ref } from 'vue'
import { RouterView, useRoute, useRouter } from 'vue-router'

import type { ComponentPublicInstance } from 'vue'

import AppSidebar from '@/components/AppSidebar.vue'

/**
 * 账户总览的外壳：左菜单 + 右内容，右边显示什么由**子路由**决定。
 *
 * 这里不再有 `active` / `detailId` 这类状态：页面就是 URL，高亮是从当前路由
 * **反推**出来的（路由表见 `router/index.ts` 开头）。好处是刷新、直接粘地址、
 * 后退键三件事一次全对；代价是「切页」必须真的走一次导航，而不是改个 ref。
 *
 * 交易页是**另一个顶层路由**（`/trade/:id`），不在这层外壳里 —— 它由账户卡片上的
 * 【继续交易】开成一个新标签页，和总览互不影响。
 */

/** 菜单 key → 路由名。菜单文案在 `AppSidebar` 里，这里只管「点哪一项去哪」。 */
const TAB_ROUTE: Record<string, string> = {
  accounts: 'accounts',
  templates: 'templates',
  audit: 'audit',
  records: 'records',
  bot: 'bot',
  profile: 'profile',
}

/** 路由名 → 菜单 key。账户详情也算在「账户总览」这一项下面，否则点进详情左菜单就全灰了。 */
const ROUTE_TAB: Record<string, string> = {
  accounts: 'accounts',
  'account-detail': 'accounts',
  templates: 'templates',
  audit: 'audit',
  records: 'records',
  bot: 'bot',
  profile: 'profile',
}

const route = useRoute()
const router = useRouter()

const active = computed(() => ROUTE_TAB[String(route.name)] ?? 'accounts')

function select(key: string): void {
  const name = TAB_ROUTE[key]
  // 点当前这一项不该再压一条历史，否则后退键会「退到同一页」。
  if (!name || route.name === name) return
  void router.push({ name })
}

/**
 * 当前页面组件实例。只为一件事存在：左边栏那个「+」要按页面分流 ——
 * 在模板页建模板，其余一律建账户；而「怎么开新建弹窗」只有页面自己知道
 * （各页自己的 `openNew()`，靠 `defineExpose` 露出来）。
 */
const page = ref<(ComponentPublicInstance & { openNew?: () => void }) | null>(null)

async function add(): Promise<void> {
  if (active.value === 'templates') {
    page.value?.openNew?.()
    return
  }
  // 从详情页（或别的页）点「+」要先回账户列表，否则弹窗开在上一页下面看不见
  if (route.name !== 'accounts') {
    await router.push({ name: 'accounts' })
    await nextTick() // 等新页面挂上、`page` 指向它之后才调得到 openNew
  }
  page.value?.openNew?.()
}
</script>

<template>
  <div class="flex h-full min-h-0">
    <AppSidebar :active="active" @select="select" @add="add" />
    <!--
      一个 `<RouterView>` 顶掉原来的五段 v-if。
      `:key="route.path"` 是为了 `/accounts/a` → `/accounts/b` 时**真的重新挂载**：
      详情页在 `onMounted` 里自己拉数据，不重挂就会拿着 a 的余额去画 b 的页面。
    -->
    <RouterView v-slot="{ Component }">
      <component :is="Component" :key="route.path" ref="page" />
    </RouterView>
  </div>
</template>
