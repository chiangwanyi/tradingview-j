<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { ClipRecorder, listMics, micSupported, requestMicPermission, toBase64, type MicDevice } from '@/audio/mic'
import {
  voiceCurrentFile,
  voiceError,
  voiceStop,
  voiceToggle,
} from '@/audio/player'
import { useReplayStore } from '@/stores/replay'
import AudioWave from '@/components/AudioWave.vue'
import type { AudioClip } from '@/types/protocol'

/**
 * 语音讲解浮窗（v0.16.0）—— **和「绘制图形」同级的一级功能**，不再挂在录制底下。
 *
 * 为什么要独立成一个组件：v0.11 – v0.15 里麦克风、波形、试听、清空全塞在录制浮窗里，
 * 而那个浮窗只在「录制中」时存在 —— 于是「不开录像就讲不了话」。
 * 现在这个窗在**任何**有实例的时候都能开（回放除外）。v0.18.0 起交易记录自动开始，
 * 持仓期间录的那条会被后端顺手在记录里占一步（回放到那一步语音响），空仓时录的
 * 只留在盘面上 —— 这件事前端不需要知道，也不需要多显示一个「录制中」。
 *
 * 交互沿用录制浮窗那一套（用户已经熟了，不必再学一遍）：
 *   - 空格**按一下开始、再按一下结束**；开麦在**抬起之后**（再等 90ms，躲开键盘回弹声）
 *   - 标题栏是把手（`pointerdown` 里 preventDefault，否则一按就框选文字、下拉框抢焦点）
 *   - 折叠成一枚 44px 的小图标，折叠后整枚都是把手；位置记在 localStorage
 *   - 默认落在下单浮窗右边一点，两个窗不打架
 *
 * 额度是**每根 K 线 9 条**（不是每次会话），所以这里显示「本根 x/9」，
 * 另外用小字标出整个实例一共几条。
 */

const store = useReplayStore()

const DEVICE_KEY = 'tv-replay-mic'
const POS_KEY = 'tv-voice-float-pos'

const PANEL_W = 268
const ICON = 44
const EST_H = 320
const MARGIN = 8
const DRAG_SLOP = 5
/** 抬起空格之后等这么久再开麦：键帽回弹那一声 < 30ms，这时候已经过去了 */
const START_DELAY_MS = 90

const mics = ref<MicDevice[]>([])
const deviceId = ref(readDevice())
const capturing = ref(false)
const busy = ref(false)
/** 空格已经按下、还没抬起 */
const armed = ref(false)
/** 空格已抬起，正在等那 90ms */
const arming = ref(false)
const micError = ref('')
const hint = ref('')
/**
 * 正在预览播放的那条（文件名）。
 *
 * v0.17.0 起这里**不再自己持有 Audio**：语音在语音浮窗、横轴下方的录音条两处都能点，
 * 各建一个播放器就会两个声音叠着响、两处高亮还各说各的。现在读的是全局单例
 * （audio/player.ts）的状态 —— 点录音条上的标记，这个窗里同样会高亮那一条。
 */
const playingFile = computed(() => voiceCurrentFile.value)
/** 「清空本根」的两段式确认 */
const armedClear = ref(false)
/** 「其他 K 线上的语音」是否展开（默认收起：它不是这一根的，不该抢位置） */
const showOthers = ref(false)

const expanded = ref(true)
const pos = ref(readSavedPos() ?? { x: 572, y: 112 })
const root = ref<HTMLElement | null>(null)

let recorder: ClipRecorder | null = null
let lastError = ''
let clearTimer = 0
let hintTimer = 0
let startTimer = 0

const clips = computed<AudioClip[]>(() => store.voiceClips)
const barAudios = computed(() => clips.value.length)
const maxAudios = computed(() => store.voiceMax)
const totalAudios = computed(() => store.voice.total)
const full = computed(() => store.voiceFull)
/** 当前这根 K 线的时间（浮窗里显示一行，确认「讲的是哪一根」） */
const barTime = computed(() => store.voice.barTime || store.currentBar?.time || '')

const canvas = ref<HTMLCanvasElement | null>(null)
let hist: number[] = []
const HIST_LEN = 150
let raf = 0

function readDevice(): string {
  try {
    return localStorage.getItem(DEVICE_KEY) ?? ''
  } catch {
    return ''
  }
}

