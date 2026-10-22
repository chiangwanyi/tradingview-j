<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'

import { useReplayStore } from '@/stores/replay'

/**
 * 录制状态浮窗（v0.11.0；v0.16.0 只留状态，麦克风搬去语音浮窗）。
 *
 * 为什么把录音搬走：语音讲解现在是一级功能（见 VoiceFloat.vue）—— 不录制也能录，
 * 所以麦克风、波形、试听、清空不该只活在「正在录制」的浮窗里。这个窗因此瘦成
 * 纯粹的**录制状态牌**：第几步、第几根、这根上几条语音、总量多少，外加一个
 * 【打开语音】的入口（录制中十有八九要讲两句，不该让人再去工具条找一遍）。
 *
 * 位置自己拖、可以折叠成一枚 44px 的小图标 —— 和下单浮窗 / 语音浮窗同一套交互：
 * 标题栏是把手（`pointerdown` 里 `preventDefault`，不然一按就框选文字）、
 * 位移超过 5px 才算拖动、折叠后整枚图标都是把手（没拖动就是单击展开）、
 * 位置存 localStorage、每次移动/展开/窗口缩放都夹回视口里。
 *
 * 默认落在下单浮窗右边一点（语音浮窗再往右），三个窗首次出现时互不重叠。
 */

const store = useReplayStore()

const POS_KEY = 'tv-record-hud-pos'

const PANEL_W = 236
const ICON = 44
const EST_H = 150
const MARGIN = 8
const DRAG_SLOP = 5

const expanded = ref(true)
const pos = ref(readSavedPos() ?? { x: 300, y: 112 })
const root = ref<HTMLElement | null>(null)

const state = computed(() => store.recording)
const step = computed(() => state.value?.step ?? 0)
const total = computed(() => state.value?.total ?? 0)
/** 本根几条 / 上限：取自**语音**状态（v0.16.0），不是录像自己的计数 —— 见下方注释 */
const barAudios = computed(() => store.voiceClips.length)
const maxAudios = computed(() => store.voiceMax)
const totalAudios = computed(() => store.voice.total)
const startTime = computed(() => state.value?.startTime ?? '')
const current = computed(() => state.value?.current ?? '')

function clampPos(p: { x: number; y: number }): { x: number; y: number } {
  const w = root.value?.offsetWidth || (expanded.value ? PANEL_W : ICON)
  const h = root.value?.offsetHeight || (expanded.value ? EST_H : ICON)
  const maxX = Math.max(MARGIN, window.innerWidth - w - MARGIN)
  const maxY = Math.max(MARGIN, window.innerHeight - h - MARGIN)
  return {
    x: Math.min(Math.max(p.x, MARGIN), maxX),
    y: Math.min(Math.max(p.y, MARGIN), maxY),
  }
}

function readSavedPos(): { x: number; y: number } | null {
  try {
    const raw = localStorage.getItem(POS_KEY)
    if (!raw) return null
    const v = JSON.parse(raw) as { x?: unknown; y?: unknown }
    if (typeof v.x !== 'number' || typeof v.y !== 'number') return null
    if (!Number.isFinite(v.x) || !Number.isFinite(v.y)) return null
    return { x: v.x, y: v.y }
  } catch {
    return null
  }
}

function savePos(): void {
  try {
    localStorage.setItem(POS_KEY, JSON.stringify({ x: Math.round(pos.value.x), y: Math.round(pos.value.y) }))
  } catch {
    // 记不住就算了
  }
}

let dragFrom: { px: number; py: number; x: number; y: number } | null = null
let dragged = false

function beginDrag(ev: PointerEvent): void {
  if (ev.button !== 0) return
  // 关键：不 preventDefault 的话，拖动过程会顺手选中浮层里的文字
  ev.preventDefault()
  ev.stopPropagation()
  dragFrom = { px: ev.clientX, py: ev.clientY, x: pos.value.x, y: pos.value.y }
  dragged = false
  window.addEventListener('pointermove', onDragMove)
  window.addEventListener('pointerup', onDragEnd)
  window.addEventListener('pointercancel', onDragEnd)
}

function onDragMove(ev: PointerEvent): void {
  if (!dragFrom) return
  const dx = ev.clientX - dragFrom.px
  const dy = ev.clientY - dragFrom.py
  if (!dragged && Math.abs(dx) + Math.abs(dy) > DRAG_SLOP) dragged = true
  if (!dragged) return
  pos.value = clampPos({ x: dragFrom.x + dx, y: dragFrom.y + dy })
}

