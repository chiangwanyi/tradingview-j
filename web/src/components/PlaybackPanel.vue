<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'

import { ClipRecorder, toBase64 } from '@/audio/mic'
import AudioWave from '@/components/AudioWave.vue'
import { usePlaybackStore } from '@/stores/playback'
import { stepKindClass, stepKindLabel } from '@/stores/step-kinds'

/**
 * 底部停靠区里的播放器 —— **正在回放时才出现**（v0.20.0 起底部不再有【回放】Tab，
 * 见 TradeDock.vue 的头注释）。
 *
 * v0.19.0 之前这里同时是「交易记录列表」：左边列记录、点一条就地回放。
 * 列表搬去 `/records` 之后，这个面板就只剩播放控制 —— 因为回放必须发生在画布上，
 * 而列表要的是占满一屏的统计和图表，两件事本来就该待在不同页面。
 *
 * 底下的空状态（「现在没有在回放」）留作兜底：它能出现的唯一情况是「刚退出回放、
 * 面板还没来得及收起来」，这时给的是一个明确的出口，而不是一片空白。
 *
 * 播放器本身没变：一步 = 一次操作，步进条按类型上色、按 K 线分块；
 * 空格 = 在当步里逐条放语音（见 views/ReplayView.vue 的 onSpace）。
 */

const pb = usePlaybackStore()

const total = computed(() => pb.recording?.steps?.length ?? 0)
const current = computed(() => pb.step)
const stepAudio = computed(() => pb.clips.length)
const clipsNow = computed(() => pb.clips)

