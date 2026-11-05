<script setup lang="ts">
import { computed } from 'vue'

import Icon from './Icon.vue'
import {
  badgeClass,
  equityTrack,
  finished,
  lossLine,
  profitTarget,
  shortSymbol,
  shortTime,
  stageBadge,
  targetRaised,
  tradingDaysElapsed,
} from '@/data/accounts'
import type { Account } from '@/data/accounts'
import { drawdownLabel, pct, usd } from '@/data/rules'

/**
 * 一张考核卡。版式（2026-11-03 调整后）：
 * 一行「模板 + 账户名」→ 右上角「标的短名 + 阶段标签（+ 成功/失败）」
 * → 小标签（回撤口径 / 已达标 / 最新K线 / 已进行几日 / 起始时间）→ 四个数字 → 进度条 → 动作。
 *
 * 左上角那格是**模板名**（`LucidFlex 50K EVAL` 这种），由调用方查好传进来（见 `tplName`）：
 * 它既回答「多大规模、什么规则」，也顺带说明了这个号处在哪一段（模板名里就带 EVAL / FUNDED）。
 * 标的退到右上角，而且只写代码（`MES1!`）—— 交易所前缀每次都一样，写全了只会挤掉阶段标签。
 *
 * 卡片上的入口只有三个：继续交易 / 详情 / 结算今日；外加一颗**只在考核通过时才出现**的
 * 【升级】—— 它是唯一一个「有结论才有动作」的按钮，没达标时整颗不画，
 * 而不是画出来再置灰（置灰的按钮会让人以为点得动、只是坏了）。改名、删除、阶段流水仍在详情页。
 *
 * ⚠️ 「通没通过」用 `a.passed`（后端现算的），**不在这里按 profitTarget / 盈利日自己推**：
 * 那是一份考核口径，推一遍就有了第二份，两份迟早不一致。
 *
 * ⚠️ 盈利目标一律用 `profitTarget(a)`（后端算好的**一致性抬升后**的值），
 * 不能用 `a.rules.profitTarget`：单日赚 2750、一致性 40% 时真实目标是 6875，
 * 而模板上写的是 3000 —— 拿模板的值会显示「还差 40 就过关」，那是错的。
 *
 * ⚠️ 标签的配色/字面只在 `data/accounts.ts` 的 `stageBadge` + `badgeClass` 里定义一份，
 * 详情页用的是同一份。这里只管「画在哪儿」：【成功】【失败】是终态标记，挂在阶段标签旁边。
 *
 * ⚠️ 字号（2026-11-01 两次调整，2026-11-03 归一）：现在**只允许用 `style.css` 里那六级刻度**
 * （`text-page` / `text-metric` / `text-title` / `text-metric-sm` / `text-section` `text-lead` /
 * `text-body` `text-label` / `text-note`），本卡片落到：名字 20 · 主数字 24 · 次数字 20 ·
 * 副行与按钮 15 · 标签与说明 13 · 刻度 12。别再写 `text-[15px]` 这种裸值 —— 之前 9 档字号
 * （9/11/12/13/14/15/20/24/26）混在一起，13 和 14 挨得太近，层级就看不出来了。
 * 上下也都别再往「整体×2」那条路走：3 列栅格放不下，只会换来满屏折行。
 * 数字用 `.num`（同族 + 等宽数字），**不要用 `.tnum`** —— 那是交易页换 monospace 对齐价格表用的。
 */
const props = defineProps<{
  a: Account
  /**
   * 当前这套模板的名字（如 `LucidFlex 50K EVAL`）。空串 = 查不到（模板被删 / 模板列表还没到）
   * —— 那一格整颗不画，不拿 id 或阶段名冒充。
   *
   * 为什么要外面传：名字在 `pf-templates` 里，账户行上只有 `templateId`。
   * 总览页本来就要拉一份模板列表给新建弹窗，顺手查一下比让后端往账户 JSON 里
   * 再投影一个 `templateName` 便宜，也不会和模板改名后的事实分叉。
   */
  tplName?: string
  busy?: boolean
  pending?: 'settle' | 'promote' | ''
}>()
const emit = defineEmits<{ detail: [id: string]; settle: [id: string]; promote: [id: string] }>()

