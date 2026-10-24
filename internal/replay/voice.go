package replay

// 语音讲解（v0.16.0）：从「录制」里解耦出来的一级功能。
//
// 为什么要解耦：v0.11 – v0.15 里录音是录制的**子能力** —— 录制缓冲 inst.rec 为 nil 时
// handleRecordAudio 第一句就是「现在没有在录制」，于是「只想在这根 K 线上讲两句」
// 的人被迫先开一段录像。现在语音挂在**实例**上，和绘图、挂单同级：随时能录、随时能听；
// 录制只是它的一个**下游**（录制期间录的语音会同时写进录像，回放到那一步就响）。
//
// 三条刻意的设计：
//
//  1. **唯一真相在实例上**。语音清单是实例存档的一部分（和标注、挂单一起），
//     不是录像的附属物 —— 所以「没在录制也能录」不是加了个分支，而是数据结构换了归属。
//  2. **额度只有一份**：同一根 K 线最多 maxClipsPerBar 段，按 **K 线时间**数，
//     不按步数、也不看有没有在录制。录制器沿用同一个数（见 recording.go 的别名），
//     不存在「录像里 9 条、界面上 12 条」这种两套口径。
//  3. **音频存两份，谁也不依赖谁**。实例这一份在
//     `<stateDir>/instances/<id>/voice/voice-N.wav`（关掉程序再进来还在），
//     录像那一份在 `<recordings>/<录像 id>/audio-N.wav`（录像可以单独拷走、单独删掉）。
//     代价是同一条语音占两份空间（一段 30 秒的讲解约 1MB），换来的是两边都能独立存活：
//     删了实例，旧录像里的语音还能放；删了录像，盘面上的讲解也还在。
//
// 落盘：
//
//	<stateDir>/instances/<instID>/voice.json         元数据（[]AudioClip）
//	<stateDir>/instances/<instID>/voice/voice-N.wav  音频（16kHz / 16bit / 单声道 PCM）
//
// 取用：/api/instances/<instID>/voice/<file>（见 server.go；只读、可缓存）。

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// maxClipsPerBar 是**同一根 K 线**上最多挂几段语音（需求写死 9 条）。
	//
	// 注意是「每根 K 线」不是「每个实例」也不是「每条录像」：整块盘面的语音总数不设上限，
	// 这条限制唯一的用处是防止用户在**同一根**上反复重录，把那根撑成几十条、回放时听不完。
	// 换到下一根 K 线就重新有 9 条额度。
	//
	// 录制器里的 maxAudiosPerBar 就是它（见 recording.go）—— 两处必须是同一个数，
	// 否则「录制中能录 9 条、不录制能录 12 条」这种事根本查不出来。
	maxClipsPerBar = 9

	voiceFileName = "voice.json"
	voiceDirName  = "voice"
	// voiceFilePrefix 是音频文件名前缀。校验文件名时用它挡掉任意文件
	// （这个前缀的文件内容会被当 audio/wav 发给浏览器）。
	voiceFilePrefix = "voice-"
)

// VoiceState 是**当前这根 K 线**上语音讲解的实时状态。
//
// 它跟着实例快照、每一根 K 线的广播、以及 voice_added / voice_cleared 三条路下发，
// 所以界面上「本根 3/9」永远和磁盘上的清单是同一个数（都是服务端数的）。
//
//	BarTime  这份清单属于哪根 K 线（换根就整体换一份，前端不用自己过滤）
//	Clips    这根 K 线上的语音（按录制顺序，含更早几步里录的）
//	Total    整个实例一共几条（**不限**，只有同一根 K 线才限 9 条）
//	Max      同一根 K 线的上限
//
// 为什么不把整个实例的清单都塞进快照：一根 K 线最多 9 条、一次复盘通常十几根，
// 全量下发会让每根 K 线都背着一份越来越长的数组；而界面真正要显示的永远只有当前这根。
type VoiceState struct {
	BarTime string      `json:"barTime"`
	Clips   []AudioClip `json:"clips"`
	Total   int         `json:"total"`
	Max     int         `json:"max"`
}

// voiceStore 是一个实例的语音讲解。
//
// 它**不带锁**：所有方法都只在 hub.mu 里被调用（和标注、挂单一样），
// 这样「谁先录的」就是广播顺序，不需要另建一套并发规则。
type voiceStore struct {
	// dir 是音频目录（`<stateDir>/instances/<id>/voice`）。空 = 不落盘
	// （没配状态目录，或者单元测试就是这么跑的）。不落盘时录音仍在内存里，
	// 只是重启后没了、界面上也放不出来（HTTP 只认磁盘）。
	dir   string
	seq   int
	clips []AudioClip
	logf  func(format string, args ...any)
}

