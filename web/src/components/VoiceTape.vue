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
import { usePlaybackStore } from '@/stores/playback'
import { useReplayStore } from '@/stores/replay'
import type { AudioClip } from '@/types/protocol'

/**
 * 时间横轴下方的**录音条**（v0.17.0）：把盘面上所有语音讲解摆到时间轴上听。
 *
 * 为什么放在横轴正下方，而不是再加一个浮窗：
 *   「这条语音讲的是哪一根 K 线」是这个功能里最要紧的信息，而**只有横轴说得清**。
 *   浮窗里的清单只能说「10-16 09:45 有一条」，人还得自己在图上找 09:45 在哪。
 *   贴在横轴底下、**左右边距和行情区完全一致**（左 10px 到价格轴左沿），
 *   一个标记就正好落在它那根 K 线的正下方 —— 缩放、平移、换实例都跟着走，
 *   因为它和画布读的是同一份 `chartViewport`（见 chart/viewport.ts）。
 *
 * 布局（整条 40px 高，横向只有三块）：
 *
 *   ┌──────────────────────────────────────────────┬────────┐
 *   │  ──●──────●──●─────────（轨道，和 K 线对齐）  │ 🎙 12 条│
 *   │                                              │ ▶ 连播 │
 *   └──────────────────────────────────────────────┴────────┘
 *      ↑ 行情区左边界                       价格轴左沿 ↑   ↑ 与价格轴同宽(86px)
 *
 *   右端那一列刻意做成**和价格轴一样宽**：视觉上它接着价格轴往下，不是另起一块。
 *
 * 交互只有三条，都能一眼看出结果：
 *   1. 点某个标记 = 按顺序放**这根 K 线**上的语音（多条的会标出条数），再点一下停；
 *   2. 点右端【▶ 连播】= 按时间顺序把整块盘面的语音连着放一遍（再点变【■ 停止】）；
 *   3. 语音在视野之外时，轨道两端出现 `‹n` / `n›`，点一下把视图移过去
 *      （只移动视图、不自动出声 —— 免得「我只是想看看」被一段讲解糊一脸）。
 *
 * 一个声音：所有播放都走 `audio/player.ts` 里的单例，和语音浮窗共用 ——
 * 两处各点一次不会叠着响，「哪条在响」也就只有一种高亮。
 *
 * 回放时同样在（否则进回放画布会突然长高 40px）：这时清单换成**录像里**的语音，
 * 地址也换成录像那条路由；点标记只出声、不改步 —— 步进是回放自己的事。
 */

const store = useReplayStore()
const pb = usePlaybackStore()

/** 每条语音的地址：盘面那份和录像那份是两条不同的路由（不复用，见 README「语音讲解」）。 */
function urlOf(clip: AudioClip): string {
  if (store.playbackActive) return pb.audioUrl(clip)
  const id = store.instance?.id
  if (!id) return ''
  return `/api/instances/${encodeURIComponent(id)}/voice/${encodeURIComponent(clip.file)}`
}

/**
 * 这条带子上摆哪些语音。
 *
 * 实时：`instance.voiceAll`（整块盘面的全量清单）。复盘只能往前走、不能倒回，
 * 所以早先几根的语音在图上早已划出视野 —— 全靠这份清单，它们才还有入口。
 * 回放：录像自己的 `audios`（都挂在步上，但**时间**同样存在，横轴照样对得上）。
 */
const clips = computed<AudioClip[]>(() => {
  if (!store.playbackActive) return store.voiceAll
  return pb.recording?.audios ?? []
})

/** 时间 → K 线下标。语音的落点是**时间**（存储键），横轴要的是下标，这里是一次性换算。 */
const indexOfTime = computed(() => {
  const map = new Map<string, number>()
  const bars = store.bars
  for (let i = 0; i < bars.length; i += 1) map.set(bars[i].time, i)
  return map
})

interface BarVoice {
  /** 这根 K 线的时间（同一根上的几条合成一个标记） */
  time: string
  index: number
  /**
   * 标记中心相对**轨道左边缘**的像素（轨道自己已经让出了 plot.left，所以这里不再加它）。
   * 和画布 `xOf()` 差一个常量偏移 plot.left，两者必须严格同时变 —— 见 markers 里的注释。
   */
  x: number
  /** 标记宽度（跟着 K 线宽度走，缩小到 2px 一根时不至于糊成一片） */
  width: number
  clips: AudioClip[]
  seconds: number
}