const target = computed(() => profitTarget(props.a))
/** 轨道：[爆仓线 ─ 起点 ─ 目标]，三个点的位置都在 `equityTrack` 里算，模板只管画。 */
const track = computed(() => equityTrack(props.a))
/** 比例 → CSS 百分比（模板里用四次，写成函数省得每处都乘 100）。 */
const p = (ratio: number): string => `${(ratio * 100).toFixed(2)}%`
/** 起点贴边时（出金号的起点就在峰值上）标签会溢出卡片，按位置换锚点。 */
const startAnchor = computed(() =>
  track.value.mark > 0.8 ? '-translate-x-full' : track.value.mark < 0.2 ? '' : '-translate-x-1/2',
)
const gained = computed(() => props.a.balance - props.a.baseBalance)
/** 亏损线：口径和交易页一致（含冻结水位），见 `data/accounts.ts` 的 `lossLine`。 */
const floor = computed(() => lossLine(props.a))
/** 目标被一致性抬高了：副行要说明来源，否则「目标怎么突然变了」会变成一个疑问。 */
const raised = computed(() => targetRaised(props.a))

/** 阶段标签：「考试号 / 出金号 / 出金号（升级）」，字面和配色都在 `stageBadge` 里。 */
const badge = computed(() => stageBadge(props.a))
/** 走完了（PAYOUT）—— 阶段标签旁边加一颗橙金【成功】。 */
const success = computed(() => finished(props.a))
/** 爆仓判失败 —— 阶段标签旁边加一颗红【失败】。 */
const failed = computed(() => props.a.status === 'FAILED')

/**
 * 已进行几日（起点那根 → 盘面最新那根，按真实开盘的交易日数）。
 * `null` 表示算不出来，卡片上写「—」，不编数字。
 */
const days = computed(() => tradingDaysElapsed(props.a))

/**
 * 回撤口径小标签：**EOD 不再显示**（用户 2026-11-01 要求）——
 * 它不改变卡片上任何数字的读法，只是多一格字，留着只会挤掉「最新K线 / 起始时间」。
 */
const drawLabel = computed(() =>
  props.a.rules.drawdownType === 'TRAILING_EOD' ? '' : drawdownLabel(props.a.rules.drawdownType),
)

/** 考核通过、能升上去 —— 失败和 PAYOUT 的账户后端给的就是 false，不用在这儿再判一遍。 */
const canPromote = computed(() => props.a.passed === true && props.a.status === 'ACTIVE')
/** 下一阶段叫什么（按钮上说清楚升到哪儿，别只写「升级」）。 */
const nextStage = computed(() => (props.a.stage === 'EVAL' ? 'FUNDED' : 'PAYOUT'))

// ---- 继续交易 ----
/**
 * 开一个**新标签页**去交易（`/trade/:id`）。
 *
 * 为什么不跳转当前页：一人可能同时盯两三个账户，浏览器标签天然支持并排、各自记住
 * 缩放和手上的工具；关掉一个也不影响另一个。URL 里带账户 id，所以刷新、收藏、
 * 粘给别人都能回到同一个账户。
 */
function onTrade(): void {
  window.open(`/trade/${encodeURIComponent(props.a.id)}`, '_blank', 'noopener')
}
</script>