function clockMs(ms: number): string {
  const s = Math.round(ms / 1000)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

/** 点一条就播；再点同一条 = 停。 */
function playClip(file: string): void {
  const c = clipsNow.value.find((x) => x.file === file)
  if (c) pb.playClip(c)
}

/** 点步进条 = 暂停 + 跳到那一步（手动跳步不打断人看盘，所以不自动继续播）。 */
function seek(n: number): void {
  pb.pause()
  pb.goto(n)
}

const btn =
  'rounded border border-line px-2 py-0.5 text-[13px] transition disabled:cursor-not-allowed disabled:opacity-40'

// ---------------- 在延伸出来的 K 线上录音（v0.27.0） ----------------
//
// 麦克风和「按一下开始、再按一下结束」那一套完全沿用复盘里的语音浮窗
// （见 components/VoiceFloat.vue），只是这里不需要浮窗：回放是只读的，
// 能录的位置只有末尾那一根，所以按钮就摆在播放器上，不占画布。
//
// 录音的校验（每根 9 条 / 2MB / WAV）在服务端，和复盘录音是同一份实现 ——
// 这里不做重复判断，只把「录了几条」显示出来。

/** 每根 K 线上最多几条（和 voice.go 的 maxClipsPerBar 对齐，只用于显示） */
const MAX_PER_BAR = 9
const capturing = ref(false)
const busy = ref(false)
const hint = ref('')
/** 「清空本根」的两段式确认 */
const armedClear = ref(false)
let recorder: ClipRecorder | null = null
let hintTimer = 0
let clearTimer = 0

const tailClips = computed(() => pb.extensionClips)
const tailTime = computed(() => pb.extensionTail?.time ?? '')

function setHint(text: string, ms = 6000): void {
  hint.value = text
  if (hintTimer) window.clearTimeout(hintTimer)
  hintTimer = 0
  if (text && ms > 0) {
    hintTimer = window.setTimeout(() => {
      hint.value = ''
    }, ms)
  }
}

async function startClip(): Promise<void> {
  if (capturing.value || busy.value || !pb.onExtensionTail) return
  setHint('')
  recorder = new ClipRecorder()
  try {
    await recorder.start('')
    capturing.value = true
  } catch (e) {
    recorder = null
    const msg = String(e)
    setHint(
      /NotAllowed|Permission/i.test(msg)
        ? '麦克风权限被拒绝了：浏览器地址栏左侧的图标里允许麦克风'
        : `开不了麦克风：${msg}`,
    )
  }
}

async function stopClip(): Promise<void> {
  if (!capturing.value || !recorder) return
  capturing.value = false
  busy.value = true
  try {
    const out = await recorder.stop()
    recorder = null
    if (!out) {
      setHint('这段太短了（不到 0.25 秒），没有上传')
      return
    }
    if (!pb.addExtensionVoice(toBase64(out.wav), out.durationMs)) {
      setHint('这会儿不能录：先停在最后一步，并且连接没断')
    }
  } catch (e) {
    setHint(`录音处理失败：${String(e)}`)
  } finally {
    busy.value = false
  }
}

function toggleClip(): void {
  if (capturing.value) void stopClip()
  else void startClip()
}

/** 清空本根：点一下变红，再点一下才真删（和语音浮窗一致）。 */
function clearBar(): void {
  if (!armedClear.value) {
    armedClear.value = true
    if (clearTimer) window.clearTimeout(clearTimer)
    clearTimer = window.setTimeout(() => {
      armedClear.value = false
    }, 3000)
    return
  }
  armedClear.value = false
  pb.clearExtensionVoice()
}

// 回放结束 / 换了记录 / 离开末尾那根：麦克风必须松开（录了也没有落点）
watch(
  () => `${pb.active}|${pb.recording?.id ?? ''}|${pb.onExtensionTail}`,
  () => {
    armedClear.value = false
    setHint('')
    if (!capturing.value) return
    recorder?.cancel()
    recorder = null
    capturing.value = false
  },
)

onBeforeUnmount(() => {
  if (hintTimer) window.clearTimeout(hintTimer)
  if (clearTimer) window.clearTimeout(clearTimer)
  recorder?.cancel()
  recorder = null
})
</script>

<template>
  <div class="flex h-full flex-col text-[13px]">
    <!-- ---------------- 没有在回放 ---------------- -->
    <div
      v-if="!pb.active"
      class="flex h-full flex-col items-center justify-center gap-2 text-center"
    >
      <p class="text-dim">现在没有在回放。</p>
      <p class="text-[12px] text-faint">
        回放的内容是<b class="text-dim">已经记下来的交易记录</b>
        —— 一笔从开仓到平仓的全部操作，一格一格演在画布上。
      </p>
      <RouterLink
        :to="{ name: 'records' }"
        class="rounded border border-line px-3 py-1 text-[13px] text-accent transition hover:border-accent hover:bg-accent-soft"
      >
        去交易记录页挑一条 →
      </RouterLink>
    </div>

    <!-- ---------------- 播放器 ---------------- -->
    <template v-else>
      <div class="flex items-center gap-2 border-b border-line px-2 py-1">
        <button
          type="button"
          :class="[btn, 'shrink-0']"
          title="退出回放，回到原来的实例"
          @click="pb.close()"
        >
          ← 退出回放
        </button>
        <span class="min-w-0 truncate font-medium text-ink">{{ pb.recording?.name }}</span>
        <span class="tnum shrink-0 text-dim">第 {{ current }}/{{ total }} 步</span>
        <span class="shrink-0 rounded bg-panel px-1 text-[11px] text-dim">
          {{ pb.currentKindLabel }}
        </span>
        <span class="tnum hidden shrink-0 text-faint sm:inline">
          第 {{ pb.barNo }}/{{ pb.recording?.bars }} 根 · {{ pb.currentStep?.time }}
        </span>
        <span
          v-if="stepAudio > 0"
          class="shrink-0 rounded bg-amber-500/15 px-1 text-[11px] text-amber-600"
        >
          本步 {{ stepAudio }} 条语音
        </span>
        <span class="ml-auto flex shrink-0 items-center gap-2 text-[11px]">
          <RouterLink :to="{ name: 'records' }" class="text-faint hover:text-accent">
            全部记录与统计 →
          </RouterLink>
          <span :class="pb.audioPlaying ? 'text-amber-600' : 'text-faint'">
            {{ pb.audioPlaying ? pb.audioLabel : pb.playing ? '播放中' : '已暂停' }}
          </span>
        </span>
      </div>

      <div class="flex flex-wrap items-center gap-1.5 px-2 py-1">
        <button
          type="button"
          :class="btn"
          title="重播：回到第 1 步并连续播放"
          @click="pb.restart()"
        >
          ⟲ 重播
        </button>
        <button
          type="button"
          :class="btn"
          title="上一步"
          :disabled="current <= 1"
          @click="pb.prevStep()"
        >
          ◀
        </button>
        <button
          type="button"
          class="rounded bg-emerald-600 px-3 py-0.5 text-[13px] font-medium text-white hover:bg-emerald-500"
          :title="pb.playing ? '暂停' : '连续播放'"
          @click="pb.toggle()"
        >
          {{ pb.playing ? '⏸ 暂停' : '▶ 播放' }}
        </button>
        <button
          type="button"
          :class="btn"
          title="单步：往后走一步并停住"
          :disabled="pb.atEnd"
          @click="pb.nextStep()"
        >
          单步 ▶|
        </button>

        <!--
          回放走完之后往后加载一根（v0.27.0）：只在最后一步出现 ——
          中间插一根会让后面所有步的 K 线时间和游标错位，
          画在上面的图形、挂在上面的话都会跑到别处去。
        -->
        <template v-if="pb.atEnd">
          <button
            v-if="pb.extendHasMore"
            type="button"
            class="rounded border border-cyan-500 px-2 py-0.5 text-[13px] text-cyan-700 transition hover:bg-cyan-500/10 disabled:cursor-not-allowed disabled:opacity-40"
            :disabled="!pb.canExtend"
            title="步数已经走完，往后加载真实行情里的下一根 K 线（会存进这条交易记录）"
            data-testid="extend-next"
            @click="pb.extendNext()"
          >
            {{ pb.extending ? '加载中…' : '＋ 加载下一根' }}
          </button>
          <span v-else class="rounded bg-panel px-1 text-[11px] text-faint">
            数据已到最后一根
          </span>
        </template>

        <span class="mx-1 h-3 w-px bg-line" />
        <button
          v-for="s in [1, 2, 4] as const"
          :key="s"
          type="button"
          :class="[btn, pb.speed === s ? 'border-accent bg-accent-soft text-accent' : '']"
          :title="`连续播放速度 ${s}x`"
          @click="pb.setSpeed(s)"
        >
          {{ s }}x
        </button>

        <span class="mx-1 h-3 w-px bg-line" />
        <label
          class="flex cursor-pointer items-center gap-1 text-[12px] text-dim"
          title="走到含语音的步骤时自动顺序播放语音"
        >
          <input v-model="pb.autoAudio" type="checkbox" class="accent-sky-600" />
          自动放语音
        </label>
        <button
          type="button"
          :class="[btn, stepAudio === 0 ? 'opacity-40' : '']"
          :disabled="stepAudio === 0"
          title="空格：在当前这一步里一条条播放语音（正在播时再按一次为停止）"
          @click="pb.spacePlay()"
        >
          空格逐条
        </button>
      </div>

      <!--
        在「延伸出来的最后一根」上录音（v0.27.0）：只在这一步出现。
        录下来的语音会以**语音步**的形式存进这条交易记录，重新打开回放时照样在。
      -->
      <div
        v-if="pb.onExtensionTail"
        class="flex flex-wrap items-center gap-1.5 border-t border-cyan-500/40 bg-cyan-500/5 px-2 py-1"
        data-testid="extend-voice"
      >
        <span class="shrink-0 text-[11px] text-cyan-700">
          延伸的 {{ tailTime }} 这根
        </span>
        <button
          type="button"
          class="shrink-0 rounded border px-2 py-0.5 text-[12px] transition"
          :class="
            capturing
              ? 'border-red-500 bg-red-500/10 text-red-600'
              : 'border-line text-dim hover:border-accent hover:text-accent'
          "
          :disabled="busy || tailClips.length >= MAX_PER_BAR"
          :title="
            tailClips.length >= MAX_PER_BAR
              ? '这根已经录满 9 条；再往后加载一根就能接着录'
              : '对着这根 K 线讲一段（再点一下结束）'
          "
          @click="toggleClip"
        >
          {{ capturing ? '■ 结束录音' : busy ? '编码中…' : '🎙 在这根上录音' }}
        </button>
        <span class="tnum shrink-0 text-[11px] text-faint">
          本根 {{ tailClips.length }}/{{ MAX_PER_BAR }}
        </span>
        <button
          v-if="tailClips.length"
          type="button"
          class="shrink-0 rounded px-1 text-[11px]"
          :class="armedClear ? 'bg-red-600 text-white' : 'text-faint hover:text-red-600'"
          :title="armedClear ? '再点一下确认清空（只清这根，不可撤销）' : '清空这根上的全部语音'"
          @click="clearBar"
        >
          {{ armedClear ? '确认清空' : '清空本根' }}
        </button>
        <span class="text-[11px] text-faint">
          {{ hint || '录下来的语音会存进这条交易记录，重新打开回放时还在' }}
        </span>
      </div>

      <!-- 本步的语音：波形 + 时长，点一条就播（步进条上那个橙点只是"有语音"的记号，
           真要听哪一条得在这里点） -->
      <div
        v-if="clipsNow.length"
        class="flex items-center gap-1 overflow-x-auto border-t border-line px-2 py-1"
      >
        <span class="shrink-0 text-[11px] text-faint">本步语音</span>
        <button
          v-for="(c, i) in clipsNow"
          :key="c.file"
          type="button"
          class="flex shrink-0 items-center gap-1 rounded border px-1 py-0.5 text-[11px] transition"
          :class="
            pb.currentClip?.file === c.file
              ? 'border-amber-500 bg-amber-500/10 text-amber-600'
              : 'border-line text-dim hover:border-accent hover:text-accent'
          "
          :title="`第 ${i + 1} 条 · ${c.time} · ${(c.durationMs / 1000).toFixed(1)} 秒（点一下播放，再点停止）`"
          :data-testid="`clip-${c.file}`"
          @click="playClip(c.file)"
        >
          <span>{{ pb.currentClip?.file === c.file ? '⏸' : '▶' }}</span>
          <span class="h-3 w-14">
            <AudioWave :url="pb.audioUrl(c)" :active="pb.currentClip?.file === c.file" />
          </span>
          <span class="tnum">{{ clockMs(c.durationMs) }}</span>
        </button>
      </div>

      <!-- 步进条：一步一格（一步 = 一次操作），按类型上色、按 K 线分块；点一格直接跳过去 -->
      <div class="min-h-0 flex-1 overflow-x-auto px-2 pb-1">
        <div class="flex items-end gap-[3px] pt-1">
          <button
            v-for="s in pb.steps"
            :key="s.step"
            type="button"
            class="group relative h-8 w-[10px] shrink-0 rounded-sm border transition"
            :class="[
              s.step === current
                ? 'border-red-500 bg-red-500/70'
                : s.step < current
                  ? stepKindClass(s.kind)
                  : [stepKindClass(s.kind), 'opacity-40 hover:opacity-100'],
              pb.barStarts.has(s.step) ? 'ml-[5px]' : '',
            ]"
            :title="`第 ${s.step} 步 · ${s.time} · ${stepKindLabel(s.kind)}${s.by ? ' · ' + s.by : ''}${
              pb.audioSteps.has(s.step) ? ' · 有语音' : ''
            }`"
            @click="seek(s.step)"
          >
            <span
              v-if="pb.audioSteps.has(s.step)"
              class="absolute bottom-0.5 left-1/2 h-1 w-1 -translate-x-1/2 rounded-full bg-amber-500"
            />
          </button>
        </div>
        <div class="mt-1 flex items-center gap-2 text-[11px] text-faint">
          <span class="flex items-center gap-1"
            ><i class="h-2 w-2 rounded-sm border border-sky-500/80 bg-sky-500/40" />下一根</span
          >
          <span class="flex items-center gap-1"
            ><i
              class="h-2 w-2 rounded-sm border border-emerald-500/80 bg-emerald-500/35"
            />画图</span
          >
          <span class="flex items-center gap-1"
            ><i
              class="h-2 w-2 rounded-sm border border-violet-500/80 bg-violet-500/35"
            />下单/成交</span
          >
          <span class="flex items-center gap-1"
            ><i class="h-2 w-2 rounded-sm border border-amber-500 bg-amber-500/50" />语音</span
          >
          <span class="flex items-center gap-1"
            ><i class="h-2 w-2 rounded-sm border border-slate-400/70 bg-slate-400/20" />入场前</span
          >
          <span class="ml-auto">一格 = 一次操作，换 K 线处留空</span>
        </div>
      </div>
    </template>
  </div>
</template>
