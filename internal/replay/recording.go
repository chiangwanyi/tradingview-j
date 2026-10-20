package replay

// 录制与回放（v0.11.0）。
//
// 录制下来的**不是视频**，而是「起始盘面 + 之后每一条操作广播」：
// 一次复盘里真正有价值的不是像素，而是「第几步、谁、做了什么」。视频既占地方
// 又搜不动，而事件流可以逐帧跳、可以单步、还能拿去做回归测试。
//
// 三条刻意的设计：
//
//  1. **复用现有的操作记录**。录制的挂点在 hub.broadcastInstanceLocked 上 ——
//     那条路径是所有状态变化的唯一出口（真人点按钮、机器人、多人广播都走它）。
//     所以「机器人操作也能被录」不是额外功能，而是**在正确的位置只挂一次**的自然结果；
//     也正因为如此，录下来的内容和「操作记录」Tab 里看到的是同一批事件。
//  2. **第 1 步 = 点录制时的那一屏**。录制从 Base 开始（已揭示的 K 线 + 标注 +
//     挂单 + 成交 + 持仓），之后每点一次【下一根】开新的一步。步数由服务端数，
//     前端只显示，避免「界面写 12 步、文件里只有 11 段」。
//  3. **录音按 K 线时间挂载**。Time 用的是那根 K 线的明文时间（"2026-10-16 09:45"），
//     和标注、挂单、成交的时间口径完全一致；音频文件单独存，不塞进 JSON。
//
// 落盘结构：
//
//	<recordingsDir>/recordings.json                索引（列表用，省得每次读全部录像）
//	<recordingsDir>/<录像 id>/recording.json       起始盘面 + 每一步的事件
//	<recordingsDir>/<录像 id>/audio-1.wav          录音（16kHz 单声道 PCM）
//
// 索引丢了也不要紧：Open 时会扫一遍目录重建，录像文件本身才是唯一事实。

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	// maxAudiosPerStep 是**单步（单根 K 线）**最多挂几段录音（需求写死 9 条）。
	//
	// 注意是「每步」不是「每条录像」：整条录像的录音数量不设上限，这条限制的
	// 用处只有一个 —— 防止用户在**同一根 K 线**上反复重录，把一步撑成几十条，
	// 回放时那一步点开是一串听不完的碎片。换到下一根 K 线就重新有 9 条额度。
	maxAudiosPerStep = 9

	// maxAudioBytes 是单段录音的上限。16kHz / 16bit / 单声道 = 32KB/s，
	// 2MB 大约 64 秒 —— 一根 K 线上的一句讲解足够了。
	//
	// 这个数不是随便定的：录音走 WebSocket（上行单帧上限 4MB，见 ws.go 的
	// maxFramePayload），base64 会膨胀三分之一。2MB → 2.67MB，留足余量；
	// 再大就会在**发送端**被帧上限掐断，表现成「录音一松手连接就断」，极难排查。
	maxAudioBytes = 2 << 20

	// maxAudioBase64 是 base64 字符串的长度上限（解码前先挡一道，
	// 免得一条畸形消息把内存吃掉）。
	maxAudioBase64 = maxAudioBytes/3*4 + 16

	recordingFileName   = "recording.json"
	recordingsIndexName = "recordings.json"
	recordingsVersion   = 1
)

// ---------------------------------------------------------------------------
// 录制缓冲：跟着实例走
// ---------------------------------------------------------------------------

// recorder 是「正在录的那一份」。
//
// 它**不带自己的锁**：所有方法都只在 hub.mu 里被调用（广播、步进、录音
// 都在那把锁下面），所以录制顺序天然等于广播顺序 —— 这正是回放能严格复现
// 实时画面的前提。唯一在锁外做的是落盘（见 Save）。
type recorder struct {
	rec      *Recording
	audios   []pendingAudio
	audioSeq int

	// dir 是这条录像的目录（`<recordings>/<id>`，开始录制那一刻定下来）。
	// 空 = 没配录像目录，这时录音只留在内存里，停止时 Save 会报错。
	//
	// 录音在**录的当下就写进 dir**，不等停止：HUD 里的「预览播放」走的是
	// /api/recordings/<id>/audio/<file> 这条 HTTP 路由，它从磁盘读 —— 如果
	// 攒到停止才写，录制中途根本没法听自己刚录的那条。顺带崩溃也不丢音频。
	dir  string
	logf func(format string, args ...any)
}

