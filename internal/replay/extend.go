package replay

// 回放延伸（v0.27.0）：步数走完之后，把**后面真实的行情**逐根加载出来，并且能在
// 这些新加载的 K 线上录音。
//
// 为什么需要它：交易记录只记到平仓那一步。可复盘真正想问的下一句往往是
// 「我平早了没有？出去之后它往哪走了？」—— 那一段行情当时在盘面上是有的，
// 只是没被记进记录（记录记的是操作，不是行情）。延伸就是把这个缺口补上：
// 从记录里最后一根 K 线的时间往后，按数据目录里的真实 K 线一根一根放出来。
//
// 五条刻意的设计：
//
//  1. **加载即写入记录**。加载一根 = 记录里多一步（Kind = ActBarExtended），
//     立刻落盘。所以它不是「这次打开回放时的临时视图」：换台机器、重启程序、
//     隔一个月再打开，那几根 K 线还在，挂在上面的语音也还在。
//     代价是记录会变大（一根 K 线约 120 字节），换来的是「看得见的东西都存得住」。
//  2. **延伸步不碰交易状态**（见 protocol.go 的 ActBarExtended）：它只带
//     Bar / Cursor / HasMore。硬凑一份「当时的持仓」等于编造 —— 平仓之后本来
//     就没有仓位了，回放里的持仓不该因为多看了几根 K 线而被改写。
//  3. **只能在末尾加载**。中间插入一根会改掉后面所有步的 K 线时间与游标，
//     已经画在那些 K 线上的图形和语音会集体错位。所以服务端只认「最后一根」，
//     前端也只在那一步给按钮。
//  4. **录音走记录自己那条路**。回放里前端没有 join 任何实例（`rec:<id>` 是演出来的），
//     所以 voice_add 那套（以及它背后的 instance.voice）根本用不了。这里直接往
//     记录里加 AudioClip + 一个语音步 —— 形式和「复盘期间录的那条」完全一样
//     （Kind = ActRecordingAudio、每根 9 条、audio-N.wav 另存一份），
//     所以回放时它照样在那一根上、走到那一步就响。
//  5. **不改格式版本**。延伸步和语音步都是普通的一步，Format 仍然是 2：
//     老版本读到它只会显示成一个不认识的动作名（画面照样演得出来），
//     没有字段语义被改写，没必要让旧记录跟着升版本。
//
// 边界情况（都在下面的处理里，逐条对应）：
//
//	数据目录里没有更晚的 K 线 —— 回 ErrNoMoreBars 的原话，前端据此把按钮置灰
//	标的被删了 / 数据目录换了 —— resolveFolder 报「数据目录里没有标的 X」
//	那一天的日文件不在了 —— 从第一个更晚的日文件接着找，不报错
//	这笔交易还在进行中（草稿）—— 拒绝：收尾时 Save 会整份覆盖，延伸会白做
//	同一根 K 线被两条请求同时加载 —— 全程在 h.mu 里串行，第二次从最新的记录读，
//	                              拿到的是下一根（不会重复）

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"tradingview-j/internal/store"
)

const (
	// maxExtendBars 是一次请求最多能加载几根。前端只发 1（逐根），
	// 上限存在只是为了不让一条手写的消息把记录一次撑大几万根。
	maxExtendBars = 50

	// extendLookaheadDays 是「找下一根」时最多连续看几个日文件。
	//
	// 和 data.go 的 ensureFuture 用同一个数：长假（圣诞、春节）会连着七八个
	// 空文件，不能因为一个空文件就说「没有更多数据」；但也不能无上限地读，
	// 否则「数据到底还有没有」这个判断本身会变成几百次磁盘 IO。
	extendLookaheadDays = 40
)

// ---------------------------------------------------------------------------
// 记录的「末尾」：从记录本身还原盘面（不依赖实例）
// ---------------------------------------------------------------------------

// stepBar 取一步里那根 K 线。只有「推进/延伸」这两类步有。
func stepBar(st RecordingStep) (Bar, bool) {
	if st.Kind != ActBarAdvanced && st.Kind != ActBarExtended {
		return Bar{}, false
	}
	for _, ev := range st.Events {
		if len(ev.Payload) == 0 {
			continue
		}
		var p struct {
			Bar Bar `json:"bar"`
		}
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			continue
		}
		if p.Bar.Time != "" {
			return p.Bar, true
		}
	}
	return Bar{}, false
}

