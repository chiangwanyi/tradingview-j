<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import AudioWave from '@/components/AudioWave.vue'
import { usePlaybackStore } from '@/stores/playback'
import { useReplayStore } from '@/stores/replay'
import type { RecordingSummary } from '@/types/protocol'

/**
 * 底部【回放】Tab：左半边是录像列表，点一条就在画布上放。
 *
 * 列表和播放器放在同一个面板里，是因为它们共用一件事——「当前这条录像」。
 * 拆成两个 Tab 的话，用户想在列表和播放之间来回换就要切两次，而回放的用法
 * 恰恰是「放一遍 → 回列表换一条 → 再放一遍」。
 *
 * 播放控制刻意做成一行按钮 + 一条步进条：复盘看录像时最常做的是「跳到某一步」，
 * 拖进度条比按十几次单步快得多。
 */

const pb = usePlaybackStore()
const store = useReplayStore()

const loading = ref('')
const armed = ref('')
let armTimer = 0

const total = computed(() => pb.recording?.steps?.length ?? 0)
const current = computed(() => pb.step)
const stepAudio = computed(() => pb.clips.length)

/** 本步的录音清单（步进条下面那一行） */
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

function clock(at: number): string {
  const d = new Date(at * 1000)
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
}

function sizeText(n: number): string {
  if (n >= 1024 * 1024) return `${(n / 1024 / 1024).toFixed(1)} MB`
  if (n >= 1024) return `${Math.round(n / 1024)} KB`
  return `${n} B`
}

function pick(rec: RecordingSummary): void {
  if (loading.value) return
  loading.value = rec.id
  store.requestRecording(rec.id)
}

function armDelete(rec: RecordingSummary): void {
  if (armed.value !== rec.id) {
    armed.value = rec.id
    if (armTimer) window.clearTimeout(armTimer)
    armTimer = window.setTimeout(() => {
      armed.value = ''
    }, 3000)
    return
  }
  // 第二下才真删：录像删了没有回收站
  armed.value = ''
  if (pb.recording?.id === rec.id) pb.close()
  store.deleteRecording(rec.id)
}

function exitPlayback(): void {
  pb.close()
}

/** 点步进条 = 暂停 + 跳到那一步（手动跳步不打断人看盘，所以不自动继续播）。 */
function seek(n: number): void {
  pb.pause()
  pb.goto(n)
}

onMounted(() => {
  store.listRecordings()
})

// 录像内容到了 → 交给播放器（请求是列表发出的，播放器只管放）
watch(
  () => store.loadedSeq,
  () => {
    const rec = store.loadedRecording
    loading.value = ''
    if (!rec) return
    store.clearLoadedRecording()
    pb.open(rec)
  },
)

watch(
  () => store.recordings.map((r) => r.id).join(','),
  () => {
    loading.value = ''
  },
)

const btn =
  'rounded border border-line px-2 py-0.5 text-[12px] transition disabled:cursor-not-allowed disabled:opacity-40'
</script>