// pendingAudio 是一段还没落盘的录音：摘要进 JSON，字节等停止时一起写文件。
type pendingAudio struct {
	clip AudioClip
	data []byte
}

// newRecorder 以实例当前盘面为第 1 步开一段录制。
//
// dir 是这条录像的目录（由 RecordingStore.MarkLive 建好）；logf 用来报告
// 「录音写不进磁盘」这类不影响录制继续的问题。
func newRecorder(inst *Instance, by, id, dir string, logf func(format string, args ...any)) *recorder {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	cursor := inst.Cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(inst.Bars) {
		cursor = len(inst.Bars) - 1
	}
	bars := make([]Bar, cursor+1)
	copy(bars, inst.Bars[:cursor+1])

	start := ""
	if len(bars) > 0 {
		start = bars[len(bars)-1].Time
	}
	now := time.Now()
	rec := &Recording{
		RecordingSummary: RecordingSummary{
			ID:        id,
			Name:      fmt.Sprintf("%s · %s 起", inst.Symbol, start),
			Symbol:    inst.Symbol,
			Folder:    inst.Folder,
			Anchor:    inst.Anchor,
			CreatedBy: by,
			CreatedAt: now.Unix(),
		},
		Tick:      inst.Tick,
		TickValue: inst.TickValue,
		Base: RecordingBase{
			Cursor:        cursor,
			Bars:          bars,
			Annotations:   copies(inst.Annotations),
			Orders:        copies(inst.Orders),
			Trades:        copies(inst.Trades),
			ClosedLots:    inst.closedLots(),
			Position:      inst.position(),
			RealizedPnl:   inst.RealizedPnl,
			UnrealizedPnl: inst.unrealizedPnl(),
		},
		// Events / Audios 都**显式给空切片**，不能留 nil：Go 的 nil slice 序列化成
		// `null`，而前端在步进条上直接读 `s.events.length`。第 1 步本来就没有事件，
		// 留 nil 会让「打开回放」当场抛 TypeError（实测 2026-10：整个播放器渲染不出来，
		// 但顶栏已经进了回放态，看着像卡死）。协议里数组一律是数组，不是 null。
		Steps:  []RecordingStep{{Step: 1, Time: start, Events: []RecordingEvent{}}},
		Audios: []AudioClip{},
	}
	return &recorder{rec: rec, dir: dir, logf: logf}
}

// recordableAction 决定哪些广播会被录进录像。
//
// 只录**改盘面**的动作：步进、标注、挂单/成交。操作权、成员变化、机器人启停、
// 录制本身都不录 —— 它们不是「画面上发生了什么」，录进去只会让第 3 步里莫名
// 多出一条「谁拿到了操作权」。
//
// 注意 order_filled 两个来源都要录：市价单（下单即成交）和被触发的挂单，
// 回放端靠它把持仓、成交和盈亏更新到当时的样子。
func recordableAction(action string) bool {
	switch action {
	case ActBarAdvanced,
		ActAnnotationAdded, ActAnnotationMoved, ActAnnotationDelete, ActAnnotationClear,
		ActOrderPlaced, ActOrderFilled, ActOrderMoved, ActOrderCancelled:
		return true
	}
	return false
}

// observe 把一条广播记进当前这一步。
//
// bar_advanced 是唯一会**开新一步**的动作：它带着这根 K 线本身（payload.Bar），
// 所以先开段、再把事件放进新段 —— 这样「第 N 步」和「第 N 步那根 K 线」永远对得上。
func (r *recorder) observe(action, by string, payload any, seq int) {
	if r == nil || r.rec == nil || !recordableAction(action) {
		return
	}
	if action == ActBarAdvanced {
		p, ok := payload.(BarAdvancedPayload)
		if !ok {
			return
		}
		r.rec.Steps = append(r.rec.Steps, RecordingStep{
			Step:   len(r.rec.Steps) + 1,
			Time:   p.Bar.Time,
			Events: []RecordingEvent{},
		})
	}
	step := &r.rec.Steps[len(r.rec.Steps)-1]
	ev := RecordingEvent{Seq: seq, At: time.Now().Unix(), By: by, Action: action}
	if payload != nil {
		// 序列化失败（理论上不可能：payload 都是一个马上要广播出去的结构体）
		// 就录一条没有 payload 的事件 —— 比整段录制失败强。
		if raw, err := json.Marshal(payload); err == nil {
			ev.Payload = raw
		}
	}
	step.Events = append(step.Events, ev)
}

