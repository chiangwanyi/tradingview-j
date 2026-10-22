<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { ClipRecorder, listMics, micSupported, requestMicPermission, toBase64, type MicDevice } from '@/audio/mic'
import { useReplayStore } from '@/stores/replay'
import AudioWave from '@/components/AudioWave.vue'
import type { AudioClip } from '@/types/protocol'

/**
 * 录制浮窗：步数、本根 K 线的录音、麦克风选择、实时波形，以及录音本身。
 *
 * 位置自己拖、可以折叠成一枚 44px 的小图标 —— **和下单浮窗（TradeFloat）同一套交互**：
 * 标题栏是把手（`pointerdown` 里 `preventDefault`，不然一按就框选文字、下拉框还抢焦点）、
 * 位移超过 5px 才算拖动、折叠后整枚图标都是把手（没拖动就是单击展开）、
 * 位置存 localStorage、每次移动/展开/窗口缩放都夹回视口里。
 *
 * 为什么不继续用「画布左上角 + 折成一行」：折叠成一行仍然占着一整条 268px 的宽度，
 * 录着录着想把下单浮窗挪开，还是得先跟它抢位置；而 `fixed` 之后它可以被拖到任何角落，
 * 也终于能和下单浮窗**默认就错开**（首次落在下单浮窗右边一点，两个窗不重叠）。
 *
 * 交互是「按一下空格开始、再按一下停」（v0.11.1 改的）：按住说话适合对讲机，
 * 但讲一根 K 线常常要说十几秒，一直按着空格手是酸的，而且按住时没法用空格做别的。
 * v0.12.0 起**抬起空格之后才开麦**（再等 `START_DELAY_MS`），见 `onKeyUp` 的注释。
 *
 * 录音上限是**每根 K 线 9 条**（不是每条录像 9 条）：换到下一步，额度重新开始。
 * 所以这里显示的是 `本根 x/9`，另外用一个小字提示整条录像一共几条。
 *
 * 注意「步」和「根」的区别（v0.15.0）：**一步 = 一次操作**，一根 K 线上可以有
 * 十几步（点下一根、画图、挂单、录音各算一步）。录音额度是按**根**算的，
 * 所以 HUD 里的 9 条是「这根 K 线还剩几条」，不是「这一步」。
 */

const store = useReplayStore()

const DEVICE_KEY = 'tv-replay-mic'
const POS_KEY = 'tv-record-hud-pos'

/** 展开态宽度（和下单浮窗几乎一样宽，两个窗并排看着是齐的） */
const PANEL_W = 268
/** 折叠态图标边长 */
const ICON = 44
/** 量不到实际高度时的估算值（首帧还没渲染，`offsetHeight` 是 0） */
const EST_H = 300
/** 离视口边缘至少留这么多：半截露在外面的浮窗是找不回来的 */
const MARGIN = 8
/** 位移超过这么多像素才算拖动，否则算单击（折叠态点一下 = 展开） */
const DRAG_SLOP = 5

/**
 * 抬起空格之后等这么久再开麦。
 *
 * 为什么要等：机械键盘的「咔」在按下和**抬起**各有一声，抬起那一声正好落在
 * `keydown` 之后几十毫秒里。原来一按下就 `getUserMedia`，麦克风比手指快，
 * 于是每段录音的开头都顶着一记按键声。改到 `keyup` 之后再等 90ms：
 * 键帽回弹的撞击声本身 < 30ms，这时候已经过去，人声还没起。
 *
 * 顺便：`getUserMedia` 本身要几十毫秒才把设备打开，这层延迟只会让起点更干净。
 */
const START_DELAY_MS = 90