// playbackBars 把一条记录**演到末尾**时画布上应该有的 K 线全部还原出来。
//
// 口径和前端 buildState 严格一致（base.bars 打底，之后每个推进步追加一根）：
// 这里是服务端算 EMA20 用的序列，序列少一根或多一根，新加载那几根的指标就会
// 和实时盘面差一点 —— 而那种差异在图上是一条斜率略微不同的线，几乎查不出来。
func playbackBars(rec *Recording) []Bar {
	if rec == nil {
		return nil
	}
	out := make([]Bar, 0, len(rec.Base.Bars)+len(rec.Steps))
	out = append(out, rec.Base.Bars...)
	for _, st := range rec.Steps {
		if b, ok := stepBar(st); ok {
			out = append(out, b)
		}
	}
	return out
}

// playbackTail 返回记录末尾那根 K 线的时间、它在盘面上的下标、以及盘面一共几根。
//
// 空记录（连 Base 都没有）返回空串 / 0 / 0 —— 调用方据此报「这条记录没有 K 线」。
func playbackTail(rec *Recording) (last string, cursor int, count int) {
	if rec == nil {
		return "", 0, 0
	}
	count = len(rec.Base.Bars)
	cursor = rec.Base.Cursor
	if count > 0 {
		last = rec.Base.Bars[count-1].Time
	}
	for _, st := range rec.Steps {
		if _, ok := stepBar(st); !ok {
			continue
		}
		count++
		last = st.Time
		cursor = st.Cursor
	}
	if count == 0 {
		return "", 0, 0
	}
	if cursor < 0 || cursor >= count {
		cursor = count - 1
	}
	return last, cursor, count
}

// extensionTailStep 是记录「末尾新加载的那一根」——回放里唯一允许录音的地方。
//
// 找的是**最后一个延伸步**，而不是「最后一步」：在那根上录过音之后，最后一步
// 变成了语音步，可录音的位置并没有变（还是这根 K 线）。
//
// 返回 false 的两种情况都是正常状态，不是错误：
//
//	这条记录从来没有延伸过（回放只到平仓那一步）
//	延伸过，但后面又加载了更多根 —— 那就该录在最后那根上，不是这一根
func extensionTailStep(rec *Recording) (RecordingStep, bool) {
	if rec == nil {
		return RecordingStep{}, false
	}
	for i := len(rec.Steps) - 1; i >= 0; i-- {
		if rec.Steps[i].Kind == ActBarExtended {
			return rec.Steps[i], true
		}
	}
	return RecordingStep{}, false
}

// clipsOfBar 数一根 K 线上挂了几条语音（按**时间**，和 voice.go 同一口径）。
func clipsOfBar(clips []AudioClip, barTime string) []AudioClip {
	out := make([]AudioClip, 0, 2)
	for _, c := range clips {
		if c.Time == barTime {
			out = append(out, c)
		}
	}
	return out
}

// renumberRecording 在摘掉若干步之后把步号重排成 1..N，并让 AudioClip.Step 跟上。
//
// 和 tradeRecorder.renumber 是同一条规则、同一份口径（Step = 下标 + 1）：
// 那一条在交易进行中用，这一条在记录落盘之后用。两处现在都只有这一条规则，
// 前端（stores/playback.ts 的 renumberLocal）抄的也是它。
func renumberRecording(rec *Recording) {
	if rec == nil {
		return
	}
	steps := make(map[string]int, len(rec.Audios))
	for i := range rec.Steps {
		st := &rec.Steps[i]
		st.Step = i + 1
		if st.Audio != nil {
			st.Audio.Step = st.Step
			steps[st.Audio.ID] = st.Step
		}
	}
	for i := range rec.Audios {
		if n, ok := steps[rec.Audios[i].ID]; ok {
			rec.Audios[i].Step = n
		}
	}
}

// ---------------------------------------------------------------------------
// 数据源：从某一根之后接着读
// ---------------------------------------------------------------------------

