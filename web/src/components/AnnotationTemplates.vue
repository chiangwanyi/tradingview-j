<script setup lang="ts">
import { computed, ref } from 'vue'

import { defaultLevels, resolveStyle } from '@/components/chart/annotations'
import { TOOL_LABEL, useReplayStore, type DrawTool } from '@/stores/replay'
import { ANNOTATION_COLORS, type AnnotationStyle, type DashStyle, type Template } from '@/types/protocol'

/**
 * 一个绘图工具的模板条 + 编辑器。
 *
 * 三种交互：
 *   - 点模板（chip）= 换用这条模板，下一条线就按它的样式画（**已经画好的线不变**）；
 *   - ＋ = 复制当前模板另建一条；
 *   - 改 / 删 = 改当前这条。
 *
 * 模板存在后端（`replay-templates.json`），换实例、重启、换浏览器都还在；
 * 这里只管编辑，保存后等后端把整份列表广播回来 —— 不在前端做乐观更新，
 * 免得两份状态不一致（列表是唯一的真相）。
 */

const props = defineProps<{ kind: DrawTool }>()
const store = useReplayStore()

const list = computed(() => store.templatesFor(props.kind))
const active = computed(() => store.templateFor(props.kind))
const activeId = computed(() => active.value?.id ?? '')

interface Draft {
  id?: string
  name: string
  style: AnnotationStyle
}

const editing = ref<Draft | null>(null)

function clone(style: AnnotationStyle): AnnotationStyle {
  return { ...style, levels: (style.levels ?? []).map((lv) => ({ ...lv })) }
}

/** 当前模板的样式（模板缺失时用出厂样式） */
function activeStyle(): AnnotationStyle {
  const base = resolveStyle({ kind: props.kind, style: active.value?.style })
  return clone(base)
}

function openNew(): void {
  editing.value = { name: `${TOOL_LABEL[props.kind]} ${list.value.length + 1}`, style: activeStyle() }
}

function openEdit(): void {
  const t = active.value
  if (!t) return
  editing.value = { id: t.id, name: t.name, style: clone(resolveStyle({ kind: props.kind, style: t.style })) }
}

function close(): void {
  editing.value = null
}

function save(asNew: boolean): void {
  const d = editing.value
  if (!d) return
  const style = resolveStyle({ kind: props.kind, style: d.style })
  store.saveTemplate({
    id: asNew ? undefined : d.id,
    kind: props.kind,
    name: d.name.trim() || TOOL_LABEL[props.kind],
    style,
  })
  editing.value = null
}

function remove(): void {
  const t = active.value
  if (!t) return
  if (!window.confirm(`删除模板「${t.name}」？已经画在图上的图形不受影响。`)) return
  store.deleteTemplate(t.id)
}

/** 把模板的样式说成一句人话，放在 chip 的 tooltip 里 */
function describe(t: Template): string {
  const s = resolveStyle({ kind: props.kind, style: t.style })
  const parts = [`${TOOL_LABEL[props.kind]}模板 ${t.name}`]
  if (props.kind === 'fib') {
    parts.push('四条水平线各自配色')
  } else {
    parts.push(`线宽 ${s.width}px`, s.dash === 'dashed' ? '虚线' : '实线')
    if (props.kind === 'rect') parts.push('带填充')
  }
  return parts.join(' · ')
}

/** fib 模板的色块：四条水平的颜色；其他工具就是一条（矩形再加一层填充） */
function swatchColors(t: Template): string[] {
  const s = resolveStyle({ kind: props.kind, style: t.style })
  if (props.kind === 'fib') return (s.levels ?? defaultLevels()).map((lv) => lv.color)
  return [s.color]
}

/** 编辑器里改某一条斐波那契水平的颜色 */
function setLevelColor(index: number, color: string): void {
  const d = editing.value
  if (!d) return
  const levels = d.style.levels ?? defaultLevels()
  d.style.levels = levels.map((lv, i) => (i === index ? { ...lv, color } : lv))
}

const widths = [1, 1.5, 2, 3, 4, 6]
const dashes: { key: DashStyle; label: string }[] = [
  { key: 'solid', label: '实线' },
  { key: 'dashed', label: '虚线' },
]