func newVoiceStore(dir string, clips []AudioClip, logf func(format string, args ...any)) *voiceStore {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	v := &voiceStore{dir: dir, clips: clips, logf: logf}
	if v.clips == nil {
		v.clips = []AudioClip{}
	}
	v.seq = maxClipSeq(clips)
	return v
}

// maxClipSeq 从已有清单里推出「下一个编号从哪儿开始」。
//
// 读 id（v12）而不是读条数：清单中间被清空过几轮之后，条数会小于历史最大编号，
// 直接用条数会**重号**（两条语音都叫 v3、都指向 voice-3.wav），后写的那条会盖掉前一条。
func maxClipSeq(clips []AudioClip) int {
	max := 0
	for _, c := range clips {
		n, err := strconv.Atoi(strings.TrimPrefix(c.ID, "v"))
		if err == nil && n > max {
			max = n
		}
	}
	return max
}

// clipsOfBar 列出某一根 K 线上的语音（按录制顺序）。返回值永远不是 nil。
func (v *voiceStore) clipsOfBar(barTime string) []AudioClip {
	out := make([]AudioClip, 0, 2)
	if v == nil {
		return out
	}
	for _, c := range v.clips {
		if c.Time == barTime {
			out = append(out, c)
		}
	}
	return out
}

// state 组装某一根 K 线的状态（给快照和广播用）。
func (v *voiceStore) state(barTime string) VoiceState {
	st := VoiceState{BarTime: barTime, Clips: []AudioClip{}, Max: maxClipsPerBar}
	if v == nil {
		return st
	}
	st.Clips = v.clipsOfBar(barTime)
	st.Total = len(v.clips)
	return st
}

// add 把一段 WAV 挂到 barTime 那根 K 线上。
//
// 校验顺序「先便宜后贵」：条数、长度、文件头。文件头必须是 RIFF/WAVE ——
// 这段音频之后会用 audio/wav 发给浏览器，收一个任意二进制进来等于开了一个
// 内容不明的本地文件服务。
//
// 写盘失败不打断录音：内存里那份还在，界面上照样能看见（只是听不了），
// 下一次落盘会补（见 persist.go 的 voice.json + 目录一起写）。
func (v *voiceStore) add(data []byte, durationMs int, barTime, by string) (AudioClip, error) {
	if v == nil {
		return AudioClip{}, errors.New("这个实例还没有语音存储")
	}
	if barTime == "" {
		return AudioClip{}, errors.New("这根 K 线上还没有 K 线数据（先点【下一根】揭示一根）")
	}
	if n := len(v.clipsOfBar(barTime)); n >= maxClipsPerBar {
		return AudioClip{}, fmt.Errorf("一根 K 线最多 %d 段录音，这根已经录满了；点【下一根】可以接着录", maxClipsPerBar)
	}
	if len(data) == 0 {
		return AudioClip{}, errors.New("录音是空的")
	}
	if len(data) > maxAudioBytes {
		return AudioClip{}, fmt.Errorf("单段录音最多 %d MB", maxAudioBytes>>20)
	}
	if len(data) < 44 || string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		return AudioClip{}, errors.New("录音不是 WAV（RIFF/WAVE）格式")
	}
	if durationMs <= 0 {
		// 采样率 16kHz、16bit、单声道：一秒 32000 字节
		durationMs = int(float64(len(data)-44) / 32.0)
	}
	v.seq++
	clip := AudioClip{
		ID:         fmt.Sprintf("v%d", v.seq),
		Time:       barTime,
		DurationMs: durationMs,
		File:       fmt.Sprintf("%s%d.wav", voiceFilePrefix, v.seq),
		Size:       int64(len(data)),
		CreatedAt:  time.Now().Unix(),
		By:         by,
	}
	if v.dir != "" {
		if err := os.MkdirAll(v.dir, 0o755); err != nil {
			v.logf("[replay] 语音目录 %s 建不出来：%v", v.dir, err)
		} else if err := os.WriteFile(filepath.Join(v.dir, clip.File), data, 0o644); err != nil {
			v.logf("[replay] 语音 %s 没能写盘：%v", clip.File, err)
		}
	}
	v.clips = append(v.clips, clip)
	return clip, nil
}