// clipsOfStep 列出某一步的录音（按录制顺序）。
func (r *recorder) clipsOfStep(step int) []AudioClip {
	out := make([]AudioClip, 0, 2)
	for _, a := range r.audios {
		if a.clip.Step == step {
			out = append(out, a.clip)
		}
	}
	return out
}

// state 组装当前录制状态（给前端显示「第几步 / 本步几条 / 本步有哪几条」）。
func (r *recorder) state() RecordingState {
	if r == nil || r.rec == nil {
		return RecordingState{Clips: []AudioClip{}}
	}
	steps := r.rec.Steps
	cur, curTime := 1, ""
	if len(steps) > 0 {
		cur = steps[len(steps)-1].Step
		curTime = steps[len(steps)-1].Time
	}
	start := ""
	if len(steps) > 0 {
		start = steps[0].Time
	}
	return RecordingState{
		Active:     true,
		ID:         r.rec.ID,
		By:         r.rec.CreatedBy,
		StartedAt:  r.rec.CreatedAt,
		Step:       cur,
		StartTime:  start,
		Current:    curTime,
		Audios:     len(r.audios),
		StepAudios: len(r.clipsOfStep(cur)),
		MaxAudios:  maxAudiosPerStep,
		Clips:      r.clipsOfStep(cur),
	}
}

