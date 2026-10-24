<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'

import AudioWave from '@/components/AudioWave.vue'
import { usePlaybackStore } from '@/stores/playback'
import { useReplayStore } from '@/stores/replay'
import { stepKindClass, stepKindLabel } from '@/stores/step-kinds'
import type { RecordingSummary, TradeStats } from '@/types/protocol'

/**
 * 底部【交易记录】Tab（v0.18.0 之前叫【回放】）：左边列表，点一条就在画布上回放。
 *
 * **这些记录是自动产生的**：仓位从空仓变成有仓的那一刻开始记，回到空仓的那一刻收尾
 * （见 README「交易记录：事件流，不是视频」与 internal/replay/trade.go）。用户不需要记得按任何开关，
 * 所以这个面板里也没有「开始/停止」这类按钮 —— 它只负责「看已经记下来的那些」。
 *
 * 列表和播放器放在同一个面板里，是因为它们共用一件事——「当前这一条记录」。
 * 拆成两个 Tab 的话，用户想在列表和播放之间来回换就要切两次，而回放的用法
 * 恰恰是「放一遍 → 回列表换一条 → 再放一遍」。
 *
 * 每一行先给**结论**（方向手数、盈亏、MFE/MAE、进出场价），再给过程（几步、几根、
 * 几条语音）。结论全部由后端算好（RecordingSummary.stats），前端只排版 ——
 * 「最大不利波动怎么算」这种事，只该有一份实现。
 *
 * 一步 = 一次操作，所以步进条上一条记录可能几百格。为此做了两件事：
 * **按类型上色**（下一根 / 画图 / 下单 / 语音）和**按 K 线分块**（换根时留一道间隙），
 * 否则一片同样的小方块根本看不出结构。
 */

const pb = usePlaybackStore()
const store = useReplayStore()

const loading = ref('')
const armed = ref('')
let armTimer = 0

const total = computed(() => pb.recording?.steps?.length ?? 0)
const current = computed(() => pb.step)
const stepAudio = computed(() => pb.clips.length)

/** 本步的语音清单（步进条下面那一行） */
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

/**
 * 盈亏配色遵循 A 股/国内习惯：**赚是红、亏是绿**（不是欧美那套反过来）。
 * 中性（0）用灰，免得把「打平」显示成赚钱。
 */
function pnlClass(n: number): string {
  if (n > 0.0001) return 'text-red-600 dark:text-red-400'
  if (n < -0.0001) return 'text-emerald-600 dark:text-emerald-400'
  return 'text-dim'
}

function pnlText(n: number): string {
  return `${n >= 0 ? '+' : ''}${n.toFixed(2)}`
}