const mics = ref<MicDevice[]>([])
const deviceId = ref(readDevice())
const capturing = ref(false)
const busy = ref(false)
/** 空格已经按下、还没抬起（抬起来才真正开麦） */
const armed = ref(false)
/** 空格已抬起，正在等那 90ms */
const arming = ref(false)
const micError = ref('')
const hint = ref('')
/** 正在预览播放的那条（文件名）；空 = 没在放 */
const playingFile = ref('')
/** 「清空本根」的两段式确认 */
const armedClear = ref(false)

/**
 * 展开态。折叠只是把盒子缩成 44px 的图标，锚点（左上角）不动 ——
 * 和下单浮窗一样，位置只有一份，展开/折叠不跳位。
 */
const expanded = ref(true)
/**
 * 浮窗左上角（视口像素）。初值在 setup 里就算好，否则第一帧会画在 (0,0)、
 * 下一帧才跳到目标位置，看着像闪了一下。
 *
 * 首次出现的默认位置在下单浮窗右边：录制时两个窗都在（录模拟交易恰恰要用下单），
 * 默认就错开，用户不用先拖一次。
 */
const pos = ref(readSavedPos() ?? { x: 300, y: 112 })
const root = ref<HTMLElement | null>(null)

let recorder: ClipRecorder | null = null
/** 上一次贴过的 store.lastError，避免同一个错误重复贴 */
let lastError = ''
let clearTimer = 0
let hintTimer = 0
let startTimer = 0
let audio: HTMLAudioElement | null = null

const state = computed(() => store.recording)
const step = computed(() => state.value?.step ?? 0)
const clips = computed<AudioClip[]>(() => state.value?.clips ?? [])
const barAudios = computed(() => state.value?.barAudios ?? clips.value.length)
const maxAudios = computed(() => state.value?.maxBarAudios ?? 9)
const totalAudios = computed(() => state.value?.audios ?? 0)
const full = computed(() => barAudios.value >= maxAudios.value)

const canvas = ref<HTMLCanvasElement | null>(null)
/** 实时波形的历史电平（每帧一个峰值），画成一条从右往左滚的包络 */
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
    // 之前选的设备没了（拔掉了），回到系统默认
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
 * 为什么要过期：这个浮窗只活在「录制中」，而一句话贴上去就再没人撤 ——
 * 上一条错误会一路跟着浮窗活到下一次录音，甚至跨到新建的实例上去
 * （曾经把「这个实例已经被删除了」贴进了一个全新实例的浮窗里）。
 * 提示本来就应该只描述**刚才那一下**。
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
  if (capturing.value || busy.value || !store.isRecording) return
  if (full.value) {
    setHint(`这根 K 线最多 ${maxAudios.value} 条录音，已经录满了；点【下一根】可以接着录`)
    return
  }
  setHint('')
  // 预览还在响就别开录：外放的声音会被麦克风一起收进去
  stopPreview()
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
    const ok = store.sendRecordingAudio(toBase64(out.wav), out.durationMs)
    if (!ok) setHint('连接断了，这段录音没发出去')
  } catch (e) {
    setHint(`录音处理失败：${String(e)}`)
  } finally {
    busy.value = false
  }
}

/** 撤掉「等 90ms 再开麦」那一步（切窗口、点停止、换实例都要撤）。 */
function cancelPendingStart(): void {
  if (startTimer) window.clearTimeout(startTimer)
  startTimer = 0
  arming.value = false
}

/** 抬起空格后把开麦排进队列：这段延迟只为躲开按键回弹声，见 START_DELAY_MS。 */
function scheduleStart(): void {
  cancelPendingStart()
  arming.value = true
  startTimer = window.setTimeout(() => {
    startTimer = 0
    arming.value = false
    // 这一小段里录音可能已经被结束了（点了【录制中…】、操作者换了人、连接断了）
    if (!store.isRecording) return
    void startClip()
  }, START_DELAY_MS)
}

/**
 * 空格按下：**只记「已按下」，不碰麦克风**。
 *
 * 正在录的时候按下去 = 结束（抬手前那一下咔哒声本来也会被录进去，早点停少半帧噪音）。
 */