// addAudio 把一段 WAV 挂到**当前这一步**的 K 线上。
//
// 校验顺序是「先便宜后贵」：条数、长度、文件头。文件头必须是 RIFF/WAVE ——
// 这段音频之后会用 audio/wav 发给浏览器，收一个任意二进制进来等于开了一个
// 内容不明的本地文件服务。
//
// 条数上限是**按这一步数**的：换到下一根 K 线，额度重新从 0 开始。
func (r *recorder) addAudio(data []byte, durationMs int) (AudioClip, error) {
	if r == nil || r.rec == nil {
		return AudioClip{}, errors.New("现在没有在录制")
	}
	step := r.rec.Steps[len(r.rec.Steps)-1]
	if n := len(r.clipsOfStep(step.Step)); n >= maxAudiosPerStep {
		return AudioClip{}, fmt.Errorf("一根 K 线（一步）最多 %d 段录音，这一步已经录满了；点【下一根】可以接着录", maxAudiosPerStep)
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
	r.audioSeq++
	clip := AudioClip{
		ID:         fmt.Sprintf("au%d", r.audioSeq),
		Step:       step.Step,
		Time:       step.Time,
		DurationMs: durationMs,
		File:       fmt.Sprintf("audio-%d.wav", r.audioSeq),
		Size:       int64(len(data)),
		CreatedAt:  time.Now().Unix(),
	}
	// 立刻写盘：HUD 的预览播放要走 HTTP，而 HTTP 只认磁盘。写失败不打断录制 ——
	// 内存里那份还在，停止时 Save 会再补一次。
	if r.dir != "" {
		if err := os.WriteFile(filepath.Join(r.dir, clip.File), data, 0o644); err != nil {
			r.logf("[replay] 录音 %s 没能即时写盘（停止时会重试）：%v", clip.File, err)
		}
	}
	r.audios = append(r.audios, pendingAudio{clip: clip, data: data})
	r.rec.Audios = append(r.rec.Audios, clip)
	return clip, nil
}

// clearAudioOfStep 删掉某一步的全部录音，返回被删掉的清单。
//
// 磁盘上的文件也一起删：录制中它们已经落地了（预览播放要用），留着会变成
// 永远不会被引用的垃圾 —— 而停止时 Save 只认 audios 列表里的文件名。
func (r *recorder) clearAudioOfStep(step int) []AudioClip {
	if r == nil || r.rec == nil {
		return []AudioClip{}
	}
	removed := r.clipsOfStep(step)
	if len(removed) == 0 {
		return []AudioClip{}
	}
	kept := r.audios[:0]
	for _, a := range r.audios {
		if a.clip.Step == step {
			if r.dir != "" {
				if err := os.Remove(filepath.Join(r.dir, a.clip.File)); err != nil && !os.IsNotExist(err) {
					r.logf("[replay] 录音 %s 删不掉：%v", a.clip.File, err)
				}
			}
			continue
		}
		kept = append(kept, a)
	}
	r.audios = kept

	left := r.rec.Audios[:0]
	for _, a := range r.rec.Audios {
		if a.Step == step {
			continue
		}
		left = append(left, a)
	}
	r.rec.Audios = left
	return removed
}

// finish 收尾：填统计字段，返回录像本体（音频字节另外交给 Save）。
func (r *recorder) finish() *Recording {
	rec := r.rec
	rec.EndedAt = time.Now().Unix()
	recorded := 0
	for _, st := range rec.Steps {
		recorded += len(st.Events)
	}
	rec.RecordingSummary.StepCount = len(rec.Steps)
	rec.RecordingSummary.AudioCount = len(rec.Audios)
	rec.RecordingSummary.EventCount = recorded
	rec.RecordingSummary.Bars = len(rec.Base.Bars) + len(rec.Steps) - 1
	if n := len(rec.Steps); n > 0 {
		rec.RecordingSummary.EndTime = rec.Steps[n-1].Time
	}
	rec.RecordingSummary.Name = recordingName(rec)
	return rec
}

// recordingName 给录像起一个「一眼能认出是哪一段」的名字：
// 标的 + 起止 K 线时间 + 步数。列表里不点开也知道录的是哪一段。
func recordingName(rec *Recording) string {
	start := rec.Anchor
	if len(rec.Base.Bars) > 0 {
		start = rec.Base.Bars[len(rec.Base.Bars)-1].Time
	}
	if rec.EndTime == "" || rec.EndTime == start {
		return fmt.Sprintf("%s · %s · %d 步", rec.Symbol, start, len(rec.Steps))
	}
	return fmt.Sprintf("%s · %s → %s · %d 步", rec.Symbol, start, rec.EndTime, len(rec.Steps))
}

// ---------------------------------------------------------------------------
// 录像目录
// ---------------------------------------------------------------------------

// RecordingStore 是录像目录的读写入口。
//
// 列表走内存索引（每次 start/stop/delete 更新），回放才去读整份文件 ——
// 一条录像的 recording.json 里带着几百根 K 线，列表页没必要每次都读。
type RecordingStore struct {
	mu    sync.Mutex
	dir   string
	items []RecordingSummary
	// live 是「正在录、还没提交」的录像 id。
	//
	// 录制期间录音已经写进磁盘了（HUD 的预览播放走 HTTP，HTTP 只认磁盘），
	// 但那一刻还没有 recording.json，所以不在索引里。AudioFile 得认这几个 id，
	// 否则「录到一半点一下听听」会 404。
	live map[string]bool
	logf func(format string, args ...any)
}

type recordingsIndex struct {
	Version int                `json:"version"`
	SavedAt int64              `json:"savedAt"`
	Items   []RecordingSummary `json:"items"`
}

// OpenRecordingStore 打开（必要时创建）录像目录，并重建索引。
//
// dir 为空 = 不落盘（单元测试里常见）。这时录制仍然能在内存里跑完，
// 只是 Save 会返回一句明确的错误，而不是静默什么都没有。
func OpenRecordingStore(dir string, logf func(format string, args ...any)) (*RecordingStore, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &RecordingStore{dir: strings.TrimSpace(dir), logf: logf, live: map[string]bool{}}
	if s.dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return s, fmt.Errorf("创建录像目录 %s 失败：%w", s.dir, err)
	}
	s.items = s.scan()
	s.writeIndex()
	return s, nil
}

// Enabled 表示录像会不会落盘。
func (s *RecordingStore) Enabled() bool { return s != nil && s.dir != "" }

