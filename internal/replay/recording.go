package replay

// 交易记录（v0.11.0 叫「录像」，v0.18.0 起改成**自动**的交易记录）。
//
// 记下来的**不是视频**，而是「开仓前的那一屏 + 之后每一条操作广播」：
// 一次复盘里真正有价值的不是像素，而是「第几步、谁、做了什么」。视频既占地方
// 又搜不动，而事件流可以逐帧跳、可以单步、还能拿去做回归测试。
//
// 五条刻意的设计：
//
//  1. **没有开关**（v0.18.0）。录制不再是一件用户要记得去做的事：仓位从空仓变成
//     有仓位的那一刻自动开始，仓位回到空仓的那一刻自动结束。界面上没有「录制中」，
//     因为它不是一种状态，而是交易本来就有的副产品 —— 见 trade.go 的触发点说明。
//  2. **复用现有的操作记录**。录制的挂点在 hub.broadcastInstanceLocked 上 ——
//     那条路径是所有状态变化的唯一出口（真人点按钮、机器人、多人广播都走它）。
//     所以「机器人操作也能被记」不是额外功能，而是**在正确的位置只挂一次**的自然结果；
//     也正因为如此，记下来的内容和「操作记录」Tab 里看到的是同一批事件。
//  3. **一步 = 一次操作**（v0.15.0）。[下一根]、画一个图形、挂一张单、录一段音，
//     各自占一步 —— 每一个用户手势本来就只产生一条广播，所以这个映射是 1:1 的，
//     不需要另加埋点。为什么要这样：v0.14 及以前「一步 = 一根 K 线」，一根 K 线上
//     画两个图形、挂一张单、说一段话全塞在同一步里，回放时那一步**一次性**把
//     一整根 K 线的变化全都摆出来，看不出用户真实的操作顺序。
//  4. **第 1 步 = 开仓前的那一屏**。Base 是开仓成交**之前**的盘面（空仓），
//     所以第 2 步就是开仓那一笔成交 —— 回放能看到「怎么进的场」。
//     步数由服务端数，前端只显示，避免「界面写 12 步、文件里只有 11 段」。
//  5. **语音按 K 线时间挂载、自己占一步**。Time 用的是那根 K 线的明文时间
//     （"2026-10-16 09:45"），和标注、挂单、成交的时间口径完全一致；音频文件单独存，
//     不塞进 JSON。语音那一步不改盘面（Events 为空），回放到它画面不动、语音响 ——
//     这正是真实发生的事。
//
// v0.16.0 的一条补充：**语音不是交易记录的子功能**（见 voice.go）。语音讲解挂在实例上，
// 随时能录；交易期间录的那些会在这里**额外抄一份**进记录（连同它的那一步）。
// 所以本文件仍然拥有「语音步」的全部规则，但它不再拥有「语音」这件事本身：
// 额度（每根 9 条）、清单、清空范围都以 voice.go 为准，这里只是沿用同一个数。
//
// 落盘结构：
//
//	<recordingsDir>/recordings.json                索引（列表用，省得每次读全部记录）
//	<recordingsDir>/<记录 id>/recording.json       开仓前的盘面 + 每一步的事件 + 统计
//	<recordingsDir>/<记录 id>/audio-1.wav          语音（16kHz 单声道 PCM）
//
// 索引丢了也不要紧：Open 时会扫一遍目录重建，记录文件本身才是唯一事实。
//
// **格式不兼容**：Steps 的语义从「一根 K 线一段」变成「一个操作一段」，所以
// Recording.Format 从 1 升到 2，旧记录既不迁移也不混读（需求原话：旧的不用留）。
// scan 会跳过 Format != 2 的目录并在日志里说一句，Load 会明确报「旧格式」。
// v0.18.0 **加字段不加版本**：TradeStats / InstanceID 都是可缺省的附加信息，
// format=2 的老文件照样能读，缺的统计在读取时按事件流反推（见 deriveStats）。

import (
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
	// RecordingFormat 是当前记录格式。1 = 一步一根 K 线（v0.11 – v0.14），
	// 2 = 一步一个操作（v0.15.0 起）。两种语义不兼容，见文件头注释。
	RecordingFormat = 2

	// maxAudiosPerBar 是**同一根 K 线**上最多挂几段语音（需求写死 9 条）。
	//
	// 注意是「每根 K 线」不是「每条记录」：整条记录的语音数量不设上限，这条限制的
	// 用处只有一个 —— 防止用户在**同一根 K 线**上反复重录，把那根 K 线撑成几十条，
	// 回放时听不完。换到下一根 K 线就重新有 9 条额度。
	//
	// 额度归 voice.go（maxClipsPerBar）管，记录器只是沿用同一个数。这里留个别名，
	// 是因为记录器自己也要挡一道（它可能被别处直接调用），而且历史注释和测试
	// 都按这个名字读。
	maxAudiosPerBar = maxClipsPerBar

	// maxAudioBytes 是单段语音的上限。16kHz / 16bit / 单声道 = 32KB/s，
	// 2MB 大约 64 秒 —— 一根 K 线上的一句讲解足够了。
	//
	// 这个数不是随便定的：语音走 WebSocket（上行单帧上限 4MB，见 ws.go 的
	// maxFramePayload），base64 会膨胀三分之一。2MB → 2.67MB，留足余量；
	// 再大就会在**发送端**被帧上限掐断，表现成「录音一松手连接就断」，极难排查。
	maxAudioBytes = 2 << 20

	// maxAudioBase64 是 base64 字符串的长度上限（解码前先挡一道，
	// 免得一条畸形消息把内存吃掉）。
	maxAudioBase64 = maxAudioBytes/3*4 + 16

	recordingFileName   = "recording.json"
	recordingsIndexName = "recordings.json"
	recordingsVersion   = 2
)

