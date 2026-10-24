<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'

import ChartPanel from '@/components/ChartPanel.vue'
import ChartToolbar from '@/components/ChartToolbar.vue'
import InstancesMenu from '@/components/InstancesMenu.vue'
import TradeDock from '@/components/TradeDock.vue'
import TradeFloat from '@/components/TradeFloat.vue'
import VoiceFloat from '@/components/VoiceFloat.vue'
import VoiceTape from '@/components/VoiceTape.vue'
import { usePlaybackStore } from '@/stores/playback'
import { useReplayStore } from '@/stores/replay'

/**
 * 复盘主界面：一条顶栏 + 画布占满 + 底部 Tab（模拟交易 / 操作记录 / 交易记录）+ 浮在页面上的下单窗。
 *
 * 布局是 v1.1 调整过的：左侧那一栏「标的与实例」搬进了右上角的折叠面板，
 * 右侧的交易栏搬到了底部 Tab —— 两处都只为了同一个目标：**把空间让给画布**。
 *
 * v0.8.0 又把「下单」这一列从底部面板里提出来，做成一枚可拖动、可折叠成小图标的浮窗：
 * 步进、看盘、挂单是交替进行的，下单框必须随手能点到，又不能再吃掉画布的高度。
 *
 * v0.18.0：交易记录改成**自动**的（开仓开始、平仓结束），所以原来贴在画布左上角的
 * 录制浮窗整个删掉了 —— 界面上不再有「正在录制」这个概念；回放仍然在这个画布上演。
 *
 * 整个应用只有这一页。实例是服务端概念，这里只负责「进入/退出/观看」，
 * 关掉标签页不会删除实例（下次连上还在列表里）。
 */

const store = useReplayStore()
const pb = usePlaybackStore()

const statusText = computed(() => {
  switch (store.status) {
    case 'open':
      return '已连接'
    case 'connecting':
      return '连接中…'
    default:
      return '已断开（正在自动重连）'
  }
})

const statusColor = computed(() => {
  switch (store.status) {
    case 'open':
      return 'bg-emerald-500'
    case 'connecting':
      return 'bg-amber-400'
    default:
      return 'bg-red-500'
  }
})

const cursorText = computed(() => {
  const inst = store.instance
  if (!inst) return ''
  return `第 ${inst.cursor + 1} 根 / 已加载 ${inst.bars.length} 根`
})

const themeLabel = computed(() => (store.theme === 'dark' ? '浅色' : '深色'))

function typable(el: Element | null): boolean {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || (el as HTMLElement).isContentEditable === true
}

/**
 * 回放时的空格 = 「当前这一步的语音逐条播放」。
 *
 * 这里的空格和语音浮窗里的空格（按一下开始录、再按一下停）不会打架：
 * 回放是只读的，麦克风在回放时是灰的。这里用捕获阶段 + preventDefault，免得空格
 * 顺手把焦点上的按钮再按一下、或者把页面滚一格。
 */
function onSpace(ev: KeyboardEvent): void {
  if (ev.code !== 'Space' || ev.repeat) return
  if (!store.playbackActive) return
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return
  if (typable(document.activeElement)) return
  if (pb.spacePlay()) {
    ev.preventDefault()
    ev.stopPropagation()
  }
}

onMounted(() => {
  store.connect()
  window.addEventListener('keydown', onSpace, true)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onSpace, true)
})
</script>

