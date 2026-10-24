<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { CSSProperties } from 'vue'

import { hasTag, tagTextColor, withTag, withoutTag } from '@/lib/records'
import type { TagUsage } from '@/lib/records'
import type { RecordTag, RecordingSummary } from '@/types/protocol'

/**
 * 一笔交易的标签：已打的标签 + 一个「+ 标签」入口。
 *
 * 面板里做两件事：
 *
 *  1. **点已有标签** —— 直接把那个标签（连同它原来的颜色和用量）打到这一笔上。
 *     这是复盘时最高频的动作：想记的是「又追单了」，不是「再挑一次颜色」。
 *  2. **新建标签** —— 名字自己写、颜色自己挑，写完立刻可用。
 *
 * 面板用 `fixed` 定位（而不是 relative 的绝对定位）：列表外面套着
 * `overflow-x-auto`，绝对定位会被裁掉下半截 —— 表现成「点了加号，面板出来一半」。
 * 因此外面的滚动/缩放会先把面板关掉，宁可多点一下，也不留一个飘在原地的浮层。
 *
 * 组件本身**不落盘**：它只 emit 一组新标签，由页面发给服务端（标签存在记录目录里，
 * 前端不做乐观更新 —— 见 stores/replay.ts 的 setRecordingTags）。
 */
const props = defineProps<{
  rec: RecordingSummary
  /** 全部记录里出现过的标签（带用量），面板上半部分就是它 */
  library: TagUsage[]
}>()

const emit = defineEmits<{ change: [RecordTag[]] }>()

/** 新建标签时的快捷色：都是白底上读得清的中间调 */
const PALETTE = [
  '#ef4444',
  '#f97316',
  '#f59e0b',
  '#22c55e',
  '#0ea5e9',
  '#6366f1',
  '#a855f7',
  '#ec4899',
  '#64748b',
] as const

const open = ref(false)
const trigger = ref<HTMLButtonElement | null>(null)
const panel = ref<HTMLElement | null>(null)
const panelStyle = ref<CSSProperties>({})
const name = ref('')
const color = ref<string>(PALETTE[0])

/** 后端的上限（maxTagsPerRecord / maxTagNameRunes），这里跟着显示、跟着挡 */
const MAX_TAGS = 8
const MAX_NAME = 12

const tags = (): RecordTag[] => props.rec.tags ?? []
const isFull = (): boolean => tags().length >= MAX_TAGS

function toggle(): void {
  if (open.value) {
    open.value = false
    return
  }
  const r = trigger.value?.getBoundingClientRect()
  if (!r) return
  const width = 292
  // 贴着触发按钮的右下角展开；超出视口就翻到上面 / 往回挪一点
  const left = Math.max(8, Math.min(r.right - width, window.innerWidth - width - 8))
  const below = window.innerHeight - r.bottom
  const top = below > 300 ? r.bottom + 6 : Math.max(8, r.top - 306)
  panelStyle.value = { left: `${left}px`, top: `${top}px`, width: `${width}px` }
  open.value = true
  void nextTick(() => {
    panel.value?.querySelector('input')?.focus()
  })
}

function close(): void {
  open.value = false
}

function remove(tag: RecordTag): void {
  emit('change', withoutTag(tags(), tag.name))
}

/** 点已有标签：没有就加上（用它自己的颜色），有就去掉 —— 一个开关。 */
function toggleLibrary(u: TagUsage): void {
  if (hasTag(props.rec, u.name)) {
    emit('change', withoutTag(tags(), u.name))
  } else if (!isFull()) {
    emit('change', withTag(tags(), { name: u.name, color: u.color }))
  }
}

function add(): void {
  const n = name.value.trim()
  if (!n) return
  if (!hasTag(props.rec, n) && isFull()) return
  emit('change', withTag(tags(), { name: n, color: color.value }))
  name.value = ''
}

function onOutside(e: MouseEvent): void {
  const t = e.target as Node | null
  if (!t) return
  if (panel.value?.contains(t) || trigger.value?.contains(t)) return
  close()
}

/** 浮层是 fixed 的：外面一滚它就不贴着按钮了，直接关掉最省事也最不容易出错。 */
function onScroll(): void {
  close()
}

watch(open, (v) => {
  if (v) {
    document.addEventListener('mousedown', onOutside)
    window.addEventListener('scroll', onScroll, true)
    window.addEventListener('resize', onScroll)
  } else {
    document.removeEventListener('mousedown', onOutside)
    window.removeEventListener('scroll', onScroll, true)
    window.removeEventListener('resize', onScroll)
  }
})