// StepContext 是「这一步发生在盘面的哪个位置」——由广播方（hub）在调用 observe 时
// 从实例上取。记录器自己不持有实例：它只在 h.mu 下面被调用，拿一份快照更安全，
// 也不会在实例被删掉之后留一个悬空指针。
type StepContext struct {
	Time   string // 当前那根 K 线的明文时间（空 = 还没有 K 线）
	Cursor int    // 当前游标
}

// ---------------------------------------------------------------------------
// 记录缓冲：跟着实例走
// ---------------------------------------------------------------------------

// tradeRecorder 是「这一笔交易正在记的那一份」。
//
// 它**不带自己的锁**：所有方法都只在 hub.mu 里被调用（广播、步进、语音
// 都在那把锁下面），所以记录顺序天然等于广播顺序 —— 这正是回放能严格复现
// 实时画面的前提。唯一在锁外做的是落盘（见 Save / saveTrade）。
type tradeRecorder struct {
	rec      *Recording
	audios   []pendingAudio
	audioSeq int
	// stats 边走边算的交易统计（见 trade.go）。草稿也要带上它 ——
	// 进程崩掉之后列表里那一行至少得是「赚了多少」而不是空白。
	stats statsTracker

	// cur 是「最近一步发生时的盘面位置」。observe 每开一步就更新它，
	// addAudio 靠它决定语音挂在哪根 K 线上 —— 语音不是广播驱动的那一步
	// （它由 addAudio 自己开段），所以不能像别的动作那样从参数里拿。
	cur StepContext

	// dir 是这条记录的目录（`<recordings>/<id>`，开始记录那一刻定下来）。
	// 空 = 没配记录目录，这时语音只留在内存里，收尾时 Save 会报错。
	//
	// 语音在**录的当下就写进 dir**，不等收尾：语音浮窗的预览播放走的是
	// /api/recordings/<id>/audio/<file> 这条 HTTP 路由，它从磁盘读 —— 如果
	// 攒到收尾才写，交易进行中根本没法听自己刚录的那条。顺带崩溃也不丢语音。
	dir  string
	logf func(format string, args ...any)

	// dirty 表示「自上次检查点以来又录进了东西」。
	//
	// 交易可能持续几十分钟甚至过夜，只在收尾时落盘的话，一次崩溃就全丢了。
	// stateLoop 每两秒看一眼这个标记，脏了就把当前这份写成草稿（见 checkpointTrades）。
	dirty bool
}

// pendingAudio 是一段还没落盘的语音：摘要进 JSON，字节等收尾时一起写文件。
type pendingAudio struct {
	clip AudioClip
	data []byte
}

// newTradeRecorder 以「开仓之前的那一屏」为第 1 步开一段记录。
//
// base 由 hub 在开仓那一笔成交**之前**抓下来（见 captureTradeBaseLocked）：
// 第 2 步就是开仓成交，所以回放从空仓开始演，入场那一下看得见。
// dir 是这条记录的目录（由 RecordingStore.MarkLive 建好）；logf 用来报告
// 「语音写不进磁盘」这类不影响记录继续的问题。
func newTradeRecorder(inst *Instance, base RecordingBase, by, id, dir string, logf func(format string, args ...any)) *tradeRecorder {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if base.Bars == nil {
		base.Bars = []Bar{}
	}
	if base.Annotations == nil {
		base.Annotations = []Annotation{}
	}
	if base.Orders == nil {
		base.Orders = []Order{}
	}
	if base.Trades == nil {
		base.Trades = []Trade{}
	}
	if base.ClosedLots == nil {
		base.ClosedLots = []ClosedLot{}
	}
	start := ""
	if len(base.Bars) > 0 {
		start = base.Bars[len(base.Bars)-1].Time
	}
	now := time.Now()
	rec := &Recording{
		RecordingSummary: RecordingSummary{
			ID:         id,
			Symbol:     inst.Symbol,
			Folder:     inst.Folder,
			Anchor:     start,
			InstanceID: inst.ID,
			CreatedBy:  by,
			CreatedAt:  now.Unix(),
		},
		Format:    RecordingFormat,
		Tick:      inst.Tick,
		TickValue: inst.TickValue,
		Base:      base,
		// Events / Audios 都**显式给空切片**，不能留 nil：Go 的 nil slice 序列化成
		// `null`，而前端在步进条上直接读 `s.events.length`。第 1 步本来就没有事件，
		// 留 nil 会让「打开回放」当场抛 TypeError（实测 2026-10：整个播放器渲染不出来，
		// 但顶栏已经进了回放态，看着像卡死）。协议里数组一律是数组，不是 null。
		Steps: []RecordingStep{{
			Step:   1,
			At:     now.UnixMilli(),
			Kind:   StepBase,
			By:     by,
			Time:   start,
			Cursor: base.Cursor,
			Events: []RecordingEvent{},
		}},
		Audios: []AudioClip{},
	}
	r := &tradeRecorder{
		rec:  rec,
		cur:  StepContext{Time: start, Cursor: base.Cursor},
		dir:  dir,
		logf: logf,
	}
	r.stats = newStatsTracker(inst.pointValue())
	r.dirty = true
	return r
}