function onKeyDown(ev: KeyboardEvent): void {
  if (ev.code !== 'Space' || ev.repeat) return
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return
  if (typable(document.activeElement)) return
  if (!store.isRecording) return
  // 一定要吞掉：否则按钮会被空格「按」一下、页面还会往下滚一格
  ev.preventDefault()
  ev.stopPropagation()
  if (capturing.value) {
    void stopClip()
    return
  }
  if (busy.value) return
  // 已经在等那 90ms 了又按一下：当作取消（这次抬起会重新排队，不会录出两条）
  if (arming.value) cancelPendingStart()
  armed.value = true
}

/**
 * 空格抬起：这里才真正开始录。
 *
 * v0.12.0 之前是 `keydown` 里直接 `getUserMedia` —— 麦克风比手指快，
 * 每段录音的开头都带着一记机械键盘的回弹声。现在把开麦挪到 `keyup` 之后再等
 * `START_DELAY_MS`，起点就干净了。
 */
function onKeyUp(ev: KeyboardEvent): void {
  if (ev.code !== 'Space') return
  if (!armed.value) return
  armed.value = false
  if (!store.isRecording || capturing.value || busy.value) return
  ev.preventDefault()
  ev.stopPropagation()
  scheduleStart()
}

function onBlur(): void {
  // 切窗口时鼠标/键盘都可能丢事件：别让录音一直开着（麦克风指示灯亮着很吓人），
  // 也别让「等 90ms 开麦」在后台偷偷启动
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
    // 从左往右画「最新的在右边」
    const x = w - (hist.length - i) * slot
    const amp = Math.max(1, Math.min(1, hist[i] * 3) * (mid - 1))
    ctx.fillRect(x, mid - amp, Math.max(1, slot - 1), amp * 2)
  }
}

// ---------------- 本根录音：预览 / 清空 ----------------

function audioUrl(clip: AudioClip): string {
  const rec = state.value
  if (!rec?.id) return ''
  return `/api/recordings/${encodeURIComponent(rec.id)}/audio/${encodeURIComponent(clip.file)}`
}

function ensureAudio(): HTMLAudioElement {
  if (!audio) {
    audio = new Audio()
    audio.preload = 'auto'
    audio.onended = () => {
      playingFile.value = ''
    }
    audio.onerror = () => {
      if (playingFile.value) setHint('这条录音取不到（可能刚被清空）')
      playingFile.value = ''
    }
  }
  return audio
}

function togglePlay(clip: AudioClip): void {
  const el = ensureAudio()
  if (playingFile.value === clip.file) {
    stopPreview()
    return
  }
  // 边放边录会把自己的声音录进去，先停录？不 —— 用户可能就是想「听一下再接着说」。
  // 但至少不要把预览自己的声音当成输入：外放没法避免，提示一句。
  if (capturing.value) setHint('边录边放会把外放的声音也录进去，建议先停一下')
  playingFile.value = clip.file
  el.src = audioUrl(clip)
  void el.play().catch((e: unknown) => {
    const name = e instanceof Error ? e.name : ''
    setHint(
      name === 'NotAllowedError'
        ? '浏览器拦了自动播放：先点一下页面空白处，再点 ▶'
        : `这条录音播不了（${name || String(e)}）`,
    )
    playingFile.value = ''
  })
}

function stopPreview(): void {
  playingFile.value = ''
  if (audio) {
    audio.pause()
    audio.removeAttribute('src')
  }
}

/** 清空**本根 K 线**上的录音：两段式（第一下变红，第二下才真删）。 */
function clearStep(): void {
  if (!armedClear.value) {
    armedClear.value = true
    if (clearTimer) window.clearTimeout(clearTimer)
    clearTimer = window.setTimeout(() => {
      armedClear.value = false
    }, 3000)
    return
  }
  armedClear.value = false
  stopPreview()
  store.deleteStepAudio()
}