function saveDevice(id: string): void {
  try {
    localStorage.setItem(DEVICE_KEY, id)
  } catch {
    // 记不住就算了
  }
}

async function refreshMics(ask = false): Promise<void> {
  if (!micSupported()) {
    micError.value = '这个浏览器不支持录音（需要麦克风权限与 MediaRecorder）'
    return
  }
  try {
    if (ask) await requestMicPermission()
    mics.value = await listMics()
    if (deviceId.value && !mics.value.some((m) => m.id === deviceId.value)) {
      deviceId.value = ''
      saveDevice('')
    }
  } catch (e) {
    micError.value = `读不到麦克风列表：${String(e)}`
  }
}

function chooseMic(ev: Event): void {
  const id = (ev.target as HTMLSelectElement).value
  deviceId.value = id
  saveDevice(id)
}

function typable(el: Element | null): boolean {
  if (!el) return false
  const tag = el.tagName
  return tag === 'INPUT' || tag === 'SELECT' || tag === 'TEXTAREA' || (el as HTMLElement).isContentEditable === true
}

/**
 * 贴一句提示，到点自己消失。
 *
 * 为什么要过期：一句话贴上去就再没人撤 —— 上一条错误会一路跟着浮窗活着，
 * 甚至跨到下一次录音上去。提示本来就应该只描述**刚才那一下**。
 */
function setHint(text: string, ms = 8000): void {
  hint.value = text
  if (hintTimer) window.clearTimeout(hintTimer)
  hintTimer = 0
  if (text && ms > 0) {
    hintTimer = window.setTimeout(() => {
      hint.value = ''
    }, ms)
  }
}

// ---------------- 录音 ----------------

async function startClip(): Promise<void> {
  if (capturing.value || busy.value || !store.canVoice) return
  if (full.value) {
    setHint(`这根 K 线最多 ${maxAudios.value} 条语音，已经录满了；点【下一根】可以接着录`)
    return
  }
  setHint('')
  // 预览还在响就别开录：外放的声音会被麦克风一起收进去
  voiceStop()
  recorder = new ClipRecorder()
  try {
    await recorder.start(deviceId.value)
    capturing.value = true
    hist = []
    nextTick(() => startWaveLoop())
  } catch (e) {
    recorder = null
    const msg = String(e)
    micError.value = /NotAllowed|Permission/i.test(msg)
      ? '麦克风权限被拒绝了：浏览器地址栏左侧的图标里允许麦克风'
      : `开不了麦克风：${msg}`
  }
}

async function stopClip(): Promise<void> {
  if (!capturing.value || !recorder) return
  capturing.value = false
  busy.value = true
  stopWaveLoop()
  try {
    const out = await recorder.stop()
    recorder = null
    if (!out) {
      setHint('这段太短了（不到 0.25 秒），没有上传')
      return
    }
    const ok = store.sendVoiceAudio(toBase64(out.wav), out.durationMs)
    if (!ok) setHint('连接断了，这段语音没发出去')
  } catch (e) {
    setHint(`录音处理失败：${String(e)}`)
  } finally {
    busy.value = false
  }
}

function cancelPendingStart(): void {
  if (startTimer) window.clearTimeout(startTimer)
  startTimer = 0
  arming.value = false
}

function scheduleStart(): void {
  cancelPendingStart()
  arming.value = true
  startTimer = window.setTimeout(() => {
    startTimer = 0
    arming.value = false
    // 这一小段里浮窗可能已经关了、实例换了、或者操作权被交还了
    if (!store.canVoice) return
    void startClip()
  }, START_DELAY_MS)
}

/**
 * 空格按下：只记「已按下」，不碰麦克风。
 * 正在录的时候按下去 = 结束。和以前一样：浮窗不开的时候**完全不拦空格**。
 */
function onKeyDown(ev: KeyboardEvent): void {
  if (ev.code !== 'Space' || ev.repeat) return
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return
  if (typable(document.activeElement)) return
  if (!store.canVoice) return
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
  if (!store.canVoice || capturing.value || busy.value) return
  ev.preventDefault()
  ev.stopPropagation()
  scheduleStart()
}

function onBlur(): void {
  // 切窗口时鼠标/键盘都可能丢事件：别让麦克风一直开着（指示灯亮着很吓人）
  armed.value = false
  cancelPendingStart()
  if (capturing.value) void stopClip()
}

// ---------------- 实时波形 ----------------