// BarsAfter 返回 afterTime **之后**的至多 limit 根真实 K 线，以及「还有没有更多」。
//
// afterTime 为空 = 从目录里最早的那根开始（记录里连一根 K 线都没有时才会发生）。
//
// hasMore 的判定刻意保守：连续 extendLookaheadDays 个日文件里都没有更晚的行，
// 就当「到末尾了」—— 和 ensureFuture 的判据一致，否则一个长假结束之后
// 前端会以为还能继续加载，点了却一次次报错。
func (ds *DataSource) BarsAfter(folder, afterTime string, limit int) ([]Bar, bool, error) {
	if ds == nil {
		return nil, false, errNoDataSource
	}
	if limit <= 0 {
		limit = 1
	}
	if limit > maxExtendBars {
		limit = maxExtendBars
	}
	info, err := ds.resolveFolder(folder)
	if err != nil {
		return nil, false, err
	}
	dir := filepath.Join(ds.Root, info.Folder)
	files, err := store.DayFiles(dir)
	if err != nil {
		return nil, false, err
	}
	if len(files) == 0 {
		return nil, false, nil
	}

	after := strings.TrimSpace(afterTime)
	startDay := 0
	if after != "" {
		date, ok := anchorDate(after)
		if !ok {
			return nil, false, errBadBarTime(after)
		}
		startDay = indexOfFile(files, date+".csv")
		if startDay < 0 {
			// 那一天的日文件已经不在了（数据被清理过、或者记录来自另一份数据）：
			// 从第一个**更晚**的文件接着找。这里不报错 —— 用户的意图是
			// 「往后看到后面几根」，而不是「校验这一天的文件还在不在」。
			startDay = sort.SearchStrings(files, date+".csv")
		}
	}

	out := make([]Bar, 0, limit)
	last := after
	for d := startDay; d < len(files) && len(out) < limit; d++ {
		rows, _, rerr := store.ReadDayRows(filepath.Join(dir, files[d]), ds.Loc)
		if rerr != nil {
			continue
		}
		for _, r := range rows {
			if r.Time <= last {
				continue
			}
			out = append(out, toBar(r))
			if len(out) >= limit {
				break
			}
		}
	}
	if len(out) > 0 {
		last = out[len(out)-1].Time
	}

	hasMore := false
	for d := startDay; d < len(files) && d < startDay+extendLookaheadDays && !hasMore; d++ {
		rows, _, rerr := store.ReadDayRows(filepath.Join(dir, files[d]), ds.Loc)
		if rerr != nil {
			continue
		}
		for _, r := range rows {
			if r.Time > last {
				hasMore = true
				break
			}
		}
	}
	return out, hasMore, nil
}

func errBadBarTime(t string) error {
	return &extendError{msg: "记录里最后一根 K 线的时间 " + t + " 格式不对（要形如 2026-10-16 12:00）"}
}

// errNoDataSource 只在 Hub 没有数据源时出现（正常启动一定有）。
var errNoDataSource = &extendError{msg: "没有配置数据目录，没法往后加载 K 线"}

// extendError 是这几条请求专用的错误：文案本身就是给用户看的那句话，
// 不套 fmt.Errorf 的「操作失败：」前缀（那些前缀在界面上只是噪音）。
type extendError struct{ msg string }

func (e *extendError) Error() string { return e.msg }

// ---------------------------------------------------------------------------
// Hub 侧：三条请求
// ---------------------------------------------------------------------------