// recordableAction 决定哪些广播会被记进交易记录。
//
// 只记**改盘面**的动作：步进、标注、挂单/成交。操作权、成员变化、机器人启停、
// 语音都不在这里 —— 语音会占一步，但那一步是由 addAudio 自己开的
// （见那里的注释），不走这条「一条广播 → 一步」的路。
//
// 注意 order_filled 两个来源都要记：市价单（下单即成交）和被触发的挂单，
// 回放端靠它把持仓、成交和盈亏更新到当时的样子。
func recordableAction(action string) bool {
	switch action {
	case ActBarAdvanced,
		ActAnnotationAdded, ActAnnotationMoved, ActAnnotationStyled, ActAnnotationDelete, ActAnnotationClear,
		ActOrderPlaced, ActOrderFilled, ActOrderMoved, ActOrderCancelled,
		// 一键退仓是一次真实操作（可能既撤单又平仓），回放时要占一步 ——
		// 否则「平仓那一下」在记录里凭空消失，那正是最该被看见的一步。
		ActFlatAll:
		return true
	}
	return false
}

// observe 把一条广播记成**新的一步**。
//
// v0.15.0 起不再有「开新段」和「追加到当前段」的区别：一条放行的广播就是一步。
// 这样「第 N 步」和「用户第 N 次动手」严格对应 —— 一根 K 线上画两个图形是两步，
// 先画图再录语音再画图也是四步，回放能一步一个动作地走。
//
// 为什么 1:1 是安全的：每个用户手势本来就只产生一条广播（画图在定稿时才发、
// 拖挂单松手才提交、点【下一根】时撮合结果就带在同一条 bar_advanced 的 Fills 里），
// 所以不存在「动作做了一半就被切成两步」的中间态。
//
// 统计（盈亏、最大不利/有利波动）在这里顺手算掉：payload 此刻还是**类型化的结构体**，
// 比落盘后从 JSON 里再解析一遍可靠得多（见 trade.go 的 statsTracker）。
//
// ctx 是「这条广播发生**之后**的盘面位置」（游标、当前 K 线时间），由 hub 从实例上取。
// 不能拿 r.cur：那是上一步的位置，bar_advanced 之后已经过时了。
func (r *tradeRecorder) observe(action, by string, payload any, ctx StepContext) {
	if r == nil || r.rec == nil {
		return
	}
	if !recordableAction(action) {
		return
	}
	r.stats.note(action, payload)

	ev := RecordingEvent{At: time.Now().Unix(), By: by, Action: action}
	if payload != nil {
		// 序列化失败（理论上不可能：payload 都是一个马上要广播出去的结构体）
		// 就记一条没有 payload 的事件 —— 比整段记录失败强。
		if raw, err := json.Marshal(payload); err == nil {
			ev.Payload = raw
		}
	}
	r.push(RecordingStep{
		At:     time.Now().UnixMilli(),
		Kind:   action,
		By:     by,
		Time:   ctx.Time,
		Cursor: ctx.Cursor,
		Events: []RecordingEvent{ev},
	})
}

// push 追加一步并维护步号 / cur。所有开步的地方都走它 —— 步号一旦算错，
// 「跳到第 N 步」就会演错，而且很难从界面上看出来。
func (r *tradeRecorder) push(st RecordingStep) {
	st.Step = len(r.rec.Steps) + 1
	if st.Time == "" {
		st.Time = r.cur.Time
	}
	r.rec.Steps = append(r.rec.Steps, st)
	// 步骤推进本身也算变化：检查点据此决定这一轮要不要写盘。
	r.dirty = true
	r.cur = StepContext{Time: st.Time, Cursor: st.Cursor}
}

// clipsOfBar 列出某一根 K 线上的语音（按录制顺序）。
//
// 语音的额度、列表、清空范围都按**这根 K 线**算 ——
// 一步一根 K 线的时候，「按步」和「按 K 线」是同一个东西，现在不是了。
func (r *tradeRecorder) clipsOfBar(barTime string) []AudioClip {
	out := make([]AudioClip, 0, 2)
	for _, a := range r.audios {
		if a.clip.Time == barTime {
			out = append(out, a.clip)
		}
	}
	return out
}