function startWaveLoop(): void {
  stopWaveLoop()
  const tick = () => {
    raf = requestAnimationFrame(tick)
    drawWave()
  }
  raf = requestAnimationFrame(tick)
}

function stopWaveLoop(): void {
  if (raf) cancelAnimationFrame(raf)
  raf = 0
}

function drawWave(): void {
  const cv = canvas.value
  const rec = recorder
  if (!cv || !rec) return
  const ctx = cv.getContext('2d')
  if (!ctx) return
  const data = rec.readWave()
  if (data) {
    let peak = 0
    for (let i = 0; i < data.length; i++) {
      const v = Math.abs(data[i])
      if (v > peak) peak = v
    }
    hist.push(peak)
    if (hist.length > HIST_LEN) hist.splice(0, hist.length - HIST_LEN)
  }
  const w = cv.width
  const h = cv.height
  ctx.clearRect(0, 0, w, h)
  if (hist.length === 0) return
  const slot = w / HIST_LEN
  const mid = h / 2
  ctx.fillStyle = '#ef4444'
  for (let i = 0; i < hist.length; i++) {
    const x = w - (hist.length - i) * slot
    const amp = Math.max(1, Math.min(1, hist[i] * 3) * (mid - 1))
    ctx.fillRect(x, mid - amp, Math.max(1, slot - 1), amp * 2)
  }
}

// ---------------- 本根语音：预览 / 清空 ----------------

/**
 * 语音的地址是**实例**那一条路由（/api/instances/<id>/voice/<file>），
 * 不是录像那条 —— 语音属于盘面，和录不录制无关。录制中同一条音频在录像目录里
 * 另有一份 audio-N.wav，但界面上听到的永远是盘面这份。
 */
function audioUrl(clip: AudioClip): string {
  const id = store.instance?.id
  if (!id) return ''
  return `/api/instances/${encodeURIComponent(id)}/voice/${encodeURIComponent(clip.file)}`
}

/**
 * 试听一条。播放器是**全局唯一**的那个（audio/player.ts）：正在放同一条就停，
 * 否则只放这一条。真正"哪条在响"写在 player 里，这个窗和横轴下方的录音条读同一份。
 */
function togglePlay(clip: AudioClip): void {
  if (capturing.value && voiceCurrentFile.value !== clip.file) {
    setHint('边录边放会把外放的声音也录进去，建议先停一下')
  }
  voiceToggle(clip, audioUrl)
}

/** 清空**本根 K 线**上的语音：两段式（第一下变红，第二下才真删）。 */
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
  voiceStop()
  store.clearVoiceBar()
}

/** 「2026-10-16 09:45」→「10-16 09:45」：清单里年份几乎没用，省下来的宽度给波形 */
function shortTime(t: string): string {
  return t.length > 10 ? t.slice(5) : t
}

