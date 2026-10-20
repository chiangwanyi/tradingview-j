<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'

import { ClipRecorder, listMics, micSupported, requestMicPermission, toBase64, type MicDevice } from '@/audio/mic'
import { useReplayStore } from '@/stores/replay'

/**
 * 录制中的浮层：步数、录音数、麦克风选择，以及「按住空格录音」。
 *
 * 为什么放在画布左上角：那是盘面上唯一**天然空着**的角落（价格轴在右、时间轴在下，
 * K 线从左下往右上长）。半透明 + 只占一小块，不挡正在看的那几根。
 *
 * 为什么设备列表要明着列出来：`enumerateDevices` 在没授权前 label 是空的，
 * 用户看到的会是「麦克风 1 / 麦克风 2」，选错设备录出来是一片静音。
 * 所以这里是**先要权限、再列设备名**，并把选择记在本机。
 */

const store = useReplayStore()

const DEVICE_KEY = 'tv-replay-mic'
const MAX_AUDIOS = 9

const mics = ref<MicDevice[]>([])
const deviceId = ref(readDevice())
const capturing = ref(false)
const busy = ref(false)
const micError = ref('')
const hint = ref('')

let recorder: ClipRecorder | null = null
let lastError = ''

const state = computed(() => store.recording)
const step = computed(() => state.value?.step ?? 0)
const audios = computed(() => state.value?.audios ?? 0)
const full = computed(() => audios.value >= (state.value?.maxAudios ?? MAX_AUDIOS))

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

async function startClip(): Promise<void> {
  if (capturing.value || busy.value || !store.isRecording) return
  if (full.value) {
    hint.value = `一条录像最多 ${MAX_AUDIOS} 段录音，已经录满了`
    return
  }
  hint.value = ''
  recorder = new ClipRecorder()
  try {
    await recorder.start(deviceId.value)
    capturing.value = true
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

function onKeyDown(ev: KeyboardEvent): void {
  if (ev.code !== 'Space' || ev.repeat) return
  if (ev.ctrlKey || ev.metaKey || ev.altKey) return
  if (typable(document.activeElement)) return
  if (!store.isRecording) return
  // 空格在录制时是「按住说话」：一定要吞掉，否则按钮会被空格「按」一下、
  // 页面还会往下滚一格。
  ev.preventDefault()
  ev.stopPropagation()
  void startClip()
}

function onKeyUp(ev: KeyboardEvent): void {
  if (ev.code !== 'Space') return
  if (!capturing.value) return
  ev.preventDefault()
  ev.stopPropagation()
  void stopClip()
}

function onBlur(): void {
  // 切窗口时松手事件收不到：别让录音一直开着
  if (capturing.value) void stopClip()
}

onMounted(() => {
  window.addEventListener('keydown', onKeyDown, true)
  window.addEventListener('keyup', onKeyUp, true)
  window.addEventListener('blur', onBlur)
  navigator.mediaDevices?.addEventListener?.('devicechange', onDeviceChange)
  // **不**在挂载时要权限：这个组件一直挂着，页面一打开就弹麦克风授权会很吓人。
  // 先只读一份设备表（没授权时是"麦克风 1/2"这种兜底名），真正要权限是在开始录制时。
  void refreshMics(false)
})

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown, true)
  window.removeEventListener('keyup', onKeyUp, true)
  window.removeEventListener('blur', onBlur)
  navigator.mediaDevices?.removeEventListener?.('devicechange', onDeviceChange)
  recorder?.cancel()
  recorder = null
})

function onDeviceChange(): void {
  void refreshMics(false)
}

// 录制结束（用户点按钮、或者操作者离开被自动收尾）：把麦克风松开
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
  },
)

watch(
  () => store.lastError,
  (msg) => {
    if (!msg || msg === lastError) return
    lastError = msg
    // 后端的错（比如「已经录满了」）也贴在这里，不用去右下角找
    hint.value = msg
  },
)
</script>

<template>
  <div
    v-if="store.isRecording"
    class="pointer-events-auto absolute top-2 left-2 z-30 w-[248px] rounded-md border border-line bg-panel px-2.5 py-1.5 text-[11px] text-dim shadow-lg"
    data-testid="record-hud"
  >
    <div class="flex items-center gap-1.5">
      <span class="inline-block h-2 w-2 shrink-0 animate-pulse rounded-full bg-red-500" />
      <span class="font-medium text-red-600 dark:text-red-400">录制中</span>
      <span class="ml-auto tnum text-ink">第 {{ step }} 步</span>
    </div>

    <div class="mt-1 flex items-center gap-2">
      <span class="tnum" :class="full ? 'text-amber-600 dark:text-amber-400' : ''">
        录音 {{ audios }}/{{ state?.maxAudios ?? MAX_AUDIOS }}
      </span>
      <span v-if="state?.current" class="tnum ml-auto text-faint">{{ state.current }}</span>
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

    <div class="mt-1.5 rounded bg-elev px-1.5 py-1 leading-4">
      <template v-if="capturing">
        <span class="font-medium text-red-600 dark:text-red-400">● 正在录音…松开空格结束</span>
      </template>
      <template v-else-if="busy">
        <span class="text-dim">正在编码录音…</span>
      </template>
      <template v-else>
        <span>按住 <b class="text-ink">空格</b> 录音，挂在当前这根 K 线上</span>
      </template>
      <div v-if="hint" class="mt-0.5 text-amber-600 dark:text-amber-400">{{ hint }}</div>
      <div v-if="micError" class="mt-0.5 text-red-600 dark:text-red-400">{{ micError }}</div>
    </div>

    <div class="mt-1 text-[10px] text-faint">机器人操作也会被一起录下（验证两端等价）</div>
  </div>
</template>