// setStep 记下「这条语音在录像里是第几步」。
//
// 只有录制期间录的语音才有步号（0 = 录制之外录的）。它只是显示用的冗余信息：
// 录像里那条对应步挂的是**录像自己那份** AudioClip（另一份文件），这里不改它。
func (v *voiceStore) setStep(id string, step int) {
	if v == nil {
		return
	}
	for i := range v.clips {
		if v.clips[i].ID == id {
			v.clips[i].Step = step
			return
		}
	}
}

// clearBar 删掉某一根 K 线上的全部语音，返回被删掉的清单。
//
// 范围是**一根 K 线**（需求原话是「一键清除当前 K 线的音频」）：一根 K 线上可能
// 分几次录了好几段，说错了要重来不该只清最后一段。别的 K 线上的东西不受影响。
//
// 磁盘上的文件一起删：否则「清空」只在界面上成立，用户去文件夹里还能看见那些 wav，
// 下一次落盘也不会把它们收回来（清单里已经没有引用了）。
func (v *voiceStore) clearBar(barTime string) []AudioClip {
	if v == nil {
		return []AudioClip{}
	}
	removed := v.clipsOfBar(barTime)
	if len(removed) == 0 {
		return []AudioClip{}
	}
	gone := make(map[string]bool, len(removed))
	for _, c := range removed {
		gone[c.ID] = true
		if v.dir != "" {
			if err := os.Remove(filepath.Join(v.dir, c.File)); err != nil && !os.IsNotExist(err) {
				v.logf("[replay] 语音 %s 删不掉：%v", c.File, err)
			}
		}
	}
	kept := v.clips[:0]
	for _, c := range v.clips {
		if gone[c.ID] {
			continue
		}
		kept = append(kept, c)
	}
	v.clips = kept
	return removed
}

// all 是整份清单（落盘用）。空也要是 `[]` 而不是 null。
func (v *voiceStore) all() []AudioClip {
	if v == nil {
		return []AudioClip{}
	}
	return copies(v.clips)
}

// ---------------------------------------------------------------------------
// Hub 侧：请求处理
// ---------------------------------------------------------------------------

// attachVoiceLocked 给实例挂上语音存储（新建实例、从存档恢复时各调一次）。
//
// 目录由**状态目录**决定：没配状态目录（单元测试）时 dir 为空 = 只留内存。
func (h *Hub) attachVoiceLocked(inst *Instance) {
	dir := ""
	if h.instanceDir != "" && inst.ID != "" {
		dir = filepath.Join(instanceDirPath(h.instanceDir, inst.ID), voiceDirName)
	}
	clips := inst.voice.all()
	inst.voice = newVoiceStore(dir, clips, h.log)
}

// voiceNotes 取得实例的语音存储；没挂过就补一个内存版（旧测试直接构造的实例会走到这里）。
func (inst *Instance) voiceNotes() *voiceStore {
	if inst.voice == nil {
		inst.voice = newVoiceStore("", nil, nil)
	}
	return inst.voice
}

// voiceState 是快照 / 广播里那一份「当前这根 K 线」的状态。
func (inst *Instance) voiceState() VoiceState {
	return inst.voiceNotes().state(inst.stepContext().Time)
}

// addVoiceLocked 落一段语音：**先写实例这一份，交易记录里再抄一份**。
//
// 顺序不能反：实例是唯一真相，交易记录只是下游。写记录失败（比如记录目录不可写）
// 不该让用户刚讲的那句话丢掉 —— 记一条日志就够，界面上那条语音已经挂好了。
//
// 返回值：voice 是挂在盘面上的那一条；recorded 是交易记录里那一条
// （当前没有进行中的交易记录时为 nil，此时 voice.Step 是 0）。
func (h *Hub) addVoiceLocked(inst *Instance, data []byte, durationMs int, by string) (AudioClip, *AudioClip, error) {
	barTime := inst.stepContext().Time
	v := inst.voiceNotes()
	clip, err := v.add(data, durationMs, barTime, by)
	if err != nil {
		return AudioClip{}, nil, err
	}
	if inst.trade == nil {
		return clip, nil, nil
	}
	// 有进行中的交易记录：同一条语音在记录里也占一步（回放到那一步画面不动、语音响）。
	// 记录里存的是**自己的副本**（audio-N.wav），所以记录文件夹可以单独拷走。
	rc, rerr := inst.trade.addAudio(data, durationMs, by)
	if rerr != nil {
		h.log("[replay] 语音 %s 已挂到盘面，但没能同时写进交易记录：%v", clip.ID, rerr)
		return clip, nil, nil
	}
	v.setStep(clip.ID, rc.Step)
	clip.Step = rc.Step
	return clip, &rc, nil
}