// addAudio 把一段 WAV 挂到**当前这根 K 线**上，并且**为它开一步**。
//
// 为什么语音要自己开一步（而不是像别的动作那样由 observe 开）：语音不是「一条广播
// 顺带产生的效果」，它本身就是一次操作 —— 用户按下空格、讲了一段、再按一下。
// 回放到这一步，画面不动、语音响，正好是当时发生的事。所以这里先建步、再广播，
// 广播带着已经包含这一步的盘面状态出去。
//
// 校验顺序是「先便宜后贵」：条数、长度、文件头。文件头必须是 RIFF/WAVE ——
// 这段音频之后会用 audio/wav 发给浏览器，收一个任意二进制进来等于开了一个
// 内容不明的本地文件服务。
//
// 条数上限按**这根 K 线**数：换到下一根 K 线，额度重新从 0 开始。
func (r *tradeRecorder) addAudio(data []byte, durationMs int, by string) (AudioClip, error) {
	if r == nil || r.rec == nil {
		return AudioClip{}, errors.New("这一笔交易还没有开始记录")
	}
	barTime := r.cur.Time
	if n := len(r.clipsOfBar(barTime)); n >= maxAudiosPerBar {
		return AudioClip{}, fmt.Errorf("一根 K 线最多 %d 段录音，这根已经录满了；点【下一根】可以接着录", maxAudiosPerBar)
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
		Step:       len(r.rec.Steps) + 1, // 这一步马上就建，步号先占住
		Time:       barTime,
		DurationMs: durationMs,
		File:       fmt.Sprintf("audio-%d.wav", r.audioSeq),
		Size:       int64(len(data)),
		CreatedAt:  time.Now().Unix(),
		By:         by,
	}
	// 立刻写盘：语音浮窗的预览播放要走 HTTP，而 HTTP 只认磁盘。写失败不打断记录 ——
	// 内存里那份还在，收尾时 Save 会再补一次。
	if r.dir != "" {
		if err := os.WriteFile(filepath.Join(r.dir, clip.File), data, 0o644); err != nil {
			r.logf("[replay] 语音 %s 没能即时写盘（收尾时会重试）：%v", clip.File, err)
		}
	}
	r.audios = append(r.audios, pendingAudio{clip: clip, data: data})
	r.rec.Audios = append(r.rec.Audios, clip)
	audio := clip
	r.push(RecordingStep{
		At:     time.Now().UnixMilli(),
		Kind:   ActRecordingAudio,
		By:     by,
		Time:   barTime,
		Cursor: r.cur.Cursor,
		Audio:  &audio,
		Events: []RecordingEvent{},
	})
	return clip, nil
}

// clearAudioOfBar 删掉某一根 K 线上的全部语音，返回被删掉的清单。
//
// 范围是**一根 K 线**（需求原话是「一键清除当前 K 线的音频」），不是「当前这一步」——
// 一根 K 线上可能已经分几步录了好几段，说错了要重来不该只清最后一段。
//
// 每段语音都占一步，所以删语音 = 删步：交易还在进行时记录还没落定、外部也没引用过
// 步号，这里直接把那些步摘掉并把后面的步号**重排**。不这么做的话，回放时那几步
// 会变成「什么都不发生」的空档，用户会以为是卡了。
func (r *tradeRecorder) clearAudioOfBar(barTime string) []AudioClip {
	if r == nil || r.rec == nil {
		return []AudioClip{}
	}
	removed := r.clipsOfBar(barTime)
	if len(removed) == 0 {
		return []AudioClip{}
	}
	gone := make(map[string]bool, len(removed))
	for _, c := range removed {
		gone[c.ID] = true
		if r.dir != "" {
			if err := os.Remove(filepath.Join(r.dir, c.File)); err != nil && !os.IsNotExist(err) {
				r.logf("[replay] 语音 %s 删不掉：%v", c.File, err)
			}
		}
	}
	kept := r.audios[:0]
	for _, a := range r.audios {
		if gone[a.clip.ID] {
			continue
		}
		kept = append(kept, a)
	}
	r.audios = kept

	left := r.rec.Audios[:0]
	for _, a := range r.rec.Audios {
		if gone[a.ID] {
			continue
		}
		left = append(left, a)
	}
	r.rec.Audios = left

	// 摘掉对应的那几步（Kind = recording_audio 且 Audio 被删了）。
	steps := r.rec.Steps[:0]
	for _, st := range r.rec.Steps {
		if st.Kind == ActRecordingAudio && st.Audio != nil && gone[st.Audio.ID] {
			continue
		}
		steps = append(steps, st)
	}
	r.rec.Steps = steps
	r.renumber()
	r.dirty = true
	return removed
}

