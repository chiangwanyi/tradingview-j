<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'

import { ClipRecorder, micSupported, toBase64 } from '@/audio/mic'
import { voiceStop } from '@/audio/player'
import { useTradeStore } from '@/stores/trade'

/**
 * 录音按钮（工具浮窗里那一枚）。
 *
 * 和 `web/` 的语音浮窗是同一套口语习惯：**按一下空格开始、再按一下结束**，
 * 开麦在**空格抬起之后**再等 90ms（躲开键帽回弹那一声）。区别只有一个 ——
 * 这里不弹浮窗、不选设备：录音本身只有「开始 / 结束」两个动作，窗口和下拉框
 * 是 v0.16 时代为了塞下试听清单才有的，而试听清单已经搬到横轴下方的录音条上了。
 *
 * 额度按**每根 K 线 9 条**数（后端数的），按钮上直接写「本根 x/9」；录满了按钮变灰，
 * 换到下一根就重新有 9 条。
 *
 * 录完的那一刻就发出去（`voice_add`）：挂在哪一根由**后端**按账户的 `lastBarTime` 决定，
 * 前端不猜 —— 猜错的代价是「语音挂到了下一根上」，而人听的时候才发现。
 */
const store = useTradeStore()

/** 抬起空格之后等这么久再开麦：键帽回弹那一声 < 30ms，这时候已经过去了 */
const START_DELAY_MS = 90

const capturing = ref(false)
const busy = ref(false)
/** 空格已经按下、还没抬起 */
const armed = ref(false)
/** 空格已抬起，正在等那 90ms */
const arming = ref(false)
/** 录制中已经过去了几秒（按钮上那个走动的秒数） */
const elapsed = ref(0)

let recorder: ClipRecorder | null = null
let startTimer = 0
let clockTimer = 0

const count = computed(() => store.voiceClips.length)
const max = computed(() => store.voiceMax)
const full = computed(() => store.voiceFull)
const canRecord = computed(() => store.socketState === 'open' && !!store.instance)

const title = computed(() => {
  if (!micSupported()) return '这个浏览器不支持录音（需要麦克风权限与 MediaRecorder）'
  if (!canRecord.value) return '和交易服务的连接还没通：连上之后才能录音'
  if (capturing.value) return '再按一下空格（或点这里）结束录音，讲完就会挂到当前这根 K 线上'
  if (full.value) return `这根 K 线已经录满 ${max.value} 条了：点【下一根】可以接着录`
  return `按一下空格开始录音，再按一下结束；每一根 K 线最多 ${max.value} 条`
})

const label = computed(() => {
  if (capturing.value) return `● 录音中 ${clock(elapsed.value)}`
  if (busy.value) return '编码中…'
  if (arming.value) return '准备开麦…'
  return '🎙 录音'
})

function clock(seconds: number): string {
  return `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, '0')}`
}

// ---------------- 录音 ----------------

async function startClip(): Promise<void> {
  if (capturing.value || busy.value || !canRecord.value) return
  if (full.value) {
    store.noteNotice(`这根 K 线最多 ${max.value} 条语音，已经录满了；点【下一根】可以接着录`)
    return
  }
  // 正在试听就别开录：外放的声音会被麦克风一起收进去
  voiceStop()
  recorder = new ClipRecorder()
  try {
    await recorder.start('')
    capturing.value = true
    startClock()
  } catch (e) {
    recorder = null
    const msg = String(e)
    store.noteError(
      /NotAllowed|Permission/i.test(msg)
        ? '麦克风权限被拒绝了：浏览器地址栏左侧的图标里允许麦克风，再按一次空格'
        : `开不了麦克风：${msg}`,
    )
  }
}

async function stopClip(): Promise<void> {
  if (!capturing.value || !recorder) return
  capturing.value = false
  busy.value = true
  stopClock()
  try {
    const out = await recorder.stop()
    recorder = null
    if (!out) {
      store.noteNotice('这段太短了（不到 0.25 秒），没有上传')
      return
    }
    if (!store.sendVoiceAudio(toBase64(out.wav), out.durationMs)) {
      store.noteError('连接断了，这段语音没发出去')
    }
  } catch (e) {
    store.noteError(`录音处理失败：${String(e)}`)
  } finally {
    busy.value = false
  }
}

function toggle(): void {
  if (capturing.value) void stopClip()
  else if (!busy.value) void startClip()
}

function startClock(): void {
  stopClock()
  elapsed.value = 0
  clockTimer = window.setInterval(() => {
    elapsed.value += 1
  }, 1000)
}

function stopClock(): void {
  if (clockTimer) window.clearInterval(clockTimer)
  clockTimer = 0
  elapsed.value = 0
}

// ---------------- 空格 ----------------

function typable(el: Element | null): boolean {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || (el as HTMLElement).isContentEditable === true
}

/** 空格按下：只记「已按下」，不碰麦克风。正在录的时候按下去 = 结束。 */
function onKeyDown(ev: KeyboardEvent): void {
  if (ev.code !== 'Space' || ev.repeat) return
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return
  if (typable(document.activeElement)) return
  if (!canRecord.value) return
  // 一定要吞掉：否则按钮会被空格「按」一下、页面还会往下滚一格
  ev.preventDefault()
  ev.stopPropagation()
  if (capturing.value) {
    void stopClip()
    return
  }
  if (busy.value) return
  if (arming.value) cancelPendingStart()
  armed.value = true
}

/** 空格抬起：这里才真正开始录（见 START_DELAY_MS）。 */
function onKeyUp(ev: KeyboardEvent): void {
  if (ev.code !== 'Space') return
  if (!armed.value) return
  armed.value = false
  if (!canRecord.value || capturing.value || busy.value) return
  ev.preventDefault()
  ev.stopPropagation()
  cancelPendingStart()
  arming.value = true
  startTimer = window.setTimeout(() => {
    startTimer = 0
    arming.value = false
    void startClip()
  }, START_DELAY_MS)
}

function cancelPendingStart(): void {
  if (startTimer) window.clearTimeout(startTimer)
  startTimer = 0
  arming.value = false
}

/** 切窗口时鼠标/键盘都可能丢事件：别让麦克风一直开着（指示灯亮着很吓人） */
function onBlur(): void {
  armed.value = false
  cancelPendingStart()
  if (capturing.value) void stopClip()
}

onMounted(() => {
  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('keyup', onKeyUp, true)
  window.addEventListener('blur', onBlur)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown, true)
  window.removeEventListener('keyup', onKeyUp, true)
  window.removeEventListener('blur', onBlur)
  cancelPendingStart()
  armed.value = false
  recorder?.cancel()
  recorder = null
  stopClock()
  // 关页面**不**停播放：播放器是全局的，录音条上也能停
})
</script>

<template>
  <div class="flex items-center gap-1">
    <button
      type="button"
      class="rounded border px-2 py-1 text-[13px] transition disabled:cursor-not-allowed disabled:opacity-40"
      :class="
        capturing
          ? 'border-red-400 bg-red-50 text-red-600'
          : 'border-line bg-panel text-dim hover:border-accent hover:text-accent'
      "
      :disabled="!canRecord || (full && !capturing)"
      :title="title"
      data-testid="voice-record"
      @click="toggle"
    >
      {{ label }}
    </button>
    <span
      class="tnum text-[11px]"
      :class="full ? 'text-amber-600' : 'text-faint'"
      :title="`本根 K 线已有 ${count} 条语音（上限 ${max}）；空白处按空格也能开始录音`"
      >{{ count }}/{{ max }}</span
    >
  </div>
</template>