// Dir 是录像目录（空 = 没配置）。
func (s *RecordingStore) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// List 返回录像列表（新的在前）。
func (s *RecordingStore) List() []RecordingSummary {
	if s == nil {
		return []RecordingSummary{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RecordingSummary, len(s.items))
	copy(out, s.items)
	return out
}

// NewID 生成一个不会撞车的录像 id。
//
// 用「日期-时刻」而不是随机串：用户在文件夹里一眼能看出这段是几点录的，
// 排序也天然正确。同一秒里连开两条（几乎不可能，但测试会）就加 -2、-3。
func (s *RecordingStore) NewID(now time.Time) string {
	base := "rec-" + now.Format("20060102-150405")
	if s == nil {
		return base
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	taken := map[string]bool{}
	for _, it := range s.items {
		taken[it.ID] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if !taken[id] {
			return id
		}
	}
}

// MarkLive 为「正在录的这一条」建好目录并登记成 live，返回目录路径。
//
// 录制的第一件事就是调它：录音要在录的当下写进这个目录，预览播放才拿得到。
// 目录建不出来不拦住录制（录音还能留在内存里），只是预览会取不到。
func (s *RecordingStore) MarkLive(id string) (string, error) {
	if s == nil || s.dir == "" || id == "" {
		return "", nil
	}
	folder := filepath.Join(s.dir, recordingFolder(id))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", fmt.Errorf("创建录像目录 %s 失败：%w", folder, err)
	}
	s.mu.Lock()
	if s.live == nil {
		s.live = map[string]bool{}
	}
	s.live[id] = true
	s.mu.Unlock()
	return folder, nil
}

// UnmarkLive 取消登记（录像保存完成，或者被放弃）。
func (s *RecordingStore) UnmarkLive(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.live, id)
	s.mu.Unlock()
}

func (s *RecordingStore) isLive(id string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[id]
}

// Drop 丢掉一条没保存成的录像目录（连录音一起）。
//
// 没有 recording.json 的文件夹在列表里看不见（scan 会跳过），留着只会变成
// 谁也认不出来的垃圾，所以保存失败就删掉 —— 内容本来也只在内存里，删掉的
// 不是「已经录好的东西」。
func (s *RecordingStore) Drop(id string) {
	if s == nil || s.dir == "" || id == "" {
		return
	}
	s.UnmarkLive(id)
	if err := os.RemoveAll(filepath.Join(s.dir, recordingFolder(id))); err != nil {
		s.logf("[replay] 录像目录 %s 清不掉：%v", id, err)
	}
}

// Save 把一条录好的录像写进 <dir>/<id>/。
//
// 顺序：先写音频、再写 recording.json、最后更新索引。recording.json 是「这一条
// 录像的提交点」—— 它出现就代表这条录像完整可读；音频还没写完时它不会出现。
func (s *RecordingStore) Save(rec *Recording, audios []pendingAudio) (RecordingSummary, error) {
	if s == nil || s.dir == "" {
		return RecordingSummary{}, errors.New("没有配置录像目录（config.json 的 replayRecordingDir）")
	}
	if rec == nil {
		return RecordingSummary{}, errors.New("没有录像可保存")
	}
	folder := filepath.Join(s.dir, recordingFolder(rec.ID))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return RecordingSummary{}, err
	}
	keep := map[string]bool{}
	var size int64
	for _, a := range audios {
		name := filepath.Base(a.clip.File)
		keep[name] = true
		path := filepath.Join(folder, name)
		// 录制中已经写过一次（预览播放要用），存在就不必重写；只有当时写失败
		// 的那种才在这里补上。
		if fi, err := os.Stat(path); err == nil && fi.Size() == a.clip.Size {
			size += fi.Size()
			continue
		}
		if err := os.WriteFile(path, a.data, 0o644); err != nil {
			return RecordingSummary{}, fmt.Errorf("写录音 %s 失败：%w", a.clip.File, err)
		}
		size += int64(len(a.data))
	}
	// 清掉没人引用的 audio-*.wav：录制中被「清空本步」删过的，或者上一次录制
	// 留下的残留 —— 它们会让「目录占用」和列表里的录音数对不上。
	if entries, err := os.ReadDir(folder); err == nil {
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || keep[n] || !strings.HasPrefix(n, "audio-") || !strings.HasSuffix(n, ".wav") {
				continue
			}
			if err := os.Remove(filepath.Join(folder, n)); err != nil {
				s.logf("[replay] 多余的录音 %s 删不掉：%v", n, err)
			}
		}
	}
	if err := writeJSONAtomic(filepath.Join(folder, recordingFileName), rec); err != nil {
		return RecordingSummary{}, fmt.Errorf("写 %s 失败：%w", recordingFileName, err)
	}
	if fi, err := os.Stat(filepath.Join(folder, recordingFileName)); err == nil {
		size += fi.Size()
	}
	sum := rec.RecordingSummary
	sum.Size = size

	s.mu.Lock()
	s.items = append(s.items, sum)
	sortSummaries(s.items)
	s.writeIndexLocked()
	delete(s.live, rec.ID) // 已提交：从此列表里能看见，AudioFile 走索引那道门
	s.mu.Unlock()
	return sum, nil
}

