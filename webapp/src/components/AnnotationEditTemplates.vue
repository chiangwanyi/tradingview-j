<script setup lang="ts">
import { computed } from 'vue'

import { resolveStyle } from '@/components/chart/annotations'
import TemplateSwatch from '@/components/TemplateSwatch.vue'
import { DRAW_TOOLS, TOOL_LABEL, useTradeStore, type DrawTool } from '@/stores/trade'
import type { Annotation, AnnotationStyle, Template } from '@/types/protocol'

/**
 * 编辑图形时出现的**同类模板条**（v0.20.0）。
 *
 * 工具条上那条模板（AnnotationTemplates）管的是「下一条线用哪套样式」；
 * 这一条管的是**已经画在图上、现在正被选中的那一个**：点一下就把它的样式换过去。
 *
 * 三条刻意的约束：
 *
 *  1. **只列同类模板**。模板是按 kind 分组的（趋势线的预设套不到矩形上：它有填充和
 *     没有箭头），所以这里直接用图形自己的 kind 去 `templatesFor`，界面上根本看不到
 *     别的种类。后端 `restyleAnnotation` 还会拿图形自己的 kind 再 sanitize 一次 ——
 *     就算以后有别的入口绕过界面，也不会画出一个「长得不像自己」的图形。
 *  2. **不改端点**。换模板只换颜色 / 线宽 / 线型 / 填充，位置和方向一个都不动。
 *     所以它走的是一条专门的 `annotation_style`，而不是复用 `annotation_move`。
 *  3. **高亮「现在用的是哪一条」**。靠比较样式而不是记 id：图形上存的是样式快照
 *     （画下去那一刻从模板抄的），早就不记得自己来自哪条模板了。样式一样就说明
 *     它现在看起来就是这条模板的样子 —— 这才是用户想知道的事。
 */

const props = defineProps<{ annotation: Annotation }>()
const store = useTradeStore()

/** 图形自己的种类（未知种类不显示模板条：那不是能换样式的五种图形之一） */
const kind = computed<DrawTool | null>(() => {
  const k = props.annotation.kind
  return (DRAW_TOOLS as string[]).includes(k) ? (k as DrawTool) : null
})

const list = computed<Template[]>(() => (kind.value ? store.templatesFor(kind.value) : []))

/** 图形当前的样式（旧存档只有 color 时也补齐成一份完整样式） */
const current = computed<AnnotationStyle>(() =>
  resolveStyle({ kind: props.annotation.kind, style: props.annotation.style, color: props.annotation.color }),
)

/** 两份样式是否「看起来一样」：只比真正影响画面的字段 */
function sameStyle(a: AnnotationStyle, b: AnnotationStyle): boolean {
  if (a.color !== b.color || a.width !== b.width || a.dash !== b.dash) return false
  if ((a.fill ?? '') !== (b.fill ?? '')) return false
  if ((a.fillOpacity ?? 0) !== (b.fillOpacity ?? 0)) return false
  const la = a.levels ?? []
  const lb = b.levels ?? []
  if (la.length !== lb.length) return false
  return la.every((lv, i) => lv.ratio === lb[i].ratio && lv.color === lb[i].color)
}

/** 这条模板的样式 = 图形的样式（用来画高亮） */
function styleOf(t: Template): AnnotationStyle {
  return resolveStyle({ kind: props.annotation.kind, style: t.style })
}

function active(t: Template): boolean {
  return sameStyle(current.value, styleOf(t))
}

/**
 * 套用模板。
 *
 * 只读观众也能点吗？不能 —— 换样式改的是所有人看到的那个图形，和拖动一样要操作权。
 * 这里不 disable 按钮，而是点下去给一句提示：灰掉一整个条会让人以为「模板没了」。
 */
function apply(t: Template): void {
  if (!store.canOperate) {
    store.noteNotice('现在只能看：先「接管」操作权再换样式')
    return
  }
  store.restyleAnnotation(props.annotation.id, styleOf(t))
}

const title = computed(() => `编辑中的${TOOL_LABEL[kind.value ?? 'select']} ${props.annotation.id}`)
</script>

<template>
  <div
    v-if="kind"
    class="flex min-w-0 flex-wrap items-center gap-1.5 rounded border border-accent/50 bg-accent-soft px-1.5 py-0.5"
  >
    <span class="shrink-0 text-[12px] font-medium text-accent">{{ title }}</span>
    <span class="shrink-0 text-[12px] text-faint">模板</span>

    <button
      v-for="t in list"
      :key="t.id"
      type="button"
      :title="`把「${t.name}」的样式套到这个图形上（只换外观，不动位置）`"
      class="flex items-center gap-1.5 rounded border px-1.5 py-0.5 text-[12px] transition"
      :class="
        active(t)
          ? 'border-accent bg-panel text-accent'
          : 'border-line bg-panel text-dim hover:border-accent hover:text-accent'
      "
      @click="apply(t)"
    >
      <TemplateSwatch :kind="annotation.kind" :style="t.style" />
      {{ t.name }}
    </button>

    <span v-if="!list.length" class="text-[12px] text-faint">
      这个类型还没有模板 —— 先在绘图工具上建一条模板，再回来换
    </span>
  </div>
</template>