// renumber 在摘掉若干步之后把步号重新排成 1..N，并让 AudioClip.Step 跟着走。
//
// 三处必须同时改，漏一处就会「步进条上是 7 步、语音说自己在第 9 步」：
//
//	Steps[i].Step        步号本身
//	Steps[i].Audio.Step  语音步里那份 Audio 的副本
//	rec.Audios 里的正本   （以及 r.audios 里的待落盘副本）
func (r *tradeRecorder) renumber() {
	for i := range r.rec.Steps {
		st := &r.rec.Steps[i]
		st.Step = i + 1
		if st.Audio == nil {
			continue
		}
		st.Audio.Step = st.Step
		id := st.Audio.ID
		for j := range r.rec.Audios {
			if r.rec.Audios[j].ID == id {
				r.rec.Audios[j] = *st.Audio
			}
		}
		for j := range r.audios {
			if r.audios[j].clip.ID == id {
				r.audios[j].clip = *st.Audio
			}
		}
	}
}

// finish 收尾：封上结束时间，返回记录本体（语音字节另外交给 Save）。
//
// closed = 这一笔是否正常平完。false 只有三种来源：实例被删、程序退出、
// 进程崩溃留下的草稿 —— 记录本身仍然完整，只是这笔交易当时还没结束。
func (r *tradeRecorder) finish(closed bool) *Recording {
	rec := r.rec
	rec.EndedAt = time.Now().Unix()
	rec.Stats.Closed = closed
	r.takeStats()
	rec.refresh()
	return rec
}

// takeStats 把「边走边算」的统计交给记录本体（见 trade.go 的 statsTracker）。
//
// 必须在 refresh 之前调：refresh 只补 Steps（步数从 Steps 现算），其余字段
// （盈亏、MFE/MAE、进出场价）都在 tracker 里，不搬过来列表上就全是 0。
// 草稿也搬：崩掉之后列表里那一行至少得是「赚了多少」而不是空白。
func (r *tradeRecorder) takeStats() {
	closed := r.rec.Stats.Closed
	r.rec.Stats = r.stats.snapshot()
	r.rec.Stats.Closed = closed
}

// draft 取出「当前这份」用于检查点落盘：返回摘要和一个**独立可安全序列化**的副本。
//
// 调用方（checkpointTrades）在锁内调用并在锁内就 json.Marshal 完 —— 副本里的
// Steps / Base 切片仍是共享的，只有立即序列化才安全。这样每两秒的检查点
// 不会因为「复制几百根 K 线」而拖慢步进。
func (r *tradeRecorder) draft() (RecordingSummary, *Recording) {
	rec := r.rec
	// 草稿没有「结束时间」这个概念：EndedAt 记的是最后一次写盘的时刻，
	// 列表上据此显示「最后更新」。真正收尾时会被 finish 覆盖。
	rec.EndedAt = time.Now().Unix()
	r.takeStats()
	rec.refresh()
	cp := *rec
	return rec.RecordingSummary, &cp
}

// refresh 重新数一遍摘要字段（步数、操作数、根数、名字、统计里的步数）。
//
// 草稿和收尾都走它：列表里的「12 步 / 3 根」必须和文件里真正有几段是同一个数，
// 所以这些计数**从 Steps 现算**，不接受外部传进来的值。
func (rec *Recording) refresh() {
	recorded, bars := 0, 1
	for _, st := range rec.Steps {
		recorded += len(st.Events)
		if st.Kind == ActBarAdvanced {
			bars++
		}
	}
	rec.StepCount = len(rec.Steps)
	rec.AudioCount = len(rec.Audios)
	rec.EventCount = recorded
	// Bars 是「推进了几根 K 线」，**不是**步数：一根 K 线可能十几步，
	// 列表里要让人看得出「这段有多长」。
	rec.Bars = bars
	rec.Stats.Steps = len(rec.Steps)
	if n := len(rec.Steps); n > 0 {
		rec.EndTime = rec.Steps[n-1].Time
	}
	rec.Name = tradeName(rec)
}

// tradeName 给一条记录起一个「一眼能认出是哪一笔」的名字：
// 标的 + 方向手数 + 盈亏 + 进出时间。列表里不点开也知道这是哪一次交易。
func tradeName(rec *Recording) string {
	s := rec.Stats
	side := "—"
	switch {
	case s.Side == "long":
		side = "多"
	case s.Side == "short":
		side = "空"
	}
	entry := s.EntryTime
	if entry == "" {
		entry = rec.Anchor
	}
	tail := "持仓中"
	if s.Closed {
		tail = s.ExitTime
	}
	if tail == "" {
		tail = "—"
	}
	pnl := "—"
	if s.Fills > 0 {
		pnl = fmt.Sprintf("%+.2f", s.RealizedPnl)
	}
	return fmt.Sprintf("%s · %s%.0f 手 · %s · %s → %s", rec.Symbol, side, s.Quantity, pnl, entry, tail)
}

// ---------------------------------------------------------------------------
// 记录目录
// ---------------------------------------------------------------------------