// Load 读一条录像的全部内容。
func (s *RecordingStore) Load(id string) (*Recording, error) {
	if s == nil || s.dir == "" {
		return nil, errors.New("没有配置录像目录")
	}
	if !s.has(id) {
		return nil, fmt.Errorf("没有这条录像：%s", id)
	}
	var rec Recording
	path := filepath.Join(s.dir, recordingFolder(id), recordingFileName)
	if err := readJSONFile(path, &rec); err != nil {
		return nil, fmt.Errorf("读 %s 失败：%w", path, err)
	}
	return &rec, nil
}

// Delete 删掉一条录像（连文件夹一起）。
func (s *RecordingStore) Delete(id string) error {
	if s == nil || s.dir == "" {
		return errors.New("没有配置录像目录")
	}
	if !s.has(id) {
		return fmt.Errorf("没有这条录像：%s", id)
	}
	s.mu.Lock()
	folder := filepath.Join(s.dir, recordingFolder(id))
	kept := s.items[:0]
	for _, it := range s.items {
		if it.ID != id {
			kept = append(kept, it)
		}
	}
	s.items = kept
	s.writeIndexLocked()
	s.mu.Unlock()
	if err := os.RemoveAll(folder); err != nil {
		return fmt.Errorf("目录删不掉：%w", err)
	}
	return nil
}

// AudioFile 返回某条录像里一段录音的磁盘路径。
//
// 两道门：id 必须在索引里**或者正在录制中**（录制时录音已经落地，用户要在
// HUD 里预览自己刚录的那条），文件名必须是 audio-N.wav 且没有路径分隔符 ——
// 这个接口会把文件内容发到浏览器，不能让它读到目录外的东西。
func (s *RecordingStore) AudioFile(id, name string) (string, error) {
	if s == nil || s.dir == "" {
		return "", errors.New("没有配置录像目录")
	}
	if !s.has(id) && !s.isLive(id) {
		return "", fmt.Errorf("没有这条录像：%s", id)
	}
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return "", errors.New("录音文件名不合法")
	}
	if !strings.HasPrefix(name, "audio-") || !strings.HasSuffix(name, ".wav") {
		return "", errors.New("录音文件名不合法")
	}
	path := filepath.Join(s.dir, recordingFolder(id), name)
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return "", fmt.Errorf("录音不存在：%s", name)
	}
	return path, nil
}

func (s *RecordingStore) has(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// scan 只读地扫一遍目录，重建摘要列表（索引丢了也能恢复）。
func (s *RecordingStore) scan() []RecordingSummary {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		s.logf("[replay] 读录像目录 %s 失败：%v", s.dir, err)
		return nil
	}
	out := make([]RecordingSummary, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		folder := filepath.Join(s.dir, e.Name())
		var rec Recording
		if err := readJSONFile(filepath.Join(folder, recordingFileName), &rec); err != nil {
			continue // 不是一条完整录像（或者正在写）：跳过，不打扰用户
		}
		sum := rec.RecordingSummary
		if sum.ID == "" {
			sum.ID = e.Name()
		}
		if len(rec.Steps) > 0 {
			sum.StepCount = len(rec.Steps)
			sum.EndTime = rec.Steps[len(rec.Steps)-1].Time
		}
		sum.AudioCount = len(rec.Audios)
		sum.EventCount = 0
		for _, st := range rec.Steps {
			sum.EventCount += len(st.Events)
		}
		sum.Bars = len(rec.Base.Bars) + maxInt(0, len(rec.Steps)-1)
		if sum.Name == "" {
			sum.Name = recordingName(&rec)
		}
		sum.Size = folderSize(folder)
		out = append(out, sum)
	}
	sortSummaries(out)
	return out
}