onBeforeUnmount(() => {
  document.removeEventListener('mousedown', onOutside)
  window.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('resize', onScroll)
})
</script>

<template>
  <div class="relative flex flex-wrap items-center gap-1" @click.stop>
    <span
      v-for="tag in tags()"
      :key="tag.name"
      class="inline-flex max-w-full items-center gap-1 rounded-full border px-1.5 py-px text-[11px] leading-4"
      :style="{
        color: tagTextColor(tag.color),
        backgroundColor: `${tag.color}14`,
        borderColor: `${tag.color}40`,
      }"
      :title="`${tag.name}（点右侧 × 移除）`"
    >
      <span class="h-1.5 w-1.5 shrink-0 rounded-full" :style="{ backgroundColor: tag.color }" />
      <span class="truncate">{{ tag.name }}</span>
      <button
        type="button"
        class="shrink-0 opacity-50 transition hover:opacity-100"
        title="移除这个标签"
        @click.stop="remove(tag)"
      >
        ×
      </button>
    </span>

    <button
      ref="trigger"
      type="button"
      class="rounded-full border border-dashed border-line px-1.5 py-px text-[11px] leading-4 text-faint transition hover:border-accent hover:text-accent"
      :title="isFull() ? `一笔交易最多 ${MAX_TAGS} 个标签` : '给这一笔打标签'"
      @click.stop="toggle"
    >
      + 标签
    </button>

    <Teleport to="body">
      <div
        v-if="open"
        ref="panel"
        class="fixed z-50 rounded-xl border border-line bg-panel p-2.5 shadow-xl"
        :style="panelStyle"
        @click.stop
      >
        <div class="flex items-baseline gap-2">
          <h3 class="text-[12px] font-medium text-ink">已有标签</h3>
          <span class="text-[11px] text-faint">点一下打到这一笔上</span>
          <button
            type="button"
            class="ml-auto text-[11px] text-faint transition hover:text-ink"
            @click="close"
          >
            关闭
          </button>
        </div>

        <div v-if="library.length" class="mt-1.5 flex flex-wrap gap-1">
          <button
            v-for="u in library"
            :key="u.name"
            type="button"
            class="inline-flex items-center gap-1 rounded-full border px-1.5 py-0.5 text-[11px] transition"
            :class="
              hasTag(rec, u.name)
                ? 'border-ink/25 bg-ink/5 font-medium'
                : 'border-line hover:border-accent'
            "
            :style="{ color: tagTextColor(u.color) }"
            :title="hasTag(rec, u.name) ? '再点一下移除' : `已用过 ${u.count} 次`"
            @click="toggleLibrary(u)"
          >
            <span class="h-1.5 w-1.5 rounded-full" :style="{ backgroundColor: u.color }" />
            {{ u.name }}
            <span class="tnum text-faint">{{ u.count }}</span>
          </button>
        </div>
        <p v-else class="mt-1.5 text-[11px] text-faint">还没有用过标签，在下面写一个。</p>

        <div class="mt-2.5 border-t border-line pt-2">
          <div class="flex items-center gap-1.5">
            <input
              v-model="name"
              type="text"
              :maxlength="MAX_NAME"
              placeholder="新标签，如：追单 / 止损太早"
              class="min-w-0 flex-1 rounded border border-line bg-elev px-1.5 py-1 text-[12px] text-ink outline-none focus:border-accent"
              @keydown.enter.prevent="add"
            />
            <button
              type="button"
              class="shrink-0 rounded border border-line px-2 py-1 text-[12px] text-dim transition hover:border-accent hover:text-accent disabled:opacity-40"
              :disabled="!name.trim() || (!hasTag(rec, name) && isFull())"
              @click="add"
            >
              添加
            </button>
          </div>
          <div class="mt-1.5 flex flex-wrap items-center gap-1">
            <button
              v-for="c in PALETTE"
              :key="c"
              type="button"
              class="h-4 w-4 rounded-full border-2 transition"
              :class="color === c ? 'border-ink' : 'border-transparent hover:border-line'"
              :style="{ backgroundColor: c }"
              :title="`用这个颜色：${c}`"
              @click="color = c"
            />
            <label class="ml-1 flex items-center gap-1 text-[11px] text-faint">
              自定义
              <input
                v-model="color"
                type="color"
                class="h-5 w-6 cursor-pointer rounded border border-line bg-transparent p-0"
              />
            </label>
          </div>
          <p class="mt-1 text-[10px] text-faint">
            {{ tags().length }} / {{ MAX_TAGS }} 个标签 · 名字最多 {{ MAX_NAME }} 字 · 存进记录目录，重启还在
          </p>
        </div>
      </div>
    </Teleport>
  </div>
</template>