function clockMs(ms: number): string {
  const s = Math.round(ms / 1000)
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
}

// ---------------- 位置：拖动、折叠、夹在视口里（和 TradeFloat 同一套） ----------------

function clampPos(p: { x: number; y: number }): { x: number; y: number } {
  // 用实测宽高：内容会随「有没有本步录音」微变，硬编码迟早夹不准
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

/** 拖动中的基准：按下那一刻的指针位置 + 当时的浮窗位置 */
let dragFrom: { px: number; py: number; x: number; y: number } | null = null
let dragged = false

function beginDrag(ev: PointerEvent): void {
  if (ev.button !== 0) return
  // 关键：不 preventDefault 的话，拖动过程会顺手选中浮层里的文字、下拉框还会抢焦点
  ev.preventDefault()
  ev.stopPropagation()
  dragFrom = { px: ev.clientX, py: ev.clientY, x: pos.value.x, y: pos.value.y }
  dragged = false
  window.addEventListener('pointermove', onDragMove)
  window.addEventListener('pointerup', onDragEnd)
  // 手指/触控笔被系统抢走时也要收尾，否则监听会留在 window 上，浮窗一直跟着指针跑
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
  // 折叠态：图标只有一个动作 —— 没拖动就是「点一下展开」
  if (!moved && !expanded.value) setExpanded(true)
}

/** 展开/折叠。折叠后左上角不动，展开时按新宽度再夹一次（否则贴右边的浮窗会露出去）。 */
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
  // 存档位置可能来自更大的窗口（缩窗口后位置就越界了），挂载时先夹一次
  pos.value = clampPos(pos.value)
  window.addEventListener('resize', onResize)
  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('keyup', onKeyUp, true)
  window.addEventListener('blur', onBlur)
  navigator.mediaDevices?.addEventListener?.('devicechange', onDeviceChange)
  // **不**在挂载时要权限：这个组件一直挂着，页面一打开就弹麦克风授权会很吓人。
  // 先只读一份设备表（没授权时是"麦克风 1/2"这种兜底名），真正要权限是在开始录制时。
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
  stopPreview()
})

function onDeviceChange(): void {
  void refreshMics(false)
}

// 录制结束（用户点按钮、或者操作者离开被自动收尾）：把麦克风松开、预览停掉
watch(
  () => store.isRecording,
  (on) => {
    if (on) {
      // 开始录制 = 用户明确要用麦克风了：这时才申请权限，好让设备名显示出来
      void refreshMics(true)
      return
    }
    armed.value = false
    cancelPendingStart()
    if (capturing.value) {
      recorder?.cancel()
      recorder = null
      capturing.value = false
    }
    stopWaveLoop()
    stopPreview()
    armedClear.value = false
    setHint('')
  },
)

// 换了实例（新建 / 进入另一个 / 退回大厅）：上一块盘面的提示、错误、待开麦全部作废
watch(
  () => store.instance?.id,
  () => {
    armed.value = false
    cancelPendingStart()
    lastError = ''
    micError.value = ''
    setHint('')
    stopPreview()
  },
)

// 切到下一步（或清空）后，本步的清单被换掉了：正在预览的那条如果已经不在了就停下
watch(
  () => clips.value.map((c) => c.file).join(','),
  () => {
    armedClear.value = false
    if (playingFile.value && !clips.value.some((c) => c.file === playingFile.value)) stopPreview()
  },
)

watch(
  () => store.lastError,
  (msg) => {
    if (!msg || msg === lastError) return
    lastError = msg
    // 后端的错（比如「这根 K 线录满了」「现在由别人操作」）也贴在这里，不用去右下角找。
    // 只是**当场那一下**的提示：`setHint` 会让它自己过期，换实例也会清掉。
    setHint(msg)
  },
)
</script>