const btn = 'rounded border border-line px-2 py-1 text-[13px] transition disabled:cursor-not-allowed disabled:opacity-40'
const btnIdle = 'bg-panel text-dim hover:border-accent hover:text-accent'
const field = 'rounded border border-line bg-surface px-1.5 py-1 text-[13px] text-ink'

const inputClass = `${field} w-full`
</script>

<template>
  <div class="flex flex-wrap items-center gap-1.5">
    <span class="text-[12px] text-faint">模板</span>

    <button
      v-for="t in list"
      :key="t.id"
      type="button"
      :title="describe(t)"
      class="flex items-center gap-1.5 rounded border px-1.5 py-0.5 text-[12px] transition"
      :class="t.id === activeId ? 'border-accent bg-accent-soft text-accent' : 'border-line bg-panel text-dim hover:border-accent'"
      @click="store.selectTemplate(props.kind, t.id)"
    >
      <svg width="16" height="10" viewBox="0 0 16 10" class="shrink-0">
        <template v-if="props.kind === 'fib'">
          <line
            v-for="(c, i) in swatchColors(t)"
            :key="i"
            x1="0"
            x2="16"
            :y1="1.5 + i * 2.4"
            :y2="1.5 + i * 2.4"
            :stroke="c"
            stroke-width="1.4"
          />
        </template>
        <template v-else-if="props.kind === 'rect'">
          <rect
            x="1"
            y="1"
            width="14"
            height="8"
            :stroke="swatchColors(t)[0]"
            :fill="resolveStyle({ kind: props.kind, style: t.style }).fill"
            fill-opacity="0.35"
            stroke-width="1.2"
          />
        </template>
        <template v-else>
          <line
            x1="0"
            y1="5"
            x2="16"
            y2="5"
            :stroke="swatchColors(t)[0]"
            stroke-width="2"
            :stroke-dasharray="resolveStyle({ kind: props.kind, style: t.style }).dash === 'dashed' ? '5 3' : ''"
          />
        </template>
      </svg>
      {{ t.name }}
    </button>

    <span v-if="!list.length" class="text-[12px] text-faint">没有模板，用出厂样式</span>

    <button type="button" :class="[btn, btnIdle, 'px-1.5']" title="按当前样式另建一条模板" @click="openNew">
      ＋
    </button>
    <button type="button" :class="[btn, btnIdle, 'px-1.5']" :disabled="!active" title="修改当前模板" @click="openEdit">
      改
    </button>
    <button
      type="button"
      :class="[btn, btnIdle, 'px-1.5']"
      :disabled="!active"
      title="删除当前模板（已画的图形不受影响）"
      @click="remove"
    >
      删
    </button>

    <!--
      编辑器：固定在屏幕中央而不是贴在画布上 —— 画布面积宝贵，而且弹窗要能盖住
      底部交易栏（那里也可能有焦点）。
    -->
    <div
      v-if="editing"
      class="fixed inset-0 z-50 flex items-center justify-center bg-black/40"
      @pointerdown.self="close"
    >
      <div class="w-[380px] rounded border border-line bg-panel p-3 text-[13px] text-ink shadow-2xl">
        <div class="mb-2 flex items-baseline justify-between">
          <b class="text-[14px]">{{ editing.id ? '修改模板' : '新建模板' }}</b>
          <span class="text-[12px] text-faint">{{ TOOL_LABEL[props.kind] }}</span>
        </div>

        <label class="mb-2 flex items-center gap-2">
          <span class="w-12 shrink-0 text-dim">名称</span>
          <input v-model="editing.name" :class="inputClass" maxlength="24" />
        </label>

        <!-- 线类 + 矩形：线条颜色 -->
        <div v-if="props.kind !== 'fib'" class="mb-2 flex items-center gap-2">
          <span class="w-12 shrink-0 text-dim">{{ props.kind === 'rect' ? '边框色' : '线条色' }}</span>
          <input
            type="color"
            class="h-6 w-8 rounded border border-line bg-transparent"
            :value="editing.style.color"
            @input="editing.style.color = ($event.target as HTMLInputElement).value"
          />
          <span class="tnum text-[12px] text-faint">{{ editing.style.color }}</span>
        </div>

        <div class="mb-2 flex items-center gap-2">
          <span class="w-12 shrink-0 text-dim">线宽</span>
          <button
            v-for="w in widths"
            :key="w"
            type="button"
            class="rounded border px-1.5 py-0.5 text-[12px] tnum"
            :class="editing.style.width === w ? 'border-accent text-accent' : 'border-line text-dim hover:border-accent'"
            @click="editing.style.width = w"
          >
            {{ w }}
          </button>
        </div>

        <div class="mb-2 flex items-center gap-2">
          <span class="w-12 shrink-0 text-dim">线型</span>
          <button
            v-for="d in dashes"
            :key="d.key"
            type="button"
            class="rounded border px-2 py-0.5 text-[12px]"
            :class="editing.style.dash === d.key ? 'border-accent text-accent' : 'border-line text-dim hover:border-accent'"
            @click="editing.style.dash = d.key"
          >
            {{ d.label }}
          </button>
        </div>

        <!-- 矩形：填充色 + 不透明度 -->
        <template v-if="props.kind === 'rect'">
          <div class="mb-2 flex items-center gap-2">
            <span class="w-12 shrink-0 text-dim">填充色</span>
            <input
              type="color"
              class="h-6 w-8 rounded border border-line bg-transparent"
              :value="editing.style.fill"
              @input="editing.style.fill = ($event.target as HTMLInputElement).value"
            />
            <span class="tnum text-[12px] text-faint">{{ editing.style.fill }}</span>
          </div>
          <label class="mb-2 flex items-center gap-2">
            <span class="w-12 shrink-0 text-dim">填充度</span>
            <input
              v-model.number="editing.style.fillOpacity"
              type="range"
              min="0.02"
              max="0.6"
              step="0.02"
              class="flex-1"
            />
            <span class="tnum w-8 text-right text-[12px] text-faint">
              {{ Math.round((editing.style.fillOpacity ?? 0.12) * 100) }}%
            </span>
          </label>
        </template>

        <!-- 斐波那契：四条水平各一个颜色 -->
        <div v-if="props.kind === 'fib'" class="mb-2 space-y-1">
          <div class="text-dim">水平（比例固定，只能改颜色）</div>
          <div v-for="(lv, i) in editing.style.levels ?? defaultLevels()" :key="lv.ratio" class="flex items-center gap-2">
            <span class="tnum w-6 text-right text-faint">{{ lv.ratio }}</span>
            <input
              type="color"
              class="h-6 w-8 rounded border border-line bg-transparent"
              :value="lv.color"
              @input="setLevelColor(i, ($event.target as HTMLInputElement).value)"
            />
            <input
              class="rounded border border-line bg-transparent px-1 py-0.5 font-mono text-[12px] text-ink"
              :value="lv.color"
              maxlength="9"
              @input="setLevelColor(i, ($event.target as HTMLInputElement).value)"
            />
          </div>
        </div>

        <!-- 快捷色板：点一下就换（色值和上面那个 color input 是同一个字段）。
             斐波那契没有「一个颜色」，四条水平各自在上面调，所以这里不显示。 -->
        <div v-if="props.kind !== 'fib'" class="mb-3 flex items-center gap-1.5">
          <span class="w-12 shrink-0 text-faint">快捷色</span>
          <button
            v-for="c in ANNOTATION_COLORS"
            :key="c"
            type="button"
            class="h-4 w-4 rounded-full border"
            :class="editing.style.color === c ? 'border-ink' : 'border-line'"
            :style="{ backgroundColor: c }"
            :title="c"
            @click="editing.style.color = c"
          />
        </div>

        <div class="flex items-center gap-2">
          <button
            type="button"
            class="rounded bg-accent px-3 py-1 text-[13px] font-medium text-white hover:opacity-90"
            @click="save(false)"
          >
            保存
          </button>
          <button
            v-if="editing.id"
            type="button"
            :class="[btn, btnIdle]"
            title="保留原来的那一条，再存一份新的"
            @click="save(true)"
          >
            另存为
          </button>
          <button type="button" :class="[btn, btnIdle]" @click="close">取消</button>
        </div>
      </div>
    </div>
  </div>
</template>
