<script setup lang="ts">
import { computed, onBeforeUnmount, watch } from 'vue'

import {
  voiceCurrentFile,
  voiceError,
  voicePlayList,
  voicePlaying,
  voiceProgress,
  voiceStop,
} from '@/audio/player'
import AudioWave from '@/components/AudioWave.vue'
import { PRICE_AXIS_WIDTH, clamp, formatAxisTime } from '@/components/chart/scale'
import { chartNav, chartViewport } from '@/components/chart/viewport'
import { useTradeStore } from '@/stores/trade'
import type { AudioClip } from '@/types/protocol'

/**
 * 时间横轴下方的**录音条**：把盘面上所有语音讲解摆到时间轴上听。
 *
 * 为什么贴在横轴底下而不是另起一个列表：「这条语音讲的是哪一根 K 线」是这个功能最要紧的
 * 信息，而只有横轴说得清。整条带子的左右边距和行情区**完全一致**，一个标记就正好落在它
 * 那根 K 线的正下方 —— 缩放、平移都跟着走，因为它和画布读的是同一份 `chartViewport`。
 *
 * 每根 K 线一个标记（同一根最多 9 条，逐条画会重叠成一坨），标记里画**真解码出来的波形**。
 * 点标记 = 按顺序放这根上的全部；右端【▶ 连播】= 把整块盘面按时间过一遍。
 * 语音在视野之外时两端出现 `‹n` / `n›`，点一下把视图移过去（只移动视图、不自动出声）。
 *
 * 播放器是全局唯一的那一个（`audio/player.ts`）：录音按钮旁边和这里点的是同一个声音。
 */
const store = useTradeStore()

function urlOf(clip: AudioClip): string {
  return store.voiceUrl(clip)
}

/** 盘面上摆哪些语音：整份清单（复盘是往前走的，早先几根早就划出视野了）。 */
const clips = computed<AudioClip[]>(() => store.voiceAll)

/**
 * 时间 → K 线下标。
 *
 * 键走 `store.bars`（5 分钟原始序列，语音挂在那些时刻上），下标落在 `store.displayBars`
 * （画布真正画的那一份）—— 换成 15 分钟后一条语音仍在它所在的那根上。两串都升序，
 * 一趟双指针就够。
 */
const indexOfTime = computed(() => {
  const map = new Map<string, number>()
  const bars = store.bars
  const shown = store.displayBars
  let j = 0
  for (let i = 0; i < bars.length; i += 1) {
    while (j + 1 < shown.length && shown[j + 1].time <= bars[i].time) j += 1
    if (shown[j] && shown[j].time <= bars[i].time) map.set(bars[i].time, j)
  }
  return map
})

interface BarVoice {
  time: string
  index: number
  /** 标记中心相对**轨道左边缘**的像素（轨道自己已经让出了 plot.left） */
  x: number
  /** 标记宽度（跟着 K 线宽度走，缩到 2px 一根时不至于糊成一片） */
  width: number
  clips: AudioClip[]
  seconds: number
}

const markers = computed<BarVoice[]>(() => {
  const vp = chartViewport
  const idx = indexOfTime.value
  const byTime = new Map<string, AudioClip[]>()
  for (const c of clips.value) {
    const list = byTime.get(c.time)
    if (list) list.push(c)
    else byTime.set(c.time, [c])
  }
  const out: BarVoice[] = []
  for (const [time, list] of byTime) {
    const i = idx.get(time)
    // 挂在当前盘面没有的 K 线上（换过数据目录）：横轴上没有落点，跳过
    if (i === undefined) continue
    list.sort((a, b) => a.createdAt - b.createdAt)
    out.push({
      time,
      index: i,
      // 画布上是 `plot.left + (i - scroll + 0.5) * bw`；轨道的左边缘就是 plot.left，
      // 所以这里不能再加一次 left
      x: (i - vp.scroll + 0.5) * vp.barWidth,
      width: clamp(vp.barWidth * 0.9, 9, 20) + (list.length > 1 ? 4 : 0),
      clips: list,
      seconds: list.reduce((s, c) => s + c.durationMs, 0) / 1000,
    })
  }
  return out.sort((a, b) => a.index - b.index)
})