// RecordingStore 是交易记录目录的读写入口。
//
// 列表走内存索引（每次 start/stop/delete 更新），回放才去读整份文件 ——
// 一条记录的 recording.json 里带着几百根 K 线，列表页没必要每次都读。
type RecordingStore struct {
	mu    sync.Mutex
	dir   string
	items []RecordingSummary
	// live 是「已经开始、还没提交」的记录 id。
	//
	// 进行中语音已经写进磁盘了（预览播放走 HTTP，HTTP 只认磁盘），
	// 而那一刻草稿可能还没写，所以不在索引里。AudioFile 得认这几个 id，
	// 否则「交易进行中点一下听听」会 404。
	live map[string]bool
	logf func(format string, args ...any)
}

type recordingsIndex struct {
	Version int                `json:"version"`
	SavedAt int64              `json:"savedAt"`
	Items   []RecordingSummary `json:"items"`
}

// OpenRecordingStore 打开（必要时创建）记录目录，并重建索引。
//
// dir 为空 = 不落盘（单元测试里常见）。这时记录仍然能在内存里跑完，
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
		return s, fmt.Errorf("创建交易记录目录 %s 失败：%w", s.dir, err)
	}
	s.items = s.scan()
	s.writeIndex()
	return s, nil
}

// Enabled 表示记录会不会落盘。
func (s *RecordingStore) Enabled() bool { return s != nil && s.dir != "" }

// Dir 是记录目录（空 = 没配置）。
func (s *RecordingStore) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// List 返回记录列表（新的在前）。
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

// NewID 生成一个不会撞车的记录 id。
//
// 用「日期-时刻」而不是随机串：用户在文件夹里一眼能看出这笔是几点开的，
// 排序也天然正确。同一秒里连开两笔（几乎不可能，但测试会）就加 -2、-3。
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

// MarkLive 为「刚开始记录的这条」建好目录并登记成 live，返回目录路径。
//
// 记录的第一件事就是调它：语音要在录的当下写进这个目录，预览播放才拿得到。
// 目录建不出来不拦住记录（语音还能留在内存里），只是预览会取不到。
func (s *RecordingStore) MarkLive(id string) (string, error) {
	if s == nil || s.dir == "" || id == "" {
		return "", nil
	}
	folder := filepath.Join(s.dir, recordingFolder(id))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", fmt.Errorf("创建记录目录 %s 失败：%w", folder, err)
	}
	s.mu.Lock()
	if s.live == nil {
		s.live = map[string]bool{}
	}
	s.live[id] = true
	s.mu.Unlock()
	return folder, nil
}

// UnmarkLive 取消登记（记录保存完成，或者被放弃）。
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

// Drop 丢掉一条没保存成的记录目录（连语音一起）。
//
// 没有 recording.json 的文件夹在列表里看不见（scan 会跳过），留着只会变成
// 谁也认不出来的垃圾，所以保存失败就删掉 —— 内容本来也只在内存里，删掉的
// 不是「已经记好的东西」。
func (s *RecordingStore) Drop(id string) {
	if s == nil || s.dir == "" || id == "" {
		return
	}
	s.UnmarkLive(id)
	if err := os.RemoveAll(filepath.Join(s.dir, recordingFolder(id))); err != nil {
		s.logf("[replay] 记录目录 %s 清不掉：%v", id, err)
	}
}

