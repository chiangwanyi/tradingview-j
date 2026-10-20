<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { ClipRecorder, listMics, micSupported, requestMicPermission, toBase64, type MicDevice } from '@/audio/mic'
import { useReplayStore } from '@/stores/replay'
import AudioWave from '@/components/AudioWave.vue'
import type { AudioClip } from '@/types/protocol'

/**
 * 录制中的浮层：步数、本步录音、麦克风选择、实时波形，以及录音本身。
 *
 * 为什么放在画布左上角：那是盘面上唯一**天然空着**的角落（价格轴在右、时间轴在下，
 * K 线从左下往右上长）。半透明 + 只占一小块，不挡正在看的那几根。
 *
 * 交互是「按一下空格开始、再按一下停」（v0.11.1 改的）：按住说话适合对讲机，
 * 但讲一根 K 线常常要说十几秒，一直按着空格手是酸的，而且按住时没法用空格做别的。
 *
 * 录音上限是**每根 K 线 9 条**（不是每条录像 9 条）：换到下一步，额度重新开始。
 * 所以这里显示的是 `本步 x/9`，另外用一个小字提示整条录像一共几条。
 */

const store = useReplayStore()

const DEVICE_KEY = 'tv-replay-mic'

const mics = ref<MicDevice[]>([])
const deviceId = ref(readDevice())
const capturing = ref(false)
const busy = ref(false)
const micError = ref('')
const hint = ref('')
/** 正在预览播放的那条（文件名）；空 = 没在放 */
const playingFile = ref('')
/** 「清空本步」的两段式确认 */
const armedClear = ref(false)
/**
 * 折叠成一行。
 *
 * 不是为了好看：下单浮窗是 `fixed`、默认就落在左上角（x=16, y=112），和这个浮层
 * 正好重叠。浮层在最上层（z-50，不然点不到），展开时会把浮窗的拖拽条盖住 ——
 * 录着录着想把浮窗挪开都做不到。折起来只剩一行，浮窗的标题栏就又露出来了。
 */
const collapsed = ref(false)

let recorder: ClipRecorder | null = null
let lastError = ''
let clearTimer = 0
let audio: HTMLAudioElement | null = null

const state = computed(() => store.recording)
const step = computed(() => state.value?.step ?? 0)
const clips = computed<AudioClip[]>(() => state.value?.clips ?? [])
const stepAudios = computed(() => state.value?.stepAudios ?? clips.value.length)
const maxAudios = computed(() => state.value?.maxAudios ?? 9)
const totalAudios = computed(() => state.value?.audios ?? 0)
const full = computed(() => stepAudios.value >= maxAudios.value)

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

// ---------------- 录音 ----------------

async function startClip(): Promise<void> {
  if (capturing.value || busy.value || !store.isRecording) return
  if (full.value) {
    hint.value = `这根 K 线最多 ${maxAudios.value} 条录音，已经录满了；点【下一根】可以接着录`
    return
  }
  hint.value = ''
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
      hint.value = '这段太短了（不到 0.25 秒），没有上传'
      return
    }
    const ok = store.sendRecordingAudio(toBase64(out.wav), out.durationMs)
    if (!ok) hint.value = '连接断了，这段录音没发出去'
  } catch (e) {
    hint.value = `录音处理失败：${String(e)}`
  } finally {
    busy.value = false
  }
}

/** 空格：按一下开始、再按一下停。 */
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
  void startClip()
}