// handleRecordingExtend 把记录末尾之后的一根（或几根）K 线加载出来并写进记录。
func (h *Hub) handleRecordingExtend(c *Client, req Request) {
	rec, ok := h.loadExtendableRecording(c, req)
	if !ok {
		return
	}
	n := req.Count
	if n <= 0 {
		n = 1
	}
	if n > maxExtendBars {
		n = maxExtendBars
	}

	last, _, count := playbackTail(rec)
	bars, hasMore, err := h.ds.BarsAfter(rec.Folder, last, n)
	if err != nil {
		h.fail(c, req, err.Error())
		return
	}
	if len(bars) == 0 {
		h.fail(c, req, ErrNoMoreBars.Error())
		return
	}

	// EMA20 必须按**整条序列**重算：只喂新增那几根的话，前 19 根没有种子、
	// 递推起点也错，指标线会和实时盘面在同一个位置给出两个数。
	all := append(playbackBars(rec), bars...)
	computeIndicators(all)
	tail := all[len(all)-len(bars):]

	now := time.Now()
	newSteps := make([]RecordingStep, 0, len(tail))
	for _, b := range tail {
		payload := BarExtendedPayload{Bar: b, Cursor: count, Time: b.Time, HasMore: hasMore}
		ev := RecordingEvent{Seq: 0, At: now.Unix(), By: c.Name, Action: ActBarExtended}
		if raw, merr := json.Marshal(payload); merr == nil {
			ev.Payload = raw
		}
		newSteps = append(newSteps, RecordingStep{
			// 步号必须在这里就写对：它会被原样发回前端（步进条立刻多一格），
			// 也会随 recording.json 落盘（下次打开「第 N 步」不能是 0）。
			Step:   len(rec.Steps) + len(newSteps) + 1,
			At:     now.UnixMilli(),
			Kind:   ActBarExtended,
			By:     c.Name,
			Time:   b.Time,
			Cursor: count,
			Events: []RecordingEvent{ev},
		})
		count++
	}
	rec.Steps = append(rec.Steps, newSteps...)
	renumberRecording(rec)
	rec.refresh()

	sum, err := h.records.UpdateRecording(rec)
	if err != nil {
		// 写不进磁盘就不该让前端以为加载成功了：下一次打开回放那几根会凭空消失。
		h.fail(c, req, "这根 K 线没能存进记录："+err.Error())
		return
	}
	h.reply(c, RecordingExtendedMsg{
		Type:        MsgRecordingExtended,
		RecordingID: rec.ID,
		Steps:       newSteps,
		Summary:     sum,
		HasMore:     hasMore,
	})
	h.log("[replay] 记录 %s 延伸加载 %d 根（到 %s，数据还有更多：%v）",
		rec.ID, len(newSteps), tail[len(tail)-1].Time, hasMore)
}