<template>
  <div
    v-if="store.isRecording"
    ref="root"
    class="fixed z-[60] text-[11px] text-dim"
    :style="{
      left: `${pos.x}px`,
      top: `${pos.y}px`,
      width: expanded ? `${PANEL_W}px` : `${ICON}px`,
    }"
    data-testid="record-hud"
    :data-recording-id="state?.id ?? ''"
  >
    <!-- 展开态：标题栏是把手（按下去拖），折叠按钮不参与拖动 -->
    <div v-if="expanded" class="overflow-hidden rounded-lg border border-line bg-panel shadow-2xl">
      <div
        class="flex cursor-move items-center gap-1.5 border-b border-line bg-elev px-2 py-1.5 select-none"
        title="按住这里把录制浮窗拖到顺手的位置"
        @pointerdown="beginDrag"
      >
        <span class="inline-block h-2 w-2 shrink-0 animate-pulse rounded-full bg-red-500" />
        <span class="font-medium text-red-600 dark:text-red-400">录制中</span>
        <span
          class="tnum text-[10px]"
          :class="full ? 'text-amber-600 dark:text-amber-400' : 'text-faint'"
        >
          本根 {{ barAudios }}/{{ maxAudios }}
        </span>
        <span class="ml-auto tnum text-ink">第 {{ step }} 步</span>
        <button
          type="button"
          class="flex h-4 w-4 items-center justify-center rounded text-[13px] leading-none text-faint hover:bg-surface hover:text-ink"
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
            本根录音 <b class="text-ink">{{ barAudios }}</b
            >/{{ maxAudios }}
          </span>
          <span class="tnum text-[10px] text-faint">共 {{ totalAudios }} 条</span>
          <span v-if="state?.current" class="tnum ml-auto text-[10px] text-faint">{{ state.current }}</span>
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

        <!-- 实时波形：录的时候才占地方 -->
        <div v-show="capturing" class="mt-1.5 h-8 rounded bg-elev px-1">
          <canvas ref="canvas" width="480" height="60" class="h-full w-full" />
        </div>

        <div class="mt-1.5 rounded bg-elev px-1.5 py-1 leading-4">
          <template v-if="capturing">
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

        <!-- 本根已录的几条：波形 + 时长 + 试听 -->
        <div v-if="clips.length" class="mt-1.5">
          <div class="flex items-center gap-1">
            <span class="text-[10px] text-faint">本根的 {{ clips.length }} 条</span>
            <button
              type="button"
              class="ml-auto rounded px-1 text-[10px]"
              :class="
                armedClear
                  ? 'bg-red-600 text-white'
                  : 'text-faint hover:text-red-600 dark:hover:text-red-400'
              "
              :title="armedClear ? '再点一下确认清空（只清这根 K 线，不可撤销）' : '清空这根 K 线上的全部录音'"
              @click="clearStep"
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

        <div class="mt-1 text-[10px] text-faint">机器人操作也会被一起录下（验证两端等价）</div>
      </div>
    </div>

    <!--
      折叠态：一枚 44px 的圆形小图标（和下单浮窗同尺寸）。整枚都是把手 ——
      没拖动就展开，拖了就只是换位置。`pointerdown` 走 beginDrag（内含 preventDefault），
      展开交给 onDragEnd 判定，所以这里不绑 click：绑了会和拖动打架（拖完松手也算一次 click）。
    -->
    <button
      v-else
      type="button"
      class="flex cursor-move flex-col items-center justify-center rounded-full border border-line bg-panel shadow-xl transition select-none hover:border-accent"
      :style="{ width: `${ICON}px`, height: `${ICON}px` }"
      :title="`录制中 · 第 ${step} 步 · 单击展开，按住可拖到别处`"
      @pointerdown="beginDrag"
    >
      <span class="inline-block h-2.5 w-2.5 animate-pulse rounded-full bg-red-500" />
      <span class="tnum mt-1 text-[9px] leading-none text-dim">第{{ step }}步</span>
    </button>
  </div>
</template>