function clockMs(ms: number): string {
  const s = Math.round(ms / 1000)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

// ---------------- 位置：拖动、折叠、夹在视口里（和下单浮窗同一套） ----------------

function clampPos(p: { x: number; y: number }): { x: number; y: number } {
  const w = root.value?.offsetWidth || (expanded.value ? PANEL_W : ICON)
  const h = root.value?.offsetHeight || (expanded.value ? EST_H : ICON)
  const maxX = Math.max(MARGIN, window.innerWidth - w - MARGIN)
  const maxY = Math.max(MARGIN, window.innerHeight - h - MARGIN)
  return {
    x: Math.min(Math.max(p.x, MARGIN), maxX),
    y: Math.min(Math.max(p.y, MARGIN), maxY),
  }
}

function readSavedPos(): { x: number; y: number } | null {
  try {
    const raw = localStorage.getItem(POS_KEY)
    if (!raw) return null
    const v = JSON.parse(raw) as { x?: unknown; y?: unknown }
    if (typeof v.x !== 'number' || typeof v.y !== 'number') return null
    if (!Number.isFinite(v.x) || !Number.isFinite(v.y)) return null
    return { x: v.x, y: v.y }
  } catch {
    return null
  }
}

function savePos(): void {
  try {
    localStorage.setItem(POS_KEY, JSON.stringify({ x: Math.round(pos.value.x), y: Math.round(pos.value.y) }))
  } catch {
    // 隐私模式下 localStorage 会直接抛：记不住位置不影响这次使用
  }
}

let dragFrom: { px: number; py: number; x: number; y: number } | null = null
let dragged = false

function beginDrag(ev: PointerEvent): void {
  if (ev.button !== 0) return
  ev.preventDefault()
  ev.stopPropagation()
  dragFrom = { px: ev.clientX, py: ev.clientY, x: pos.value.x, y: pos.value.y }
  dragged = false
  window.addEventListener('pointermove', onDragMove)
  window.addEventListener('pointerup', onDragEnd)
  window.addEventListener('pointercancel', onDragEnd)
}

function onDragMove(ev: PointerEvent): void {
  if (!dragFrom) return
  const dx = ev.clientX - dragFrom.px
  const dy = ev.clientY - dragFrom.py
  if (!dragged && Math.abs(dx) + Math.abs(dy) > DRAG_SLOP) dragged = true
  if (!dragged) return
  pos.value = clampPos({ x: dragFrom.x + dx, y: dragFrom.y + dy })
}

function onDragEnd(): void {
  window.removeEventListener('pointermove', onDragMove)
  window.removeEventListener('pointerup', onDragEnd)
  window.removeEventListener('pointercancel', onDragEnd)
  const moved = dragged
  dragFrom = null
  dragged = false
  if (moved) savePos()
  if (!moved && !expanded.value) setExpanded(true)
}

function setExpanded(v: boolean): void {
  expanded.value = v
  void nextTick(() => {
    pos.value = clampPos(pos.value)
  })
}

function onResize(): void {
  pos.value = clampPos(pos.value)
}

// ---------------- 生命周期 ----------------

onMounted(() => {
  pos.value = clampPos(pos.value)
  window.addEventListener('resize', onResize)
  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('keyup', onKeyUp, true)
  window.addEventListener('blur', onBlur)
  navigator.mediaDevices?.addEventListener?.('devicechange', onDeviceChange)
  // **不**在挂载时要权限：打开浮窗只是「我准备讲了」，真正开麦是在按下空格之后。
  void refreshMics(false)
})

onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  window.removeEventListener('keydown', onKeyDown, true)
  window.removeEventListener('keyup', onKeyUp, true)
  window.removeEventListener('blur', onBlur)
  window.removeEventListener('pointermove', onDragMove)
  window.removeEventListener('pointerup', onDragEnd)
  window.removeEventListener('pointercancel', onDragEnd)
  navigator.mediaDevices?.removeEventListener?.('devicechange', onDeviceChange)
  if (clearTimer) window.clearTimeout(clearTimer)
  if (hintTimer) window.clearTimeout(hintTimer)
  cancelPendingStart()
  armed.value = false
  recorder?.cancel()
  recorder = null
  stopWaveLoop()
  // 关窗**不**停播放：播放器是全局的（录音条上也能放），停不停由录音条/浮窗里的按钮说。
  // 关窗只代表「放下麦克风」—— 麦克风在上面已经 cancel 了。
})

function onDeviceChange(): void {
  void refreshMics(false)
}

// 操作权被交还 / 机器人接管 / 换了实例：麦克风必须松开（录了也发不出去）
watch(
  () => store.canVoice,
  (on) => {
    if (on) return
    armed.value = false
    cancelPendingStart()
    if (capturing.value) {
      recorder?.cancel()
      recorder = null
      capturing.value = false
      setHint('现在不能录音了（操作权被交还，或者机器人接管了盘面）')
    }
    stopWaveLoop()
    voiceStop()
    armedClear.value = false
  },
)

// 换了一根 K 线：清单整体换掉。
// 正在放的那条**只在被删掉时**才停 —— 录音条可以把别的 K 线上的语音连着听，
// 而「点一下下一根」不该把人家正在听的讲解打断（v0.17.0 之前是换根就停）。
watch(
  () => `${store.voice.barTime}|${clips.value.map((c) => c.file).join(',')}`,
  () => {
    armedClear.value = false
    if (voiceCurrentFile.value && !store.voiceAll.some((c) => c.file === voiceCurrentFile.value)) voiceStop()
  },
)

watch(
  () => store.lastError,
  (msg) => {
    if (!msg || msg === lastError) return
    lastError = msg
    // 后端的错（比如「这根录满了」「现在由别人操作」）也贴在这里，不用去右下角找
    setHint(msg)
  },
)