<template>
  <div class="flex h-full flex-col text-[12px]">
    <!-- ---------------- 列表 ---------------- -->
    <template v-if="!pb.active">
      <div class="flex items-center gap-3 px-2 py-1 text-[11px] text-faint">
        <span>已保存 {{ store.recordings.length }} 条录像</span>
        <span class="min-w-0 flex-1 truncate" :title="store.recordingDir">
          目录 {{ store.recordingDir || '（未配置）' }}
        </span>
        <button type="button" class="hover:text-accent" @click="store.listRecordings()">刷新</button>
      </div>

      <div class="min-h-0 flex-1 overflow-auto px-2 pb-1">
        <div v-if="store.recordings.length === 0" class="py-4 text-faint leading-6">
          还没有录像。<br />
          进入一个实例 → 点操作栏的 <b class="text-dim">● 录制</b> → 逐步操作 → 再点一次结束，
          录像会自动存到上面的目录里。
        </div>
        <div
          v-for="r in store.recordings"
          :key="r.id"
          class="mb-1 flex items-center gap-2 rounded border border-line bg-elev px-2 py-1 hover:border-accent"
        >
          <button type="button" class="min-w-0 flex-1 text-left" @click="pick(r)">
            <div class="flex items-center gap-2">
              <span class="truncate font-medium text-ink">{{ r.name }}</span>
              <span class="shrink-0 rounded bg-panel px-1 text-[10px] text-dim">{{ r.stepCount }} 步</span>
              <span v-if="r.audioCount > 0" class="shrink-0 rounded bg-panel px-1 text-[10px] text-amber-600 dark:text-amber-400">
                {{ r.audioCount }} 录音
              </span>
              <span v-if="loading === r.id" class="shrink-0 text-[10px] text-accent">载入中…</span>
            </div>
            <div class="tnum truncate text-[10px] text-faint">
              {{ r.symbol }} · {{ r.anchor }}<template v-if="r.endTime && r.endTime !== r.anchor"> → {{ r.endTime }}</template>
              · {{ r.eventCount }} 个操作 · {{ sizeText(r.size) }} · {{ clock(r.createdAt) }} · {{ r.createdBy }}
            </div>
          </button>
          <button
            type="button"
            class="shrink-0 rounded px-1.5 py-0.5 text-[11px]"
            :class="
              armed === r.id
                ? 'bg-red-600 text-white'
                : 'text-faint hover:text-red-600 dark:hover:text-red-400'
            "
            :title="armed === r.id ? '再点一下确认删除（不可撤销）' : '删除这条录像'"
            @click.stop="armDelete(r)"
          >
            {{ armed === r.id ? '确认删除' : '删除' }}
          </button>
        </div>
      </div>
    </template>

    <!-- ---------------- 播放器 ---------------- -->
    <template v-else>
      <div class="flex items-center gap-2 border-b border-line px-2 py-1">
        <button type="button" :class="[btn, 'shrink-0']" title="退出回放，回到原来的实例" @click="exitPlayback">
          ← 列表
        </button>
        <span class="min-w-0 truncate font-medium text-ink">{{ pb.recording?.name }}</span>
        <span class="tnum shrink-0 text-dim">第 {{ current }}/{{ total }} 步</span>
        <span class="tnum hidden shrink-0 text-faint sm:inline">{{ pb.currentStep?.time }}</span>
        <span
          v-if="stepAudio > 0"
          class="shrink-0 rounded bg-amber-500/15 px-1 text-[10px] text-amber-600 dark:text-amber-400"
        >
          本步 {{ stepAudio }} 条录音
        </span>
        <span class="ml-auto shrink-0 text-[10px]" :class="pb.audioPlaying ? 'text-amber-600 dark:text-amber-400' : 'text-faint'">
          {{ pb.audioPlaying ? pb.audioLabel : pb.playing ? '播放中' : '已暂停' }}
        </span>
      </div>

      <div class="flex flex-wrap items-center gap-1.5 px-2 py-1">
        <button type="button" :class="btn" title="重播：回到第 1 步并连续播放" @click="pb.restart()">⟲ 重播</button>
        <button type="button" :class="btn" title="上一步" :disabled="current <= 1" @click="pb.prevStep()">◀</button>
        <button
          type="button"
          class="rounded bg-emerald-600 px-3 py-0.5 text-[12px] font-medium text-white hover:bg-emerald-500"
          :title="pb.playing ? '暂停' : '连续播放'"
          @click="pb.toggle()"
        >
          {{ pb.playing ? '⏸ 暂停' : '▶ 播放' }}
        </button>
        <button type="button" :class="btn" title="单步：往后走一步并停住" :disabled="pb.atEnd" @click="pb.nextStep()">
          单步 ▶|
        </button>

        <span class="mx-1 h-3 w-px bg-line" />
        <button
          v-for="s in ([1, 2, 4] as const)"
          :key="s"
          type="button"
          :class="[btn, pb.speed === s ? 'border-accent bg-accent-soft text-accent' : '']"
          :title="`连续播放速度 ${s}x`"
          @click="pb.setSpeed(s)"
        >
          {{ s }}x
        </button>

        <span class="mx-1 h-3 w-px bg-line" />
        <label class="flex cursor-pointer items-center gap-1 text-[11px] text-dim" title="走到含语音的步骤时自动顺序播放录音">
          <input v-model="pb.autoAudio" type="checkbox" class="accent-sky-600" />
          自动放语音
        </label>
        <button
          type="button"
          :class="[btn, stepAudio === 0 ? 'opacity-40' : '']"
          :disabled="stepAudio === 0"
          title="空格：在当前这一步里一条条播放录音（正在播时再按一次为停止）"
          @click="pb.spacePlay()"
        >
          空格逐条
        </button>
      </div>

      <!-- 本步的录音：波形 + 时长，点一条就播（步进条上那个橙点只是"有录音"的记号，
           真要听哪一条得在这里点） -->
      <div
        v-if="clipsNow.length"
        class="flex items-center gap-1 overflow-x-auto border-t border-line px-2 py-1"
      >
        <span class="shrink-0 text-[10px] text-faint">本步录音</span>
        <button
          v-for="(c, i) in clipsNow"
          :key="c.file"
          type="button"
          class="flex shrink-0 items-center gap-1 rounded border px-1 py-0.5 text-[10px] transition"
          :class="
            pb.currentClip?.file === c.file
              ? 'border-amber-500 bg-amber-500/10 text-amber-600 dark:text-amber-400'
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

      <!-- 步进条：一步一格，有语音的格子下面带个点；点格子直接跳过去 -->
      <div class="min-h-0 flex-1 overflow-x-auto px-2 pb-1">
        <div class="flex items-end gap-[3px] pt-1">
          <button
            v-for="s in pb.steps"
            :key="s.step"
            type="button"
            class="group relative h-8 w-[10px] shrink-0 rounded-sm border transition"
            :class="
              s.step === current
                ? 'border-red-500 bg-red-500/70'
                : s.step < current
                  ? 'border-line bg-elev'
                  : 'border-line bg-panel opacity-50 hover:opacity-100'
            "
            :title="`第 ${s.step} 步 · ${s.time} · ${s.events?.length ?? 0} 个操作${
              pb.audioSteps.has(s.step) ? ' · 有录音' : ''
            }`"
            @click="seek(s.step)"
          >
            <span
              v-if="pb.audioSteps.has(s.step)"
              class="absolute bottom-0.5 left-1/2 h-1 w-1 -translate-x-1/2 rounded-full bg-amber-500"
            />
          </button>
        </div>
      </div>
    </template>
  </div>
</template>