// handleRecordingVoiceAdd 在「末尾新加载的那根」上录一段语音。
//
// 校验和复盘期间录音完全同一套：每根 9 条、单段 2MB、必须是 RIFF/WAVE
// （checkAudioClip 是同一份实现）。区别只在落点 —— 这里没有实例，
// 语音只有记录这一份（回放时的地址就是 /api/recordings/<id>/audio/audio-N.wav）。
func (h *Hub) handleRecordingVoiceAdd(c *Client, req Request) {
	rec, ok := h.loadExtendableRecording(c, req)
	if !ok {
		return
	}
	tail, ok := extensionTailStep(rec)
	if !ok {
		h.fail(c, req, "只有回放末尾新加载的 K 线能录音：先点【加载下一根】")
		return
	}
	if n := len(clipsOfBar(rec.Audios, tail.Time)); n >= maxClipsPerBar {
		h.fail(c, req, fmt.Sprintf("一根 K 线最多 %d 段录音，这根已经录满了；再往后加载一根就能接着录", maxClipsPerBar))
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
	durationMs, err := checkAudioClip(data, req.DurationMs)
	if err != nil {
		h.fail(c, req, err.Error())
		return
	}

	seq := maxAudioNo(rec.Audios) + 1
	clip := AudioClip{
		ID:         "au" + strconv.Itoa(seq),
		Step:       len(rec.Steps) + 1, // 这一步马上就建，步号先占住
		Time:       tail.Time,
		DurationMs: durationMs,
		File:       "audio-" + strconv.Itoa(seq) + ".wav",
		Size:       int64(len(data)),
		CreatedAt:  time.Now().Unix(),
		By:         c.Name,
	}
	// 先写音频再改 JSON：反过来的话，先落盘的记录会指向一个不存在的文件，
	// 回放到那一步就是「点开 404」。写失败直接报错，不留下一条听不到的语音。
	if err := h.records.WriteAudio(rec.ID, clip.File, data); err != nil {
		h.fail(c, req, err.Error())
		return
	}
	audio := clip
	step := RecordingStep{
		At:     time.Now().UnixMilli(),
		Kind:   ActRecordingAudio,
		By:     c.Name,
		Time:   tail.Time,
		Cursor: tail.Cursor,
		Audio:  &audio,
		Events: []RecordingEvent{},
	}
	rec.Steps = append(rec.Steps, step)
	rec.Audios = append(rec.Audios, clip)
	renumberRecording(rec)
	// 回给前端的那一份要从**记录里**重新取：step 是值拷贝，renumberRecording
	// 改的是切片里那一个 —— 不重取的话消息里的步号是 0，前端会跳到第 1 步去。
	step = rec.Steps[len(rec.Steps)-1]
	audio = clip
	rec.refresh()

	sum, err := h.records.UpdateRecording(rec)
	if err != nil {
		h.fail(c, req, "语音写进了记录目录，但 recording.json 没更新成："+err.Error())
		return
	}
	h.reply(c, RecordingVoiceMsg{
		Type:        MsgRecordingVoice,
		RecordingID: rec.ID,
		Audio:       &clip,
		Step:        &step,
		Summary:     sum,
	})
	h.log("[replay] 记录 %s 在 %s 这根上录了一段（%d 毫秒）", rec.ID, clip.Time, durationMs)
}

// handleRecordingVoiceClear 清空「末尾新加载的那根」上的全部语音。
//
// 范围是**一根 K 线**（和复盘里的【清空本根】同一条规则）。语音自己占一步，
// 所以删语音 = 删步 + 重排步号（见 renumberRecording）—— 留着那些空步，
// 回放时会走到几步「什么都不发生」的空档，看起来像卡住了。
func (h *Hub) handleRecordingVoiceClear(c *Client, req Request) {
	rec, ok := h.loadExtendableRecording(c, req)
	if !ok {
		return
	}
	tail, ok := extensionTailStep(rec)
	if !ok {
		h.fail(c, req, "只有回放末尾新加载的 K 线能录音：先点【加载下一根】")
		return
	}
	removed := clipsOfBar(rec.Audios, tail.Time)
	if len(removed) == 0 {
		h.fail(c, req, "这根 K 线上还没有录音")
		return
	}
	gone := make(map[string]bool, len(removed))
	for _, a := range removed {
		gone[a.ID] = true
		if dir := h.records.Dir(); dir != "" {
			path := filepath.Join(dir, recordingFolder(rec.ID), a.File)
			if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
				h.log("[replay] 语音 %s 删不掉：%v", a.File, err)
			}
		}
	}
	steps := rec.Steps[:0]
	for _, st := range rec.Steps {
		if st.Kind == ActRecordingAudio && st.Audio != nil && gone[st.Audio.ID] {
			continue
		}
		steps = append(steps, st)
	}
	rec.Steps = steps
	clips := rec.Audios[:0]
	for _, a := range rec.Audios {
		if gone[a.ID] {
			continue
		}
		clips = append(clips, a)
	}
	rec.Audios = clips
	renumberRecording(rec)
	rec.refresh()

	sum, err := h.records.UpdateRecording(rec)
	if err != nil {
		h.fail(c, req, "语音文件已删，但 recording.json 没更新成："+err.Error())
		return
	}
	h.reply(c, RecordingVoiceMsg{
		Type:        MsgRecordingVoice,
		RecordingID: rec.ID,
		Removed:     removed,
		Summary:     sum,
	})
	h.log("[replay] 记录 %s 清掉了 %s 上的 %d 条语音", rec.ID, tail.Time, len(removed))
}

// loadExtendableRecording 是三条请求共用的入口：读记录 + 四道前置检查。
//
// 四道检查都要有，而且都要说清**为什么**（用户看到的是一句人话）：
//
//	没有记录目录   —— 延伸/录音都是要落盘的，不落盘就失去意义
//	记录不存在     —— 可能刚被另一个页面删了
//	草稿还在录     —— 收尾时 Save 会整份覆盖，这会儿的延伸会被静默抹掉
//	记录里没有 K 线 —— 连「从哪一根往后」都算不出来
func (h *Hub) loadExtendableRecording(c *Client, req Request) (*Recording, bool) {
	id := strings.TrimSpace(req.RecordingID)
	if id == "" {
		h.fail(c, req, "没有指定是哪一条交易记录")
		return nil, false
	}
	if h.records == nil || !h.records.Enabled() {
		h.fail(c, req, "没有配置交易记录目录（config.json 的 replayRecordingDir），加载的 K 线和语音都没处存")
		return nil, false
	}
	if h.records.isLive(id) {
		h.fail(c, req, "这笔交易还在进行中：等它平仓收尾之后再往回到放里加载 K 线")
		return nil, false
	}
	rec, err := h.records.Load(id)
	if err != nil {
		h.fail(c, req, err.Error())
		return nil, false
	}
	if last, _, count := playbackTail(rec); count == 0 || last == "" {
		h.fail(c, req, "这条记录里一根 K 线都没有，没法往后加载")
		return nil, false
	}
	return rec, true
}