// 播放失败的说明写在全剧唯一的播放器里（录音条也在读），这里顺手贴进浮窗的提示条
watch(voiceError, (msg) => {
  if (msg) setHint(msg)
})
</script>

<template>
  <div
    v-if="store.voiceOpen && store.instance && !store.playbackActive"
    ref="root"
    class="fixed z-[60] text-[11px] text-dim"
    :style="{
      left: `${pos.x}px`,
      top: `${pos.y}px`,
      width: expanded ? `${PANEL_W}px` : `${ICON}px`,
    }"
    data-testid="voice-float"
    :data-bar-time="barTime"
  >
    <div v-if="expanded" class="overflow-hidden rounded-lg border border-line bg-panel shadow-2xl">
      <div
        class="flex cursor-move items-center gap-1.5 border-b border-line bg-elev px-2 py-1.5 select-none"
        title="按住这里把语音浮窗拖到顺手的位置"
        @pointerdown="beginDrag"
      >
        <span class="text-[12px]">🎙</span>
        <span class="font-medium text-ink">语音讲解</span>
        <span
          class="tnum text-[10px]"
          :class="full ? 'text-amber-600 dark:text-amber-400' : 'text-faint'"
        >
          本根 {{ barAudios }}/{{ maxAudios }}
        </span>
        <span
          v-if="store.voice.total > 0"
          class="ml-auto tnum text-[10px] text-faint"
          title="这段盘面上一共讲了几条（每根 K 线最多 9 条）"
        >
          共 {{ totalAudios }} 条
        </span>
        <button
          type="button"
          class="flex h-4 w-4 items-center justify-center rounded text-[13px] leading-none text-faint hover:bg-surface hover:text-ink"
          :class="store.voice.total > 0 ? '' : 'ml-auto'"
          title="折叠成小图标（仍可拖动）"
          @pointerdown.stop
          @click.stop="setExpanded(false)"
        >
          —
        </button>
      </div>

      <div class="px-2.5 py-1.5">
        <div class="flex items-baseline gap-2">
          <span class="tnum" :class="full ? 'text-amber-600 dark:text-amber-400' : ''">
            本根 <b class="text-ink">{{ barAudios }}</b
            >/{{ maxAudios }}
          </span>
          <span class="tnum text-[10px] text-faint">共 {{ totalAudios }} 条</span>
          <span v-if="barTime" class="tnum ml-auto text-[10px] text-faint">{{ barTime }}</span>
        </div>

        <div class="mt-1.5 flex items-center gap-1">
          <select
            class="min-w-0 flex-1 rounded border border-line bg-elev px-1 py-0.5 text-[11px] text-ink"
            :value="deviceId"
            title="选择录音用的麦克风（只列当前系统里能用的输入设备）"
            @change="chooseMic"
          >
            <option value="">系统默认麦克风</option>
            <option v-for="m in mics" :key="m.id" :value="m.id">{{ m.label }}</option>
          </select>
          <button
            type="button"
            class="shrink-0 rounded border border-line px-1 py-0.5 text-[11px] hover:border-accent hover:text-accent"
            title="重新读一遍可用麦克风（插拔设备后点这里）"
            @click="refreshMics(true)"
          >
            刷新
          </button>
        </div>

        <div v-show="capturing" class="mt-1.5 h-8 rounded bg-elev px-1">
          <canvas ref="canvas" width="480" height="60" class="h-full w-full" />
        </div>

        <div class="mt-1.5 rounded bg-elev px-1.5 py-1 leading-4">
          <template v-if="!store.canVoice">
            <span class="text-amber-600 dark:text-amber-400">
              现在是观看状态，只能听、不能录（拿到操作权后就能开口）
            </span>
          </template>
          <template v-else-if="capturing">
            <span class="font-medium text-red-600 dark:text-red-400">● 正在录音…再按一次 空格 结束</span>
          </template>
          <template v-else-if="busy">
            <span class="text-dim">正在编码录音…</span>
          </template>
          <template v-else-if="arming">
            <span class="text-dim">准备开麦…（等按键声过去）</span>
          </template>
          <template v-else>
            <span>按一下 <b class="text-ink">空格</b>：抬起后开始录音，再按一下就结束</span>
          </template>
          <div v-if="hint" class="mt-0.5 text-amber-600 dark:text-amber-400">{{ hint }}</div>
          <div v-if="micError" class="mt-0.5 text-red-600 dark:text-red-400">{{ micError }}</div>
        </div>

        <div v-if="clips.length" class="mt-1.5">
          <div class="flex items-center gap-1">
            <span class="text-[10px] text-faint">本根的 {{ clips.length }} 条</span>
            <button
              v-if="store.canVoice"
              type="button"
              class="ml-auto rounded px-1 text-[10px]"
              :class="
                armedClear
                  ? 'bg-red-600 text-white'
                  : 'text-faint hover:text-red-600 dark:hover:text-red-400'
              "
              :title="armedClear ? '再点一下确认清空（只清这根 K 线，不可撤销）' : '清空这根 K 线上的全部语音'"
              @click="clearBar"
            >
              {{ armedClear ? '确认清空' : '清空本根' }}
            </button>
          </div>
          <div
            v-for="c in clips"
            :key="c.file"
            class="mt-0.5 flex items-center gap-1 rounded px-0.5 hover:bg-elev"
            :class="playingFile === c.file ? 'text-amber-600 dark:text-amber-400' : 'text-faint'"
          >
            <span class="tnum w-7 shrink-0">{{ clockMs(c.durationMs) }}</span>
            <span class="h-4 min-w-0 flex-1">
              <AudioWave :url="audioUrl(c)" :active="playingFile === c.file" />
            </span>
            <span v-if="c.step" class="tnum shrink-0 text-[9px] text-faint" title="这条在录像里是第几步">
              #{{ c.step }}
            </span>
            <button
              type="button"
              class="shrink-0 rounded px-1 hover:text-accent"
              :title="playingFile === c.file ? '停止试听' : '试听这一条'"
              @click="togglePlay(c)"
            >
              {{ playingFile === c.file ? '⏸' : '▶' }}
            </button>
          </div>
        </div>

        <!--
          其他 K 线上的语音：复盘只能往前走、不能倒回，所以离开一根之后那些语音
          在界面上就没有入口了。这里给一份全量清单（后端随快照/语音广播下发），
          至少能点开听 —— 不然「本根 0/9、共 12 条」会让人以为那 12 条丢了。
        -->
        <div v-if="store.voiceElsewhere.length" class="mt-1.5 border-t border-line pt-1">
          <button
            type="button"
            class="w-full text-left text-[10px] text-faint hover:text-accent"
            @click="showOthers = !showOthers"
          >
            {{ showOthers ? '▾' : '▸' }} 其他 K 线还有 {{ store.voiceElsewhere.length }} 条（点开听）
          </button>
          <div v-if="showOthers" class="mt-0.5 max-h-28 overflow-auto">
            <div
              v-for="c in store.voiceElsewhere"
              :key="c.file"
              class="mt-0.5 flex items-center gap-1 rounded px-0.5 hover:bg-elev"
              :class="playingFile === c.file ? 'text-amber-600 dark:text-amber-400' : 'text-faint'"
            >
              <span class="tnum w-14 shrink-0 text-[9px]">{{ shortTime(c.time) }}</span>
              <span class="h-4 min-w-0 flex-1">
                <AudioWave :url="audioUrl(c)" :active="playingFile === c.file" />
              </span>
              <button
                type="button"
                class="shrink-0 rounded px-1 hover:text-accent"
                :title="playingFile === c.file ? '停止试听' : `试听 ${c.time} 的语音`"
                @click="togglePlay(c)"
              >
                {{ playingFile === c.file ? '⏸' : '▶' }}
              </button>
            </div>
          </div>
        </div>

        <div class="mt-1 text-[10px] text-faint">
          语音挂在 K 线上，不录制也在；每根最多 {{ maxAudios }} 条
        </div>
      </div>
    </div>

    <!-- 折叠态：一枚 44px 的小图标，整枚都是把手（没拖动就是单击展开） -->
    <button
      v-else
      type="button"
      class="flex cursor-move flex-col items-center justify-center rounded-full border border-line bg-panel shadow-xl transition select-none hover:border-accent"
      :style="{ width: `${ICON}px`, height: `${ICON}px` }"
      :title="`语音讲解 · 本根 ${barAudios}/${maxAudios} · 单击展开，按住可拖到别处`"
      @pointerdown="beginDrag"
    >
      <span class="text-[13px] leading-none">🎙</span>
      <span class="tnum mt-1 text-[9px] leading-none text-dim">{{ barAudios }}/{{ maxAudios }}</span>
    </button>
  </div>
</template>