function onBlur(): void {
  // 切窗口时鼠标/键盘都可能丢事件：别让录音一直开着（麦克风指示灯亮着很吓人）
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

// ---------------- 本步录音：预览 / 清空 ----------------

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
      if (playingFile.value) hint.value = '这条录音取不到（可能刚被清空）'
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
  if (capturing.value) hint.value = '边录边放会把外放的声音也录进去，建议先停一下'
  playingFile.value = clip.file
  el.src = audioUrl(clip)
  void el.play().catch((e: unknown) => {
    const name = e instanceof Error ? e.name : ''
    hint.value =
      name === 'NotAllowedError'
        ? '浏览器拦了自动播放：先点一下页面空白处，再点 ▶'
        : `这条录音播不了（${name || String(e)}）`
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

/** 清空**本步**的录音：两段式（第一下变红，第二下才真删）。 */
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

// ---------------- 生命周期 ----------------

onMounted(() => {
  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('blur', onBlur)
  navigator.mediaDevices?.addEventListener?.('devicechange', onDeviceChange)
  // **不**在挂载时要权限：这个组件一直挂着，页面一打开就弹麦克风授权会很吓人。
  // 先只读一份设备表（没授权时是"麦克风 1/2"这种兜底名），真正要权限是在开始录制时。
  void refreshMics(false)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown, true)
  window.removeEventListener('blur', onBlur)
  navigator.mediaDevices?.removeEventListener?.('devicechange', onDeviceChange)
  if (clearTimer) window.clearTimeout(clearTimer)
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
    if (capturing.value) {
      recorder?.cancel()
      recorder = null
      capturing.value = false
    }
    stopWaveLoop()
    stopPreview()
    armedClear.value = false
    hint.value = ''
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
    // 后端的错（比如「这根 K 线录满了」）也贴在这里，不用去右下角找
    hint.value = msg
  },
)
</script>

<template>
  <div
    v-if="store.isRecording"
    class="pointer-events-auto absolute top-2 left-2 z-[60] w-[268px] rounded-md border border-line bg-panel px-2.5 py-1.5 text-[11px] text-dim shadow-lg"
    data-testid="record-hud"
    :data-recording-id="state?.id ?? ''"
  >
    <div class="flex items-center gap-1.5">
      <span class="inline-block h-2 w-2 shrink-0 animate-pulse rounded-full bg-red-500" />
      <span class="font-medium text-red-600 dark:text-red-400">录制中</span>
      <span
        v-if="collapsed"
        class="tnum text-[10px]"
        :class="full ? 'text-amber-600 dark:text-amber-400' : 'text-faint'"
      >
        本步 {{ stepAudios }}/{{ maxAudios }}
      </span>
      <span class="ml-auto tnum text-ink">第 {{ step }} 步</span>
      <button
        type="button"
        class="ml-1 rounded px-1 text-[11px] leading-none text-faint hover:text-accent"
        :title="collapsed ? '展开录制面板' : '折叠成一行（下单浮窗被挡住时用）'"
        @click="collapsed = !collapsed"
      >
        {{ collapsed ? '+' : '–' }}
      </button>
    </div>

    <div v-show="!collapsed" class="mt-1 flex items-baseline gap-2">
      <span class="tnum" :class="full ? 'text-amber-600 dark:text-amber-400' : ''">
        本步录音 <b class="text-ink">{{ stepAudios }}</b
        >/{{ maxAudios }}
      </span>
      <span class="tnum text-[10px] text-faint">共 {{ totalAudios }} 条</span>
      <span v-if="state?.current" class="tnum ml-auto text-[10px] text-faint">{{ state.current }}</span>
    </div>

    <div v-show="!collapsed">
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
      <template v-else>
        <span>按 <b class="text-ink">空格</b> 开始录音，再按一次结束</span>
      </template>
      <div v-if="hint" class="mt-0.5 text-amber-600 dark:text-amber-400">{{ hint }}</div>
      <div v-if="micError" class="mt-0.5 text-red-600 dark:text-red-400">{{ micError }}</div>
    </div>

    <!-- 本步已录的几条：波形 + 时长 + 试听 -->
    <div v-if="clips.length" class="mt-1.5">
      <div class="flex items-center gap-1">
        <span class="text-[10px] text-faint">本步的 {{ clips.length }} 条</span>
        <button
          type="button"
          class="ml-auto rounded px-1 text-[10px]"
          :class="
            armedClear
              ? 'bg-red-600 text-white'
              : 'text-faint hover:text-red-600 dark:hover:text-red-400'
          "
          :title="armedClear ? '再点一下确认清空（只清这一步，不可撤销）' : '清空这一步的全部录音'"
          @click="clearStep"
        >
          {{ armedClear ? '确认清空' : '清空本步' }}
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
</template>