/** 方向 + 手数：`多 2` / `空 1` / `—`（没有成交的记录不会出现，兜一层而已）。 */
function dirText(s: TradeStats): string {
  if (s.side === 'long') return `多 ${s.quantity}`
  if (s.side === 'short') return `空 ${s.quantity}`
  return '—'
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
  // 第二下才真删：记录删了没有回收站
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

// 记录内容到了 → 交给播放器（请求是列表发出的，播放器只管放）
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
        <span>已保存 {{ store.recordings.length }} 笔交易记录</span>
        <span class="min-w-0 flex-1 truncate" :title="store.recordingDir">
          目录 {{ store.recordingDir || '（未配置）' }}
        </span>
        <button type="button" class="hover:text-accent" @click="store.listRecordings()">刷新</button>
      </div>

      <div class="min-h-0 flex-1 overflow-auto px-2 pb-1">
        <div v-if="store.recordings.length === 0" class="py-4 text-faint leading-6">
          还没有交易记录。<br />
          <b class="text-dim">开仓的那一刻自动开始记，仓位平掉的那一刻自动收尾</b> ——
          不需要点任何按钮，也不会有「正在录制」的提示。记下的是这一笔从进场到出场的全部操作
          （【下一根】、画图、挂单、语音），回放时一格一格演给你看。
        </div>
        <div
          v-for="r in store.recordings"
          :key="r.id"
          class="mb-1 rounded border border-line bg-elev px-2 py-1 hover:border-accent"
        >
          <div class="flex items-center gap-2">
            <button type="button" class="min-w-0 flex-1 text-left" @click="pick(r)">
              <div class="flex items-center gap-2">
                <span class="truncate font-medium text-ink">{{ r.name }}</span>
                <span class="shrink-0 rounded bg-panel px-1 text-[10px] text-dim">
                  {{ r.stepCount }} 步 / {{ r.bars }} 根
                </span>
                <span
                  v-if="r.audioCount > 0"
                  class="shrink-0 rounded bg-panel px-1 text-[10px] text-amber-600 dark:text-amber-400"
                >
                  {{ r.audioCount }} 语音
                </span>
                <span
                  v-if="!r.stats.closed"
                  class="shrink-0 rounded bg-panel px-1 text-[10px] text-sky-600 dark:text-sky-300"
                  title="这一笔还没平仓：记录停在最后一次操作上（可能是实例被删、程序退出，或还在持仓中）"
                >
                  持仓中
                </span>
                <span v-if="loading === r.id" class="shrink-0 text-[10px] text-accent">载入中…</span>
              </div>
              <!--
                第二行是**结论**：方向手数、盈亏、最大不利/有利波动、进出场价。
                全部来自后端的 stats（口径只有一份实现），这里只排版。
              -->
              <div class="tnum mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 text-[10px]">
                <span class="text-dim">{{ dirText(r.stats) }}</span>
                <span :class="pnlClass(r.stats.realizedPnl)">
                  盈亏 {{ pnlText(r.stats.realizedPnl) }}
                </span>
                <span class="text-faint">
                  MFE <span :class="pnlClass(r.stats.mfe)">{{ pnlText(r.stats.mfe) }}</span> / MAE
                  <span :class="pnlClass(r.stats.mae)">{{ pnlText(r.stats.mae) }}</span>
                </span>
                <span class="text-faint">
                  入场 {{ r.stats.entryPrice || '—' }} → 出场 {{ r.stats.exitPrice || '—' }}
                </span>
                <span class="text-faint">持仓 {{ r.stats.barsHeld }} 根 · {{ r.stats.fills }} 次成交</span>
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
              :title="armed === r.id ? '再点一下确认删除（不可撤销）' : '删除这笔交易记录'"
              @click.stop="armDelete(r)"
            >
              {{ armed === r.id ? '确认删除' : '删除' }}
            </button>
          </div>
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
        <span class="shrink-0 rounded bg-panel px-1 text-[10px] text-dim">
          {{ pb.currentKindLabel }}
        </span>
        <span class="tnum hidden shrink-0 text-faint sm:inline">第 {{ pb.barNo }}/{{ pb.recording?.bars }} 根 · {{ pb.currentStep?.time }}</span>
        <span
          v-if="stepAudio > 0"
          class="shrink-0 rounded bg-amber-500/15 px-1 text-[10px] text-amber-600 dark:text-amber-400"
        >
          本步 {{ stepAudio }} 条语音
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
        <label class="flex cursor-pointer items-center gap-1 text-[11px] text-dim" title="走到含语音的步骤时自动顺序播放语音">
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

      <!-- 本步的语音：波形 + 时长，点一条就播（步进条上那个橙点只是"有语音"的记号，
           真要听哪一条得在这里点） -->
      <div
        v-if="clipsNow.length"
        class="flex items-center gap-1 overflow-x-auto border-t border-line px-2 py-1"
      >
        <span class="shrink-0 text-[10px] text-faint">本步语音</span>
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
        <div class="mt-1 flex items-center gap-2 text-[10px] text-faint">
          <span class="flex items-center gap-1"><i class="h-2 w-2 rounded-sm border border-sky-500/80 bg-sky-500/40" />下一根</span>
          <span class="flex items-center gap-1"><i class="h-2 w-2 rounded-sm border border-emerald-500/80 bg-emerald-500/35" />画图</span>
          <span class="flex items-center gap-1"><i class="h-2 w-2 rounded-sm border border-violet-500/80 bg-violet-500/35" />下单/成交</span>
          <span class="flex items-center gap-1"><i class="h-2 w-2 rounded-sm border border-amber-500 bg-amber-500/50" />语音</span>
          <span class="flex items-center gap-1"><i class="h-2 w-2 rounded-sm border border-slate-400/70 bg-slate-400/20" />入场前</span>
          <span class="ml-auto">一格 = 一次操作，换 K 线处留空</span>
        </div>
      </div>
    </template>
  </div>
</template>