func (s *RecordingStore) writeIndex() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeIndexLocked()
}

// writeIndexLocked 写索引。失败只打日志：录像文件本身才是事实，
// 下次启动 scan 会把列表重建出来。
func (s *RecordingStore) writeIndexLocked() {
	if s.dir == "" {
		return
	}
	idx := recordingsIndex{Version: recordingsVersion, SavedAt: time.Now().Unix(), Items: s.items}
	if err := writeJSONAtomic(filepath.Join(s.dir, recordingsIndexName), idx); err != nil {
		s.logf("[replay] 录像索引没写成：%v（下次启动会按目录重建）", err)
	}
}

// recordingFolder 把录像 id 变成安全的目录名。
func recordingFolder(id string) string { return instanceFolder(id) }

func sortSummaries(items []RecordingSummary) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt > items[j].CreatedAt
		}
		return items[i].ID > items[j].ID
	})
}

func folderSize(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !e.IsDir() {
			n += fi.Size()
		}
	}
	return n
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ---------------------------------------------------------------------------
// Hub 侧：开关、请求处理、落盘
// ---------------------------------------------------------------------------

// EnableRecordings 指定录像目录并读入已有列表。
//
// dir 为空 = 不落盘（单元测试）。返回的错误只影响「能不能保存」，
// 不影响服务起来 —— 录像目录不可写不该让整个复盘服务罢工。
func (h *Hub) EnableRecordings(dir string) error {
	st, err := OpenRecordingStore(dir, h.log)
	h.mu.Lock()
	h.records = st
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if n := len(st.List()); n > 0 {
		h.log("[replay] 录像目录 %s 里有 %d 条录像", st.Dir(), n)
	}
	return nil
}

// Records 供 HTTP 层（/api/recordings/...）取音频路径。
func (h *Hub) Records() *RecordingStore {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.records
}

func (h *Hub) handleRecordStart(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if inst.rec != nil {
		h.fail(c, req, fmt.Sprintf("已经在录制了（%s 起的，第 %d 步）", inst.rec.state().StartTime, inst.rec.state().Step))
		return
	}
	if len(inst.Bars) == 0 {
		h.fail(c, req, "这个实例还没有 K 线，没法录制")
		return
	}
	id := h.records.NewID(time.Now())
	// 先把目录建出来：录制中每录一条就写进去，HUD 的预览播放靠它。
	folder, err := h.records.MarkLive(id)
	if err != nil {
		h.log("[replay] 录像目录不可用，这条录像的录音只会留在内存里：%v", err)
	}
	inst.rec = newRecorder(inst, c.Name, id, folder, h.log)
	// 录制状态不进快照的持久化（重启后不该「接着录」），所以这里不标脏。
	st := inst.rec.state()
	h.broadcastInstanceLocked(inst, ActRecordingStarted, st, c.Name)
	h.log("[replay] %s 开始在实例 %s 录制 %s（第 1 步 %s）", c.Name, inst.ID, id, st.StartTime)
}

func (h *Hub) handleRecordStop(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	payload, ok := h.finishRecordingLocked(inst)
	if !ok {
		h.fail(c, req, "现在没有在录制")
		return
	}
	// 录制一定已经结束；payload 里带着「落盘成没成」——没配目录、磁盘满都要说清楚，
	// 但不能因为写不进去就把已经录完的内容一起丢掉（事件还在内存里，等锁外再试一次）。
	h.broadcastInstanceLocked(inst, ActRecordingStopped, payload, c.Name)
	if payload.Error != "" {
		h.log("[replay] 录像 %s 结束了但没保存：%s", payload.Recording.ID, payload.Error)
	} else {
		h.log("[replay] 录像 %s 已结束：%d 步 / %d 条录音，正在写盘", payload.Recording.ID, payload.Recording.StepCount, payload.Recording.AudioCount)
	}
}