// handleVoiceAdd 收一段语音讲解，挂到**当前那根 K 线**上（不需要在录制中）。
//
// 和绘图、挂单一样要操作权：语音是**这块盘面**的共享数据（所有人都会在清单里看到它），
// 观众只能听、不能往上加。走 WS 而不是 HTTP 上传的理由和录音时代一样：
// 一条只有几十到几百 KB，和别的动作走同一条有序消息流，「挂在哪一根」天然准确；
// 换成 HTTP 并发上传，播放与步进的先后就没法保证了。
func (h *Hub) handleVoiceAdd(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if len(inst.Bars) == 0 {
		h.fail(c, req, "这个实例还没有 K 线，没法录音")
		return
	}
	if len(req.AudioData) > maxAudioBase64 {
		h.fail(c, req, fmt.Sprintf("单段录音最多 %d MB", maxAudioBytes>>20))
		return
	}
	data, err := base64.StdEncoding.DecodeString(req.AudioData)
	if err != nil {
		h.fail(c, req, "录音数据不是合法的 base64")
		return
	}
	clip, _, err := h.addVoiceLocked(inst, data, req.DurationMs, c.Name)
	if err != nil {
		h.fail(c, req, err.Error())
		return
	}
	h.broadcastInstanceLocked(inst, ActVoiceAdded, VoiceAddedPayload{
		Audio:    clip,
		Voice:    inst.voiceState(),
		VoiceAll: inst.voiceNotes().all(),
	}, c.Name)
}

// handleVoiceClear 清空**当前这根 K 线**上的语音讲解。
//
// 录制中会连录像里那几步一起摘掉（见 clearVoiceOfBarLocked）—— 用户按下「清空本根」
// 的意思是「这根上的讲解都不要了」，不是「盘面清掉、录像里还留着」。
func (h *Hub) handleVoiceClear(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	barTime := inst.stepContext().Time
	removed, recRemoved := h.clearVoiceOfBarLocked(inst, barTime)
	if len(removed) == 0 && len(recRemoved) == 0 {
		h.fail(c, req, "这根 K 线上还没有录音")
		return
	}
	h.broadcastInstanceLocked(inst, ActVoiceCleared, VoiceClearedPayload{
		Removed:  removed,
		Voice:    inst.voiceState(),
		VoiceAll: inst.voiceNotes().all(),
	}, c.Name)
	h.log("[replay] %s 上的 %d 条语音已清除（交易记录里 %d 条）", barTime, len(removed), len(recRemoved))
}

// clearVoiceOfBarLocked 清掉一根 K 线上的语音，返回（盘面上的，交易记录里的）两份清单。
//
// 两处都要清，而且是同一个动作的两个落点：盘面那一份是用户看见的，记录那一份
// 是回放时听的。只清一处就会出现「界面上空了、回放到这根还在说话」（或者反过来）。
// 记录里那一步被摘掉之后步号会重排（见 tradeRecorder.renumber）。
func (h *Hub) clearVoiceOfBarLocked(inst *Instance, barTime string) ([]AudioClip, []AudioClip) {
	removed := inst.voiceNotes().clearBar(barTime)
	var recRemoved []AudioClip
	if inst.trade != nil {
		recRemoved = inst.trade.clearAudioOfBar(barTime)
	}
	return removed, recRemoved
}

// VoiceFile 返回某条语音的磁盘路径（给 HTTP 路由用）。
//
// 两道门：实例必须还在内存里（语音属于实例），文件名必须是 voice-N.wav 且不含
// 路径分隔符 —— 这个接口会把文件内容发给浏览器，不能让它读到目录外的东西。
func (h *Hub) VoiceFile(instID, name string) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return "", errors.New("语音文件名不合法")
	}
	if !strings.HasPrefix(name, voiceFilePrefix) || !strings.HasSuffix(name, ".wav") {
		return "", errors.New("语音文件名不合法")
	}
	h.mu.Lock()
	inst, ok := h.instances[instID]
	dir := ""
	if ok && inst.voice != nil {
		dir = inst.voice.dir
	}
	h.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("没有这个实例：%s（可能已被删除）", instID)
	}
	if dir == "" {
		return "", errors.New("这个实例的语音没有落盘（没配 state 目录）")
	}
	path := filepath.Join(dir, name)
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return "", fmt.Errorf("语音不存在：%s", name)
	}
	return path, nil
}