/** 轨道宽度（= 行情区宽度） */
const trackWidth = computed(() => chartViewport.right - chartViewport.left)

/** 摆不上轴的语音：挂在当前盘面没有的 K 线上（换过数据目录的老存档）。 */
const orphan = computed(() => clips.value.filter((c) => !indexOfTime.value.has(c.time)))

/** 视野外多留 10px：贴边的那半个标记也算「看得见」 */
const EDGE = 10
const shown = computed(() => markers.value.filter((m) => m.x >= -EDGE && m.x <= trackWidth.value + EDGE))
const offLeft = computed(() => markers.value.filter((m) => m.x < -EDGE))
const offRight = computed(() => markers.value.filter((m) => m.x > trackWidth.value + EDGE))

/** 当前这根 K 线（账户停在哪根）：给它加一圈，和录音按钮上的「本根」对上 */
const currentTime = computed(() => store.voice.barTime || store.currentTime)

const totalSeconds = computed(() => clips.value.reduce((s, c) => s + c.durationMs, 0) / 1000)

function clock(seconds: number): string {
  const s = Math.round(seconds)
  if (s < 60) return `${s} 秒`
  return `${Math.floor(s / 60)} 分 ${String(s % 60).padStart(2, '0')} 秒`
}

function playingHere(m: BarVoice): boolean {
  return m.clips.some((c) => c.file === voiceCurrentFile.value)
}

/** 点标记：这一组正在响就停，否则**整组**按顺序放。 */
function toggleGroup(m: BarVoice): void {
  if (playingHere(m)) {
    voiceStop()
    return
  }
  voicePlayList(m.clips, urlOf)
}

function toggleAll(): void {
  if (voicePlaying.value) {
    voiceStop()
    return
  }
  const all = [...clips.value].sort((a, b) =>
    a.time === b.time ? a.createdAt - b.createdAt : a.time < b.time ? -1 : 1,
  )
  voicePlayList(all, urlOf)
}

function markerTitle(m: BarVoice): string {
  const head = `${formatAxisTime(m.time)} · ${m.clips.length} 条 · ${m.seconds.toFixed(1)} 秒`
  if (playingHere(m)) return `${head}\n正在播放，单击停止`
  return `${head}\n单击按顺序播放这根 K 线讲解的 ${m.clips.length} 条语音`
}

const allTitle = computed(() => {
  if (voicePlaying.value) return '停止播放'
  if (clips.value.length === 0) return '还没有语音可放'
  return `按时间顺序连播全部 ${clips.value.length} 条语音（共 ${clock(totalSeconds.value)}）`
})

/** 没有可点的东西时的一句话（空的时候要教怎么录） */
const hint = computed(() => {
  if (voiceError.value) return voiceError.value
  if (clips.value.length === 0) return '还没有语音讲解 · 点工具条【🎙 录音】，或按 空格 讲两句'
  if (shown.value.length > 0) return ''
  if (markers.value.length === 0) return '这些语音挂在已经不在的 K 线上（数据目录换过？）'
  const later = orphan.value.length > 0 ? ` · 另有 ${orphan.value.length} 条挂不上时间轴` : ''
  return `语音都不在视野内 · 点轨道两端的 ‹n / n› 移过去${later}`
})

/** 换账户：手上那条语音可能已经不属于眼前这块盘面了，先停下 */
watch(
  () => store.accountId,
  () => voiceStop(),
)

onBeforeUnmount(() => voiceStop())
</script>

