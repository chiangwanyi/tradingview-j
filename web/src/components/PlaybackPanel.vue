<script setup lang="ts">
import { computed } from 'vue'
import { RouterLink } from 'vue-router'

import AudioWave from '@/components/AudioWave.vue'
import { usePlaybackStore } from '@/stores/playback'
import { stepKindClass, stepKindLabel } from '@/stores/step-kinds'

/**
 * 底部【回放】Tab 的内容：**只放播放器**。
 *
 * v0.19.0 之前这里同时是「交易记录列表」：左边列记录、点一条就地回放。
 * 列表搬去 `/records` 之后，这个面板就只剩播放控制 —— 因为回放必须发生在画布上，
 * 而列表要的是占满一屏的统计和图表，两件事本来就该待在不同页面。
 *
 * 没在回放时它不是空白：给一个明确的出口（去记录页挑一条），
 * 否则用户会以为「回放坏了」。
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
  'rounded border border-line px-2 py-0.5 text-[12px] transition disabled:cursor-not-allowed disabled:opacity-40'
</script>

<template>
  <div class="flex h-full flex-col text-[12px]">
    <!-- ---------------- 没有在回放 ---------------- -->
    <div
      v-if="!pb.active"
      class="flex h-full flex-col items-center justify-center gap-2 text-center"
    >
      <p class="text-dim">现在没有在回放。</p>
      <p class="text-[11px] text-faint">
        回放的内容是<b class="text-dim">已经记下来的交易记录</b>
        —— 一笔从开仓到平仓的全部操作，一格一格演在画布上。
      </p>
      <RouterLink
        :to="{ name: 'records' }"
        class="rounded border border-line px-3 py-1 text-[12px] text-accent transition hover:border-accent hover:bg-accent-soft"
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
        <span class="shrink-0 rounded bg-panel px-1 text-[10px] text-dim">
          {{ pb.currentKindLabel }}
        </span>
        <span class="tnum hidden shrink-0 text-faint sm:inline">
          第 {{ pb.barNo }}/{{ pb.recording?.bars }} 根 · {{ pb.currentStep?.time }}
        </span>
        <span
          v-if="stepAudio > 0"
          class="shrink-0 rounded bg-amber-500/15 px-1 text-[10px] text-amber-600 dark:text-amber-400"
        >
          本步 {{ stepAudio }} 条语音
        </span>
        <span class="ml-auto flex shrink-0 items-center gap-2 text-[10px]">
          <RouterLink :to="{ name: 'records' }" class="text-faint hover:text-accent">
            全部记录与统计 →
          </RouterLink>
          <span :class="pb.audioPlaying ? 'text-amber-600 dark:text-amber-400' : 'text-faint'">
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
          class="rounded bg-emerald-600 px-3 py-0.5 text-[12px] font-medium text-white hover:bg-emerald-500"
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
          class="flex cursor-pointer items-center gap-1 text-[11px] text-dim"
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