function onDragEnd(): void {
  window.removeEventListener('pointermove', onDragMove)
  window.removeEventListener('pointerup', onDragEnd)
  window.removeEventListener('pointercancel', onDragEnd)
  const moved = dragged
  dragFrom = null
  dragged = false
  if (moved) savePos()
  if (!moved && !expanded.value) setExpanded(true)
}

function setExpanded(v: boolean): void {
  expanded.value = v
  void nextTick(() => {
    pos.value = clampPos(pos.value)
  })
}

function onResize(): void {
  pos.value = clampPos(pos.value)
}

onMounted(() => {
  pos.value = clampPos(pos.value)
  window.addEventListener('resize', onResize)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  window.removeEventListener('pointermove', onDragMove)
  window.removeEventListener('pointerup', onDragEnd)
  window.removeEventListener('pointercancel', onDragEnd)
})

/** 录制中直接打开语音浮窗：这个动作本身就是「我要边录边讲」的下一步。 */
function openVoice(): void {
  if (!store.voiceOpen) store.toggleVoicePanel()
}
</script>

<template>
  <div
    v-if="store.isRecording"
    ref="root"
    class="fixed z-[60] text-[11px] text-dim"
    :style="{
      left: `${pos.x}px`,
      top: `${pos.y}px`,
      width: expanded ? `${PANEL_W}px` : `${ICON}px`,
    }"
    data-testid="record-hud"
    :data-recording-id="state?.id ?? ''"
  >
    <div v-if="expanded" class="overflow-hidden rounded-lg border border-line bg-panel shadow-2xl">
      <div
        class="flex cursor-move items-center gap-1.5 border-b border-line bg-elev px-2 py-1.5 select-none"
        title="按住这里把录制浮窗拖到顺手的位置"
        @pointerdown="beginDrag"
      >
        <span class="inline-block h-2 w-2 shrink-0 animate-pulse rounded-full bg-red-500" />
        <span class="font-medium text-red-600 dark:text-red-400">录制中</span>
        <span class="ml-auto tnum text-ink">第 {{ step }}/{{ total }} 步</span>
        <button
          type="button"
          class="flex h-4 w-4 items-center justify-center rounded text-[13px] leading-none text-faint hover:bg-surface hover:text-ink"
          title="折叠成小图标（仍可拖动）"
          @pointerdown.stop
          @click.stop="setExpanded(false)"
        >
          —
        </button>
      </div>

      <div class="px-2.5 py-1.5">
        <div class="flex items-baseline gap-2">
          <span class="tnum">
            本根语音 <b class="text-ink">{{ barAudios }}</b
            >/{{ maxAudios }}
          </span>
          <span class="tnum text-[10px] text-faint">共 {{ totalAudios }} 条</span>
          <span v-if="current" class="tnum ml-auto text-[10px] text-faint">{{ current }}</span>
        </div>
        <div v-if="startTime" class="mt-0.5 text-[10px] text-faint">起点 {{ startTime }}</div>

        <!--
          录制里**不再有麦克风**：录音是语音浮窗的事（不录制也能录）。这里只给一个入口 ——
          录制中要讲两句是最常见的组合，不该让人再去工具条找一遍。
        -->
        <button
          type="button"
          class="mt-1.5 w-full rounded border border-line px-1.5 py-1 text-[11px] hover:border-accent hover:text-accent"
          :class="store.voiceOpen ? 'border-accent text-accent' : 'text-dim'"
          title="打开语音讲解浮窗（空格开始录音）；录制中录的语音会同时记进这条录像"
          @pointerdown.stop
          @click="openVoice"
        >
          🎙 {{ store.voiceOpen ? '语音浮窗已打开' : '打开语音浮窗（空格录音）' }}
        </button>

        <div class="mt-1 text-[10px] text-faint">机器人操作也会被一起录下（验证两端等价）</div>
      </div>
    </div>

    <!-- 折叠态：一枚 44px 的小图标，整枚都是把手 -->
    <button
      v-else
      type="button"
      class="flex cursor-move flex-col items-center justify-center rounded-full border border-line bg-panel shadow-xl transition select-none hover:border-accent"
      :style="{ width: `${ICON}px`, height: `${ICON}px` }"
      :title="`录制中 · 第 ${step} 步 · 单击展开，按住可拖到别处`"
      @pointerdown="beginDrag"
    >
      <span class="inline-block h-2.5 w-2.5 animate-pulse rounded-full bg-red-500" />
      <span class="tnum mt-1 text-[9px] leading-none text-dim">第{{ step }}步</span>
    </button>
  </div>
</template>