<template>
  <div
    v-if="store.instance"
    class="relative h-10 shrink-0 select-none border-t border-line bg-panel text-[11px] text-dim"
    data-testid="voice-tape"
  >
    <!-- 轨道：左右边距和行情区完全一致，所以 `x` 可以直接用像素摆 -->
    <div
      class="absolute inset-y-0"
      :style="{ left: `${chartViewport.left}px`, right: `${PRICE_AXIS_WIDTH}px` }"
    >
      <div class="pointer-events-none absolute inset-x-0 top-1/2 h-px bg-line" />

      <div
        v-if="hint"
        class="pointer-events-none absolute inset-y-0 flex items-center truncate pr-2"
        :class="[
          offLeft.length ? 'left-7' : 'left-2',
          offRight.length ? 'right-7' : 'right-2',
          voiceError ? 'text-amber-600' : 'text-faint',
        ]"
      >
        {{ hint }}
      </div>

      <!-- 视野外的语音：两端各露一个数字。没有它，人不知道往哪边拖 -->
      <button
        v-if="offLeft.length"
        type="button"
        class="absolute top-1/2 left-0 z-20 -translate-y-1/2 rounded border border-line bg-elev px-1 text-[9px] leading-4 text-faint hover:border-accent hover:text-accent"
        :title="`视野左侧还有 ${offLeft.length} 根 K 线上有语音，点击把视图移过去`"
        @click="chartNav.centerOn(offLeft[offLeft.length - 1].index)"
      >
        ‹{{ offLeft.length }}
      </button>
      <button
        v-if="offRight.length"
        type="button"
        class="absolute top-1/2 right-0 z-20 -translate-y-1/2 rounded border border-line bg-elev px-1 text-[9px] leading-4 text-faint hover:border-accent hover:text-accent"
        :title="`视野右侧还有 ${offRight.length} 根 K 线上有语音，点击把视图移过去`"
        @click="chartNav.centerOn(offRight[0].index)"
      >
        {{ offRight.length }}›
      </button>

      <!-- 每根有语音的 K 线一个标记：小竖条里画这一条的波形，多条时右上角标数字 -->
      <button
        v-for="m in shown"
        :key="m.time"
        type="button"
        class="absolute top-1/2 z-10 flex -translate-y-1/2 items-center justify-center rounded-[3px] border p-[2px] transition hover:z-30"
        :class="[
          playingHere(m)
            ? 'border-amber-500 bg-amber-500/10 text-amber-600'
            : 'border-line bg-elev text-accent hover:border-accent',
          m.time === currentTime && !playingHere(m) ? 'ring-1 ring-accent/50' : '',
        ]"
        :style="{ left: `${m.x - m.width / 2}px`, width: `${m.width}px`, height: '20px' }"
        :title="markerTitle(m)"
        @click="toggleGroup(m)"
      >
        <AudioWave :url="urlOf(m.clips[0])" :active="playingHere(m)" />
        <span
          v-if="playingHere(m)"
          class="pointer-events-none absolute bottom-0 left-0 h-[2px] rounded-full bg-amber-500"
          :style="{ width: `${voiceProgress * 100}%` }"
        />
        <span
          v-if="m.clips.length > 1"
          class="tnum pointer-events-none absolute -top-1.5 -right-1 rounded-full border border-line bg-panel px-[3px] text-[8px] leading-[10px]"
          :class="playingHere(m) ? 'text-amber-600' : 'text-faint'"
        >
          {{ m.clips.length }}
        </span>
      </button>
    </div>

    <!-- 右端那一列：宽度和价格轴一样，视觉上接着价格轴往下 -->
    <div
      class="absolute inset-y-0 right-0 flex flex-col items-center justify-center gap-[2px] border-l border-line"
      :style="{ width: `${PRICE_AXIS_WIDTH}px` }"
    >
      <span class="leading-4 text-faint">🎙 {{ clips.length }} 条</span>
      <button
        type="button"
        class="rounded border border-line px-1 leading-4 transition disabled:cursor-not-allowed disabled:opacity-40"
        :class="
          voicePlaying
            ? 'border-amber-500 text-amber-600'
            : 'text-dim hover:border-accent hover:text-accent'
        "
        :disabled="clips.length === 0"
        :title="allTitle"
        @click="toggleAll"
      >
        {{ voicePlaying ? '■ 停止' : '▶ 连播' }}
      </button>
    </div>
  </div>
</template>