<template>
  <div class="flex h-screen flex-col bg-surface text-ink">
    <header class="flex h-11 shrink-0 items-center gap-3 border-b border-line bg-panel px-3">
      <span class="text-[15px] font-semibold tracking-wide">看盘 · 复盘</span>
      <span class="text-[11px] text-faint">v{{ store.me?.version ?? '' }}</span>

      <span class="flex items-center gap-1.5 text-[12px] text-dim">
        <span class="h-1.5 w-1.5 rounded-full" :class="statusColor" />
        {{ statusText }}
      </span>

      <span v-if="store.me" class="rounded bg-elev px-1.5 py-0.5 text-[11px] text-dim">
        {{ store.me.name }}
        <span v-if="store.isOperator" class="text-emerald-600 dark:text-emerald-400">（操作者）</span>
        <span v-else-if="store.instance && !store.playbackActive" class="text-faint">（观众）</span>
      </span>

      <!-- 回放中：顶栏直接写清楚「现在看的不是实时实例」 -->
      <div v-if="store.playbackActive" class="flex min-w-0 items-center gap-2 text-[12px]">
        <span class="h-4 w-px bg-line" />
        <span class="rounded bg-red-500/15 px-1.5 text-[11px] font-medium text-red-600 dark:text-red-400">回放中</span>
        <span class="min-w-0 truncate font-medium text-ink">{{ pb.recording?.name }}</span>
        <span class="tnum shrink-0 text-[11px] text-faint">第 {{ pb.step }}/{{ pb.total }} 步</span>
      </div>
      <div v-else-if="store.instance" class="flex min-w-0 items-center gap-2 text-[12px] text-dim">
        <span class="h-4 w-px bg-line" />
        <span class="truncate font-medium text-ink">{{ store.instance.symbol }}</span>
        <span class="tnum text-[11px] text-faint">{{ store.instance.anchor }}</span>
        <span class="text-[11px] text-faint">{{ cursorText }}</span>
        <span class="tnum text-[11px] text-faint">tick {{ store.tick }}</span>
        <span v-if="store.instance.bot" class="rounded bg-sky-600/20 px-1.5 text-[11px] text-sky-600 dark:text-sky-300">
          机器人演示中
        </span>
      </div>
      <div v-else class="text-[12px] text-faint">没有进入实例</div>

      <div class="ml-auto flex items-center gap-2">
        <button
          type="button"
          class="rounded border border-line px-2 py-1 text-[12px] text-dim hover:border-accent hover:text-accent"
          :title="`切换到${themeLabel}模式`"
          @click="store.toggleTheme()"
        >
          {{ themeLabel }}
        </button>
        <InstancesMenu />
        <button
          v-if="store.playbackActive"
          type="button"
          class="rounded border border-red-400 px-2 py-1 text-[12px] text-red-600 hover:bg-red-500/10 dark:text-red-400"
          @click="pb.close()"
        >
          退出回放
        </button>
        <button
          v-else-if="store.instance"
          type="button"
          class="rounded border border-line px-2 py-1 text-[12px] text-dim hover:border-accent hover:text-accent"
          @click="store.leaveInstance()"
        >
          离开
        </button>
      </div>
    </header>

    <main class="relative flex min-h-0 flex-1 flex-col">
      <ChartToolbar />
      <div class="relative min-h-0 flex-1">
        <ChartPanel />

        <!--
          语音讲解浮窗：工具条上点【🎙 录音】才会出现（`store.voiceOpen`）。
          它和交易记录完全无关 —— 空仓也能讲两句，持仓期间讲的会由后端顺手记进
          那笔交易记录（这里不需要做任何事）。
        -->
        <VoiceFloat />

        <!-- 回放进度：画布顶部居中一条窄条，纯显示、不接指针，不抢画布的手势 -->
        <div
          v-if="store.playbackActive"
          class="pointer-events-none absolute top-2 left-1/2 z-30 -translate-x-1/2 rounded-full border border-line bg-panel px-3 py-1 text-[11px] text-dim shadow"
        >
          <span class="mr-2 font-medium text-red-600 dark:text-red-400">回放</span>
          <span class="tnum text-ink">第 {{ pb.step }}/{{ pb.total }} 步</span>
          <span class="tnum ml-2 text-faint">{{ pb.currentStep?.time }}</span>
          <span v-if="pb.audioPlaying" class="ml-2 text-amber-600 dark:text-amber-400">🔊 {{ pb.audioLabel }}</span>
          <span v-else-if="pb.clips.length > 0" class="ml-2 text-faint">本步 {{ pb.clips.length }} 条语音 · 空格逐条</span>
          <span class="ml-2 text-faint">{{ pb.playing ? `${pb.speed}x 播放中` : '已暂停' }}</span>
        </div>
      </div>

      <!--
        语音录音条：贴在**时间横轴正下方**，和 K 线严格对齐（同一个 chartViewport）。
        它属于这一页的布局：占 40px 的固定高度，始终在（回放时也在，否则进出回放
        画布会突然长高 40px）。
      -->
      <VoiceTape />

      <!--
        提示走「轻提示」而不是错误框：交易记录保存成功、语音挂载成功都不是错误，
        不该要人点一下才能继续看盘。
      -->
      <div
        v-if="store.notice"
        :key="store.noticeKey"
        class="absolute right-2 bottom-2 z-40 max-w-lg rounded border border-emerald-300 bg-emerald-50 px-3 py-2 text-[13px] text-emerald-800 shadow-lg dark:border-emerald-700 dark:bg-emerald-950 dark:text-emerald-100"
      >
        <div class="flex items-start gap-3">
          <span class="flex-1">{{ store.notice }}</span>
          <button type="button" class="text-emerald-600 hover:opacity-70" @click="store.clearNotice()">关闭</button>
        </div>
      </div>

      <!--
        错误提示贴着画布右下角（不是窗口右下角）：底部现在有交易栏，
        fixed 到 bottom-3 会被它整个盖住，报错了也看不见。
      -->
      <div
        v-if="store.lastError"
        :key="store.errorKey"
        class="absolute right-2 bottom-2 z-50 max-w-md rounded border border-red-300 bg-red-50/95 px-3 py-2 text-[13px] text-red-800 shadow-lg dark:border-red-700 dark:bg-red-950/95 dark:text-red-100"
      >
        <div class="flex items-start gap-3">
          <span class="flex-1">{{ store.lastError }}</span>
          <button type="button" class="text-red-500 hover:opacity-70" @click="store.clearError()">关闭</button>
        </div>
      </div>
    </main>

    <TradeDock />

    <!--
      下单浮窗：`position: fixed`，挂在这里只是因为它属于这一页；
      它自己管位置（拖到哪儿记在哪儿）、自己管折叠，不参与上面的 flex 布局。
      回放时藏起来：那个盘面是历史，下单没有意义。
    -->
    <TradeFloat v-if="!store.playbackActive" />
  </div>
</template>