/**
 * 每根 K 线一个标记（同一根上最多 9 条，逐条画会重叠成一坨）。
 *
 * 为什么按 K 线而不是按条：横轴的刻度就是 K 线，一个位置只能有一个「东西」；
 * 一组里的几条按步号/录制顺序排好，点一下连着放 —— 这正是「听这根讲了什么」。
 */
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
    // 挂在已经不存在的 K 线上（换过数据集的老存档）：横轴上没有落点，跳过
    if (i === undefined) continue
    list.sort((a, b) => a.step - b.step || a.createdAt - b.createdAt)
    out.push({
      time,
      index: i,
      // 画布上是 `plot.left + (i - scroll + 0.5) * bw`；轨道的左边缘**就是** plot.left，
      // 所以这里不能再加一次 left（加过一次，标记整体右偏 10px，对齐核对脚本抓出来的）
      x: (i - vp.scroll + 0.5) * vp.barWidth,
      // 宽度跟着 K 线走（一根柱子的 9 成），上下限保证再小也点得中、再大也不抢戏。
      // 相邻两根都有语音时两个标记会紧挨着（偶尔压住 1~2px，悬停会提到最上层），
      // 缩放到底时会连成一条「语音密集区」—— 那正是该有的样子。
      // 同一根上多条时再宽 4px：右上角那个条数角标要个落脚的地方。
      width: clamp(vp.barWidth * 0.9, 9, 20) + (list.length > 1 ? 4 : 0),
      clips: list,
      seconds: list.reduce((s, c) => s + c.durationMs, 0) / 1000,
    })
  }
  return out.sort((a, b) => a.index - b.index)
})

/** 轨道宽度（= 行情区宽度）：标记的 x 是轨道内的坐标，判「在不在视野里」要用它 */
const trackWidth = computed(() => chartViewport.right - chartViewport.left)

/** 视野外多留 10px：贴边的那半个标记也算「看得见」，不让它突然跳到两侧的数字里去 */
const EDGE = 10
const shown = computed(() => markers.value.filter((m) => m.x >= -EDGE && m.x <= trackWidth.value + EDGE))
const offLeft = computed(() => markers.value.filter((m) => m.x < -EDGE))
const offRight = computed(() => markers.value.filter((m) => m.x > trackWidth.value + EDGE))

/** 当前这根 K 线（实时 = 游标那根，回放 = 这一步那根）：给它加一圈，和浮窗里的「本根」对上 */
const currentTime = computed(() =>
  store.playbackActive ? (pb.currentStep?.time ?? '') : store.voice.barTime || store.currentBar?.time || '',
)

const totalSeconds = computed(() => clips.value.reduce((s, c) => s + c.durationMs, 0) / 1000)

function clock(seconds: number): string {
  const s = Math.round(seconds)
  if (s < 60) return `${s} 秒`
  return `${Math.floor(s / 60)} 分 ${String(s % 60).padStart(2, '0')} 秒`
}

function playingHere(m: BarVoice): boolean {
  return m.clips.some((c) => c.file === voiceCurrentFile.value)
}

/** 点标记：这一组正在响就停，否则**整组**按顺序放（不是只放第一条）。 */
function toggleGroup(m: BarVoice): void {
  if (playingHere(m)) {
    voiceStop()
    return
  }
  voicePlayList(m.clips, urlOf)
}

/**
 * 【连播】：整块盘面的语音按时间连着放一遍。
 *
 * 用的是「有序清单」而不是「视野内的那些」：这个按钮的意思是「把讲解过一遍」，
 * 跟着视野变会让人听一半发现少了。
 */