<template>
  <article class="flex flex-col rounded-xl border border-line bg-panel shadow-[0_1px_2px_rgba(15,23,42,0.04)]">
    <header class="flex items-start justify-between gap-2 px-4 pt-4">
      <!-- 模板 + 账户名同一行：模板决定这套考核的口径（规模 / 回撤 / 有没有目标），
           升级换了模板这一段字也会跟着变，所以它比标的更像「这个账户是谁」 -->
      <div class="flex min-w-0 items-center gap-1.5">
        <span
          v-if="tplName"
          class="min-w-0 truncate rounded border border-accent/30 bg-accent-soft px-1.5 py-0.5 text-label font-medium text-accent-ink"
          title="这套账户是从哪个模板开的；升级后会换成下一阶段那套模板"
        >
          {{ tplName }}
        </span>
        <h3 class="min-w-0 truncate text-title font-semibold text-ink">
          {{ a.name }}
        </h3>
      </div>

      <!-- 右上角：标的（短名）+ 阶段标签 + 终态标记（成功 / 失败） -->
      <div class="flex shrink-0 flex-wrap items-center justify-end gap-1">
        <span
          class="rounded border border-line px-1.5 py-0.5 text-label font-medium whitespace-nowrap text-dim uppercase"
          :title="`这个账户只做 ${a.symbolId ?? '一个标的'}，创建后不可切换`"
        >
          {{ shortSymbol(a.symbolId) || '未绑定标的' }}
        </span>
        <span
          class="rounded border px-1.5 py-0.5 text-label font-medium whitespace-nowrap"
          :class="badgeClass(badge.tone)"
          :title="badge.hint"
          >{{ badge.text }}</span
        >
        <span
          v-if="success"
          class="rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-label font-medium whitespace-nowrap text-amber-700"
          title="出金号已走完全程（PAYOUT），这笔考核结束了"
          >成功</span
        >
        <span
          v-if="failed"
          class="rounded border border-red-200 bg-red-50 px-1.5 py-0.5 text-label font-medium whitespace-nowrap text-red-700"
          title="撞了亏损线，本阶段已经判失败（终态，不能重开）"
          >失败</span
        >
      </div>
    </header>

    <!-- 小标签：回撤口径（EOD 不显示）/ 已达标 / 最新K线 / 已进行几日 / 起始时间 -->
    <div class="mt-2.5 flex flex-wrap gap-1 px-4">
      <span
        v-if="drawLabel"
        class="rounded border border-line px-1.5 py-0.5 text-label text-faint uppercase"
      >
        {{ drawLabel }}
      </span>
      <span
        v-if="canPromote"
        class="rounded border border-amber-300 bg-amber-50 px-1.5 py-0.5 text-label font-medium text-amber-800"
      >
        已达标 · 待升级
      </span>
      <!-- 盘面现在停在哪根（原「创建于」那格，2026-11-03 改：阶段起始时间不如下面那根 K 线有用） -->
      <span
        class="rounded border border-line px-1.5 py-0.5 text-label text-faint"
        title="最新的那根 K 线（盘面停在这儿）"
      >
        最新K线 <span class="num">{{ shortTime(a.lastBarTime) }}</span>
      </span>
      <!-- 已经走了几个交易日：起点那根 → 盘面最新那根，由后端按数据目录里真实开盘日数出来（休市日不算） -->
      <span
        class="rounded border border-line px-1.5 py-0.5 text-label text-faint"
        title="从账户盘面起点到最新那根，中间真实开盘的交易日数（休市日不算）"
      >
        已进行 <span class="num font-medium text-dim">{{ days == null ? '—' : days }}</span> 日
      </span>
      <!-- 起始时间：账户盘面的起点那根（永不改动），和「最新K线」并排看才知道这段考核从哪儿开始 -->
      <span
        class="rounded border border-line px-1.5 py-0.5 text-label text-faint"
        title="这个账户的盘面从哪一根开始（建户时抽中，永不改动）"
      >
        起始时间 <span class="num">{{ shortTime(a.startBarTime) }}</span>
      </span>
    </div>

    <div class="mt-3 grid grid-cols-2 gap-x-3 gap-y-3 border-t border-line px-4 py-3.5">
      <div>
        <!-- 失败账户标「强平后」：余额是按当根收盘价强平后的实收，判死用的是权益（下行那一格）。
             不标口径的话，「余额 49,350 > 回撤线 48,591 却失败了」会被看成状态错了。 -->
        <p
          class="text-label font-medium text-faint"
          :title="failed ? '按当根收盘价强平后的实收现金；判死用的是权益，不是这个数' : ''"
        >
          {{ failed ? '强平后余额' : '账户余额' }}
        </p>
        <p class="num text-metric font-semibold text-ink">{{ usd(a.balance) }}</p>
        <p class="num mt-0.5 flex flex-wrap items-baseline gap-x-2 text-body text-faint">
          <span class="whitespace-nowrap">起点 {{ usd(a.baseBalance) }}</span>
          <span class="whitespace-nowrap" :class="gained >= 0 ? 'text-accent-ink' : 'text-red-600'">
            {{ gained >= 0 ? '+' : '' }}{{ usd(gained) }}
          </span>
        </p>
      </div>
      <div>
        <p class="text-label font-medium text-faint">盈利目标</p>
        <p class="num text-metric font-semibold text-ink">
          {{ usd(target) }}
        </p>
        <p class="num text-body text-faint">
          {{ target == null ? '本阶段无目标' : `还差 ${usd(Math.max(0, target - gained))}` }}
        </p>
        <!-- 一致性抬升：目标 > 模板上的那个数时说明原因，否则会被当成 bug -->
        <p v-if="raised" class="num text-body text-accent-ink" title="单日最大净盈利 ÷ 一致性比例">
          一致性抬升 · 单日最大 {{ usd(a.maxDayNetBalance) }}
        </p>
      </div>
      <div>
        <!-- 主数字换成了「回撤线」（跌破就判失败的那条），最大回撤退成灰色小字说明它怎么来的 -->
        <p class="text-label font-medium text-faint">回撤线</p>
        <p class="num text-metric-sm font-medium text-ink">{{ usd(floor) }}</p>
        <p class="num text-body text-faint" title="回撤线 = 日终峰值（到过冻结水位就按水位）− 最大回撤">
          最大回撤 {{ usd(a.rules.maxDrawdown) }}
        </p>
        <!-- 失败卡补一行「权益」：判死的是权益（持仓按盘中最差价计浮亏），不是上面的余额 ——
             两个数并排而不说明口径，卡片就会看起来「状态不对」（碧空蒲团 2026-11-05）。
             数字来自后端重放（`a.equity`，同交易页 PfView.equity），缺了就不画，不拿余额冒充。 -->
        <p
          v-if="failed && a.equity != null"
          class="num text-body"
          :class="floor != null && a.equity < floor ? 'text-red-600' : 'text-faint'"
          title="权益 = 余额 + 持仓最不利浮盈亏（多单按 low、空单按 high）；权益 ≤ 回撤线即判失败。余额是强平后的实收，两者分叉不是 bug"
        >
          权益 {{ usd(a.equity)
          }}<span v-if="floor != null && a.equity < floor" class="whitespace-nowrap">
            （触线 {{ usd(floor - a.equity) }}）</span
          >
        </p>
      </div>
      <div>
        <p class="text-label font-medium text-faint">一致性上限</p>
        <p class="num text-metric-sm font-medium text-ink">
          {{ a.rules.consistencyRatio == null ? '—' : `≤${pct(a.rules.consistencyRatio)}` }}
        </p>
        <p class="num text-body text-faint">
          {{ a.rules.minProfitDays == null ? '不查盈利日' : `盈利日 ${a.rules.minProfitDays} 天` }}
        </p>
      </div>
    </div>

    <!-- 轨道：[爆仓线 ─ 起点 ─ 目标]。左端是**爆仓线**（日终峰值上移它就跟着上移），
         右端是目标，起点按 `baseBalance` 落在两者之间的真实位置（不是画在正中）。 -->
    <div class="px-4 pb-3">
      <div class="relative mt-4 h-1.5 w-full rounded-full bg-elev">
        <!-- 起点左侧 = 亏损区（红），右侧 = 到目标的盈利区（淡绿）。
             没有目标时轨道只是「爆仓线 → 峰值」，起点就在右端，这两段底色会变成一整条红 —— 不画。 -->
        <template v-if="target != null">
          <div class="absolute inset-y-0 left-0 rounded-full bg-down/25" :style="{ width: p(track.mark) }" />
          <div class="absolute inset-y-0 right-0 rounded-full bg-accent/15" :style="{ left: p(track.mark) }" />
        </template>
        <!-- 余额实到的那一段：赚钱在起点右侧（绿），亏钱在起点左侧（红） -->
        <div
          v-if="track.fill >= track.mark"
          class="absolute inset-y-0 rounded-full bg-accent"
          :style="{ left: p(track.mark), width: p(track.fill - track.mark) }"
        />
        <div
          v-else
          class="absolute inset-y-0 rounded-full bg-down"
          :style="{ left: p(track.fill), width: p(track.mark - track.fill) }"
        />
        <!-- 起点线 -->
        <div class="absolute -inset-y-1 w-0.5 -translate-x-1/2 rounded-full bg-ink/40" :style="{ left: p(track.mark) }" />
        <span
          class="num absolute -top-4 text-note whitespace-nowrap text-faint"
          :class="startAnchor"
          :style="{ left: p(track.mark) }"
          title="本阶段起点余额（升级时重置回账户规模）"
          >起点 {{ usd(a.baseBalance) }}</span
        >
      </div>
      <div class="num mt-1 flex justify-between text-note text-faint">
        <span>
          爆仓线 <b class="font-medium text-down" :title="`日终峰值 − 最大回撤 ${usd(a.rules.maxDrawdown)}`">{{ usd(track.lo) }}</b>
        </span>
        <span>
          {{ target == null ? '日终峰值' : '目标' }}
          <b class="font-medium text-accent-ink">{{ usd(track.hi) }}</b>
        </span>
      </div>
    </div>

    <div class="mt-auto grid grid-cols-2 gap-1.5 px-4 pb-4">
      <button
        type="button"
        class="flex items-center justify-center gap-1 rounded-md bg-accent py-2 text-lead font-medium text-white transition-colors hover:bg-accent-ink"
        title="在新标签页打开这个账户的 K 线画布"
        @click="onTrade"
      >
        <Icon name="right" :size="15" />继续交易
      </button>
      <button
        type="button"
        class="rounded-md border border-line py-2 text-lead text-dim transition-colors hover:border-line-strong hover:text-ink"
        @click="emit('detail', a.id)"
      >
        详情
      </button>
      <!-- 升级：**只在考核通过时出现**（`passed` 由后端重放算出来）。
           没通过时整颗不画 —— 画出来再置灰，人会以为「点得动、只是坏了」，
           而真实情况是「还没考完」。 -->
      <button
        v-if="canPromote"
        type="button"
        class="col-span-2 flex items-center justify-center gap-1.5 rounded-md bg-accent py-2 text-lead font-medium text-white transition-colors hover:bg-accent-ink disabled:opacity-50"
        title="余额会重置回下一阶段的账户规模（本阶段赚的钱不带过去）"
        :disabled="busy"
        @click="emit('promote', a.id)"
      >
        <Icon name="check" :size="15" />
        {{ pending === 'promote' ? '升级中…' : `考核通过 · 升级到 ${nextStage}` }}
      </button>
      <!-- 只在考核中给：失败的账户没有「今天的账」可结 -->
      <button
        v-if="a.status === 'ACTIVE'"
        type="button"
        class="col-span-2 rounded-md border border-line py-2 text-lead text-dim transition-colors hover:border-line-strong hover:text-ink disabled:opacity-50"
        title="一步推到下一个交易日，结算上一个交易日（日终峰值 → 回撤线上移）；有持仓或挂单时不让用"
        :disabled="busy"
        @click="emit('settle', a.id)"
      >
        {{ pending === 'settle' ? '结算中…' : '结算今日' }}
      </button>
    </div>
  </article>
</template>