// handleRecordAudio 接收一段录音，挂到**当前这一步**的 K 线上。
//
// 走 WS 而不是 HTTP 上传：录音只有几十到几百 KB，和别的动作走同一条有序消息流，
// 「第几步录的」天然准确 —— 换成 HTTP，并发上传和步进的先后就没法保证了。
func (h *Hub) handleRecordAudio(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if inst.rec == nil {
		h.fail(c, req, "现在没有在录制")
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
	clip, err := inst.rec.addAudio(data, req.DurationMs)
	if err != nil {
		h.fail(c, req, err.Error())
		return
	}
	h.broadcastInstanceLocked(inst, ActRecordingAudio, RecordingAudioPayload{
		Audio: clip,
		State: inst.rec.state(),
	}, c.Name)
}

// handleRecordAudioDelete 清空**当前这一步**已经录下的全部录音。
//
// 需求是「一键清除当前 K 线的音频」：录坏了、说错了，重来一遍不用把整条录像作废。
// 只清当前这一步 —— 别的步里录的东西跟这根 K 线没关系，不该被顺手删掉。
func (h *Hub) handleRecordAudioDelete(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if inst.rec == nil {
		h.fail(c, req, "现在没有在录制")
		return
	}
	step := inst.rec.state().Step
	removed := inst.rec.clearAudioOfStep(step)
	if len(removed) == 0 {
		h.fail(c, req, "这一步还没有录音")
		return
	}
	h.broadcastInstanceLocked(inst, ActRecordingAudioCleared, RecordingAudioClearedPayload{
		Removed: removed,
		State:   inst.rec.state(),
	}, c.Name)
	h.log("[replay] 第 %d 步的 %d 条录音已清除", step, len(removed))
}

func (h *Hub) handleListRecordings(c *Client) {
	h.reply(c, RecordingsMsg{Type: MsgRecordings, Dir: h.records.Dir(), Recordings: h.records.List()})
}

func (h *Hub) handleGetRecording(c *Client, req Request) {
	rec, err := h.records.Load(req.RecordingID)
	if err != nil {
		h.fail(c, req, err.Error())
		return
	}
	h.reply(c, RecordingMsg{Type: MsgRecording, Recording: rec})
}

func (h *Hub) handleDeleteRecording(c *Client, req Request) {
	if err := h.records.Delete(req.RecordingID); err != nil {
		h.fail(c, req, err.Error())
		return
	}
	h.log("[replay] 录像 %s 已删除", req.RecordingID)
	h.reply(c, RecordingsMsg{Type: MsgRecordings, Dir: h.records.Dir(), Recordings: h.records.List()})
}

// finishRecordingLocked 结束当前录制：清空实例上的录制缓冲，并把录像排进写盘队列。
//
// 返回的 payload 已经是**可以立刻广播**的样子（统计字段都填好了），只有 Size 是
// 音频字节数的估算 —— 精确值要等 Save 统计完，那点差别不值得为它把广播推迟到 IO 之后。
func (h *Hub) finishRecordingLocked(inst *Instance) (RecordingStoppedPayload, bool) {
	if inst == nil || inst.rec == nil {
		return RecordingStoppedPayload{}, false
	}
	rec := inst.rec.finish()
	audios := append([]pendingAudio(nil), inst.rec.audios...)
	inst.rec = nil

	var audioBytes int64
	for _, a := range audios {
		audioBytes += a.clip.Size
	}
	sum := rec.RecordingSummary
	sum.Size = audioBytes
	payload := RecordingStoppedPayload{Recording: sum, Dir: h.records.Dir()}

	if !h.records.Enabled() {
		payload.Error = "没有配置录像目录（config.json 的 replayRecordingDir），这条录像没有落盘"
		return payload, true
	}
	h.pendingSaves = append(h.pendingSaves, pendingSave{rec: rec, audios: audios})
	return payload, true
}

// saveRecording 在锁外把一条录像写进磁盘（见 Handle 的 defer 与 Close）。
func (h *Hub) saveRecording(s pendingSave) {
	if s.rec == nil || !h.records.Enabled() {
		return
	}
	sum, err := h.records.Save(s.rec, s.audios)
	if err != nil {
		h.log("[replay] 录像 %s 没保存成：%v", s.rec.ID, err)
		// 没有 recording.json 的目录在列表里看不见，留着谁也认不出来
		h.records.Drop(s.rec.ID)
		return
	}
	h.log("[replay] 录像已保存：%s（%d 步 / %d 条录音 / %d 个操作 / %d KB）→ %s",
		sum.ID, sum.StepCount, sum.AudioCount, sum.EventCount, sum.Size>>10, h.records.Dir())
}