function toggleAll(): void {
  if (voicePlaying.value) {
    voiceStop()
    return
  }
  const all = [...clips.value].sort((a, b) =>
    a.time === b.time ? a.step - b.step || a.createdAt - b.createdAt : a.time < b.time ? -1 : 1,
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

/**
 * 轨道左端那行小字：只在「没有可点的东西」时出现 ——
 * 空的时候要教怎么录，全在视野外的时候要说清语音没丢、只是划出去了。
 * 有标记可点的时候就别再占位置（标记本身就是最好的说明）。
 */
const hint = computed(() => {
  if (voiceError.value) return voiceError.value
  if (clips.value.length === 0) {
    return store.playbackActive ? '这条录像里没有语音' : '还没有语音讲解 · 点工具条【🎙 录音】，或按 空格 讲两句'
  }
  if (shown.value.length > 0) return ''
  // 有语音却一个标记都摆不出来：不是「在视野外」，是**根本没有那根 K 线**
  // （换过数据集的老存档会这样），别拿「点两侧的箭头」去糊弄人
  if (markers.value.length === 0) return '这些语音挂在已经不在的 K 线上（数据目录换过？）'
  return '语音都不在视野内 · 点轨道两端的 ‹n / n› 移过去'
})

// 换实例 / 进出回放：手上那条语音可能已经不属于眼前这块盘面了，先停下
watch(
  () => `${store.instance?.id ?? ''}|${store.playbackActive}`,
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
    <!--
      轨道：左右边距和行情区完全一致（左 10px、右边让出价格轴），
      所以 `x` 可以直接用像素摆，不用再折算一次。
    -->
    <div
      class="absolute inset-y-0"
      :style="{ left: `${chartViewport.left}px`, right: `${PRICE_AXIS_WIDTH}px` }"
    >
      <!-- 基线：一条极淡的横线，说明这条带子是时间轴的延伸（标记就骑在它上面） -->
      <div class="pointer-events-none absolute inset-x-0 top-1/2 h-px bg-line" />

      <!--
        空态/出错时的一句话：让开两端那两个箭头，别被它们压住；
        出错了（取不到音频、浏览器拦自动播放）用琥珀色，和浮窗里的提示同一个颜色。
      -->
      <div
        v-if="hint"
        class="pointer-events-none absolute inset-y-0 flex items-center truncate pr-2"
        :class="[
          offLeft.length ? 'left-7' : 'left-2',
          offRight.length ? 'right-7' : 'right-2',
          voiceError ? 'text-amber-600 dark:text-amber-400' : 'text-faint',
        ]"
      >
        {{ hint }}
      </div>

      <!--
        视野外的语音：只在两端露一个数字。
        复盘一路往前走，早先几根早就划出去了；没有这两个数字，人根本不知道该往哪边拖。
      -->
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

      <!--
        每根有语音的 K 线一个标记：小竖条里画**这一条的波形**（真解码出来的包络，
        不是装饰），同一根上有多条时右上角标数字。点一下连着放这根上的全部。
      -->
      <button
        v-for="m in shown"
        :key="m.time"
        type="button"
        class="absolute top-1/2 z-10 flex -translate-y-1/2 items-center justify-center rounded-[3px] border p-[2px] transition hover:z-30"
        :class="[
          playingHere(m)
            ? 'border-amber-500 bg-amber-500/10 text-amber-600 dark:text-amber-400'
            : 'border-line bg-elev text-accent hover:border-accent',
          m.time === currentTime && !playingHere(m) ? 'ring-1 ring-accent/50' : '',
        ]"
        :style="{ left: `${m.x - m.width / 2}px`, width: `${m.width}px`, height: '20px' }"
        :title="markerTitle(m)"
        @click="toggleGroup(m)"
      >
        <AudioWave :url="urlOf(m.clips[0])" :active="playingHere(m)" />
        <!-- 正在放的那条：底部一条进度，和声音走到哪儿对得上 -->
        <span
          v-if="playingHere(m)"
          class="pointer-events-none absolute bottom-0 left-0 h-[2px] rounded-full bg-amber-500"
          :style="{ width: `${voiceProgress * 100}%` }"
        />
        <span
          v-if="m.clips.length > 1"
          class="tnum pointer-events-none absolute -top-1.5 -right-1 rounded-full border border-line bg-panel px-[3px] text-[8px] leading-[10px]"
          :class="playingHere(m) ? 'text-amber-600 dark:text-amber-400' : 'text-faint'"
        >
          {{ m.clips.length }}
        </span>
      </button>
    </div>

    <!--
      右端那一列：宽度和**价格轴**一样，视觉上接着价格轴往下。
      🎙 n 条 = 顺手打开语音浮窗（录音入口）；▶ 连播 = 把全部按顺序过一遍。
    -->
    <div
      class="absolute inset-y-0 right-0 flex flex-col items-center justify-center gap-[2px] border-l border-line"
      :style="{ width: `${PRICE_AXIS_WIDTH}px` }"
    >
      <button
        v-if="!store.playbackActive"
        type="button"
        class="rounded px-1 leading-4 text-faint hover:text-accent"
        title="语音讲解浮窗：开关麦克风、看本根清单（和工具条上的【🎙 录音】是同一个入口）"
        @click="store.toggleVoicePanel()"
      >
        🎙 {{ clips.length }} 条
      </button>
      <span v-else class="leading-4 text-faint">🎙 {{ clips.length }} 条</span>
      <button
        type="button"
        class="rounded border border-line px-1 leading-4 transition disabled:cursor-not-allowed disabled:opacity-40"
        :class="
          voicePlaying
            ? 'border-amber-500 text-amber-600 dark:text-amber-400'
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