// Save 把一条记好的记录写进 <dir>/<id>/（**按 id 覆盖**，不是追加）。
//
// 覆盖这一点很重要：交易进行中每两秒会写一次草稿（SaveDraft），收尾时同一条
// 记录会再写一次正式版 —— 列表里必须始终只有一行，而不是一行草稿加一行成品。
//
// 顺序：先写语音、再写 recording.json、最后更新索引。recording.json 是「这一条
// 记录的提交点」—— 它出现就代表这条记录完整可读；语音还没写完时它不会出现。
func (s *RecordingStore) Save(rec *Recording, audios []pendingAudio) (RecordingSummary, error) {
	if s == nil || s.dir == "" {
		return RecordingSummary{}, errors.New("没有配置交易记录目录（config.json 的 replayRecordingDir）")
	}
	if rec == nil {
		return RecordingSummary{}, errors.New("没有记录可保存")
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
		// 记录中已经写过一次（预览播放要用），存在就不必重写；只有当时写失败
		// 的那种才在这里补上。
		if fi, err := os.Stat(path); err == nil && fi.Size() == a.clip.Size {
			size += fi.Size()
			continue
		}
		if err := os.WriteFile(path, a.data, 0o644); err != nil {
			return RecordingSummary{}, fmt.Errorf("写语音 %s 失败：%w", a.clip.File, err)
		}
		size += int64(len(a.data))
	}
	// 清掉没人引用的 audio-*.wav：记录中被「清空本根」删过的，或者上一次记录
	// 留下的残留 —— 它们会让「目录占用」和列表里的语音数对不上。
	if entries, err := os.ReadDir(folder); err == nil {
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || keep[n] || !strings.HasPrefix(n, "audio-") || !strings.HasSuffix(n, ".wav") {
				continue
			}
			if err := os.Remove(filepath.Join(folder, n)); err != nil {
				s.logf("[replay] 多余的语音 %s 删不掉：%v", n, err)
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
	s.upsertLocked(sum)
	s.writeIndexLocked()
	delete(s.live, rec.ID) // 已提交：从此列表里能看见，AudioFile 走索引那道门
	s.mu.Unlock()
	return sum, nil
}

// SaveDraft 写一份「进行中」的检查点：只写 recording.json（语音早就落盘了），
// 然后把它按 id 覆盖进索引。
//
// data 是调用方在**锁内**序列化好的字节（见 checkpointTrades）：这样序列化
// 不可能和正在进行的步进抢同一份切片，而 IO 仍然留在锁外。
//
// 草稿和正式记录在列表里长得一样（同一行、同一个 id），区别只在 Stats.Closed ——
// 交易还没平完时它是 false，界面上显示「持仓中」。
func (s *RecordingStore) SaveDraft(sum RecordingSummary, data []byte) error {
	if s == nil || s.dir == "" || sum.ID == "" {
		return errors.New("没有配置交易记录目录")
	}
	folder := filepath.Join(s.dir, recordingFolder(sum.ID))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return err
	}
	path := filepath.Join(folder, recordingFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	sum.Size = folderSize(folder)
	s.mu.Lock()
	s.upsertLocked(sum)
	s.writeIndexLocked()
	s.mu.Unlock()
	return nil
}

// upsertLocked 按 id 插入或替换一行摘要。调用方持有 s.mu。
func (s *RecordingStore) upsertLocked(sum RecordingSummary) {
	for i := range s.items {
		if s.items[i].ID == sum.ID {
			s.items[i] = sum
			sortSummaries(s.items)
			return
		}
	}
	s.items = append(s.items, sum)
	sortSummaries(s.items)
}

// Load 读一条记录的全部内容。
//
// 旧格式（format<2）明确报错而不是「尽力读」：它的 Steps 是「一根 K 线一段」，
// 交给新播放器会得到一个步进条很短、每步却蹦出一堆变化的怪物 —— 与其让人
// 以为是回放坏了，不如直说这条记录该删。
func (s *RecordingStore) Load(id string) (*Recording, error) {
	if s == nil || s.dir == "" {
		return nil, errors.New("没有配置交易记录目录")
	}
	if !s.has(id) {
		// 索引里没有，但磁盘上可能是旧格式（scan 跳过了它）：给一句能对上号的解释
		var probe Recording
		if err := readJSONFile(filepath.Join(s.dir, recordingFolder(id), recordingFileName), &probe); err == nil &&
			probe.Format != RecordingFormat {
			return nil, fmt.Errorf("记录 %s 是旧格式（按 K 线分步），当前版本不再兼容；删掉 %s 下对应目录即可",
				id, s.dir)
		}
		return nil, fmt.Errorf("没有这条记录：%s", id)
	}
	var rec Recording
	path := filepath.Join(s.dir, recordingFolder(id), recordingFileName)
	if err := readJSONFile(path, &rec); err != nil {
		return nil, fmt.Errorf("读 %s 失败：%w", path, err)
	}
	if rec.Format != RecordingFormat {
		return nil, fmt.Errorf("记录 %s 是旧格式（按 K 线分步），当前版本不再兼容；删掉 %s 下对应目录即可",
			id, s.dir)
	}
	// v0.18.0 之前落盘的文件没有 stats：按事件流反推一份，
	// 否则列表/详情里那一笔交易的盈亏会是 0 —— 看起来像算错了。
	if rec.Stats.Fills == 0 && len(rec.Steps) > 1 {
		rec.Stats = deriveStats(&rec)
	}
	if rec.Name == "" {
		rec.Name = tradeName(&rec)
	}
	return &rec, nil
}

// Delete 删掉一条记录（连文件夹一起）。**与实例存档完全无关**：
// 记录在 <recordingsDir>，实例在 <stateDir>，删一边不碰另一边。
func (s *RecordingStore) Delete(id string) error {
	if s == nil || s.dir == "" {
		return errors.New("没有配置交易记录目录")
	}
	if !s.has(id) {
		return fmt.Errorf("没有这条记录：%s", id)
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

// AudioFile 返回某条记录里一段语音的磁盘路径。
//
// 两道门：id 必须在索引里**或者正在记录中**（记录时语音已经落地，用户要在
// 浮窗里预览自己刚录的那条），文件名必须是 audio-N.wav 且没有路径分隔符 ——
// 这个接口会把文件内容发到浏览器，不能让它读到目录外的东西。
func (s *RecordingStore) AudioFile(id, name string) (string, error) {
	if s == nil || s.dir == "" {
		return "", errors.New("没有配置交易记录目录")
	}
	if !s.has(id) && !s.isLive(id) {
		return "", fmt.Errorf("没有这条记录：%s", id)
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
//
// **只认当前格式**：Format 不是 RecordingFormat 的目录会被跳过 —— 旧格式的
// Steps 是「一根 K 线一段」，硬读出来会让步进条显示成另一回事（看着像回放坏了）。
// 跳过时给一句日志，用户知道那些目录可以删。
//
// 崩溃遗留的草稿也会被认出来（它有完整的 recording.json）：交易还没平完时
// Stats.Closed 是 false，列表里显示「持仓中」，不会当成「坏文件」丢掉。
func (s *RecordingStore) scan() []RecordingSummary {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		s.logf("[replay] 读交易记录目录 %s 失败：%v", s.dir, err)
		return nil
	}
	out := make([]RecordingSummary, 0, len(entries))
	stale := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		folder := filepath.Join(s.dir, e.Name())
		var rec Recording
		if err := readJSONFile(filepath.Join(folder, recordingFileName), &rec); err != nil {
			continue // 不是一条完整记录（或者正在写）：跳过，不打扰用户
		}
		if rec.Format != RecordingFormat {
			stale++
			continue
		}
		sum := rec.RecordingSummary
		if sum.ID == "" {
			sum.ID = e.Name()
		}
		sum.StepCount = len(rec.Steps)
		sum.AudioCount = len(rec.Audios)
		sum.EventCount = 0
		sum.Bars = 1
		for _, st := range rec.Steps {
			sum.EventCount += len(st.Events)
			if st.Kind == ActBarAdvanced {
				sum.Bars++
			}
		}
		if n := len(rec.Steps); n > 0 {
			sum.EndTime = rec.Steps[n-1].Time
		}
		if sum.Stats.Fills == 0 && len(rec.Steps) > 1 {
			sum.Stats = deriveStats(&rec)
		}
		if sum.Name == "" {
			sum.Name = tradeName(&rec)
		}
		sum.Size = folderSize(folder)
		out = append(out, sum)
	}
	if stale > 0 {
		s.logf("[replay] 跳过 %d 条旧格式记录（按 K 线分步，format<2）：版本升级后不再兼容，"+
			"需要的话直接删掉 %s 下对应目录即可", stale, s.dir)
	}
	sortSummaries(out)
	return out
}

func (s *RecordingStore) writeIndex() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeIndexLocked()
}

// writeIndexLocked 写索引。失败只打日志：记录文件本身才是事实，
// 下次启动 scan 会把列表重建出来。
func (s *RecordingStore) writeIndexLocked() {
	if s.dir == "" {
		return
	}
	idx := recordingsIndex{Version: recordingsVersion, SavedAt: time.Now().Unix(), Items: s.items}
	if err := writeJSONAtomic(filepath.Join(s.dir, recordingsIndexName), idx); err != nil {
		s.logf("[replay] 交易记录索引没写成：%v（下次启动会按目录重建）", err)
	}
}

// recordingFolder 把记录 id 变成安全的目录名。
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

// ---------------------------------------------------------------------------
// Hub 侧：开关、请求处理
// ---------------------------------------------------------------------------

// EnableRecordings 指定交易记录目录并读入已有列表。
//
// dir 为空 = 不落盘（单元测试）。返回的错误只影响「能不能保存」，
// 不影响服务起来 —— 目录不可写不该让整个复盘服务罢工。
func (h *Hub) EnableRecordings(dir string) error {
	st, err := OpenRecordingStore(dir, h.log)
	h.mu.Lock()
	h.records = st
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if n := len(st.List()); n > 0 {
		h.log("[replay] 交易记录目录 %s 里有 %d 条记录", st.Dir(), n)
	}
	return nil
}

// Records 供 HTTP 层（/api/recordings/...）取语音路径。
func (h *Hub) Records() *RecordingStore {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.records
}

// handleRecordStart / handleRecordStop 是**已经取消的两个手动开关**。
//
// v0.18.0 起交易记录是自动的（开仓开始、平仓结束），没有「现在开始录」这个动作。
// 这两个消息仍然认，但只回一句明确的解释 —— 直接删掉会让老前端/老脚本收到
// 「不认识的消息类型」，那看起来像 bug，而不像「这个功能换了形态」。
func (h *Hub) handleRecordStart(c *Client, req Request) {
	h.fail(c, req, "交易记录已经改为自动：开仓时自动开始、平仓时自动结束，不需要手动开关")
}

func (h *Hub) handleRecordStop(c *Client, req Request) {
	h.fail(c, req, "交易记录已经改为自动：开仓时自动开始、平仓时自动结束，不需要手动开关")
}

// handleRecordAudio / handleRecordAudioDelete 是语音讲解的**旧入口别名**。
//
// v0.16.0 起语音有自己的入口（voice_add / voice_clear），v0.18.0 起连「必须在录制中」
// 这个附加条件也没有了（那个状态不存在）。所以这两个消息现在**就是**语音的两个入口：
// 不再单独维护一条录音路径，落盘、额度、清空范围全部走 voice.go 那一套。
func (h *Hub) handleRecordAudio(c *Client, req Request) {
	h.handleVoiceAdd(c, req)
}

func (h *Hub) handleRecordAudioDelete(c *Client, req Request) {
	h.handleVoiceClear(c, req)
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
	h.log("[replay] 交易记录 %s 已删除", req.RecordingID)
	h.reply(c, RecordingsMsg{Type: MsgRecordings, Dir: h.records.Dir(), Recordings: h.records.List()})
}
