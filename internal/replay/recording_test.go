package replay

// 录制与回放的回归测试。
//
// 这里钉住四件事：
//
//  1. **一步 = 一次操作**（v0.15.0）：第 1 步 = 开始录制时那一屏，之后每一条放行的
//     广播各占一步 —— 同一根 K 线上画两个图形就是两步，不会攒成一步一次性蹦出来。
//  2. **录的是操作，不是画面**：步号连续、Kind 对得上、步里就一条事件；
//     录音自己占一步（Events 为空、Audio 挂在步上）。
//  3. **机器人走的是同一条通道**：机器人不经过任何后门，它的操作会以 by="bot"
//     出现在同一份录像里 —— 这条用例就是需求里「验证前端用户操作与后端机器人操作
//     完全等价」的机器化版本：把录像重放一遍，最终状态必须和实例一模一样。
//  4. **录音按 K 线时间挂载、同一根最多 9 条**，并且音频文件能被 HTTP 路由正确取回。

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testWAV 造一段合法的 WAV（RIFF/WAVE + 16bit 单声道静音）。
// 后端只校验文件头，但测试里给真数据，将来加解码也不会立刻失效。
func testWAV(samples int) []byte {
	dataLen := samples * 2
	buf := make([]byte, 44+dataLen)
	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataLen))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16)
	binary.LittleEndian.PutUint16(buf[20:22], 1) // PCM
	binary.LittleEndian.PutUint16(buf[22:24], 1) // 单声道
	binary.LittleEndian.PutUint32(buf[24:28], 16000)
	binary.LittleEndian.PutUint32(buf[28:32], 32000) // 字节率
	binary.LittleEndian.PutUint16(buf[32:34], 2)
	binary.LittleEndian.PutUint16(buf[34:36], 16)
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataLen))
	return buf
}

func sendAudio(t *testing.T, hub *Hub, fc *fakeClient, instID string, wav []byte) map[string]any {
	t.Helper()
	fc.send(hub, map[string]any{
		"type":       "record_audio",
		"instanceId": instID,
		"audioData":  base64.StdEncoding.EncodeToString(wav),
		"durationMs": 1000,
	})
	return fc.waitAction(ActRecordingAudio)
}

// snippetForLog 把一段 JSON 截短，失败信息里不要把整份录像（几百根 K 线）都打出来。
// maxInt 只给测试用：生产代码里不再需要它（v0.15.0 起 Bars 靠数 Kind 算，
// 不再用「基础根数 + 步数 - 1」那种估法），所以放在测试文件里。
func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func snippetForLog(raw []byte) string {
	const max = 400
	if len(raw) <= max {
		return string(raw)
	}
	return string(raw[:max]) + "…（已截断）"
}

// TestRecordingCapturesOpsAndReplays 是主用例：录一段「真人 + 机器人混着来」的操作，
// 再把它重放出来，逐项比对最终状态。
//
// 步序表（v0.15.0 起一步一个操作，所以这张表本身就是断言）：
//
//	1 base          开始录制那一屏
//	2 bar_advanced  真人点【下一根】
//	3 annotation_added  真人画射线      ← 同一根 K 线
//	4 order_placed      真人挂限价单    ← 同一根 K 线
//	5 annotation_added  机器人画趋势线
//	6 bar_advanced      机器人步进
//	7 recording_audio   真人录一段（不改盘面）
//	8 bar_advanced      真人再点【下一根】
func TestRecordingCapturesOpsAndReplays(t *testing.T) {
	hub, _ := newTestHub(t, 3)
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用录像目录失败：%v", err)
	}

	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	// 1. 开始录制：第 1 步就是当前这一屏
	a.drain()
	a.send(hub, map[string]any{"type": "record_start", "instanceId": instID})
	started := a.waitAction(ActRecordingStarted)
	state := started["payload"].(map[string]any)
	if state["active"] != true || state["step"].(float64) != 1 {
		t.Fatalf("开始录制后的状态不对：%v", state)
	}
	if got := int(state["total"].(float64)); got != 1 {
		t.Fatalf("刚开始录制时应当只有 1 步（起点），实际 %d", got)
	}
	if state["startTime"] != inst.Bars[inst.Cursor].Time {
		t.Fatalf("第 1 步应当对应当前 K 线 %s，实际 %v", inst.Bars[inst.Cursor].Time, state["startTime"])
	}

	// 2. 真人步进 → 第 2 步。步号由服务端随消息下发（前端不再自己 +1）
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	adv := a.waitAction(ActBarAdvanced)
	if got := int(adv["recStep"].(float64)); got != 2 {
		t.Fatalf("点【下一根】应当是第 2 步，实际 %d", got)
	}

	nowBar := inst.Bars[inst.Cursor]
	prevBar := inst.Bars[inst.Cursor-1]
	a.send(hub, map[string]any{
		"type": "annotation_add", "instanceId": instID, "kind": AnnRay,
		"start": map[string]any{"time": prevBar.Time, "price": prevBar.Close},
		"end":   map[string]any{"time": nowBar.Time, "price": nowBar.High},
	})
	if got := int(a.waitAction(ActAnnotationAdded)["recStep"].(float64)); got != 3 {
		t.Fatalf("画完一个图形应当是第 3 步（和第 2 步是同一条 K 线，但必须是独立的一步），实际 %d", got)
	}
	a.send(hub, map[string]any{
		"type": "order_place", "instanceId": instID,
		"side": "buy", "kind": KindLimit, "price": nowBar.Low - 1, "quantity": 2,
	})
	if got := int(a.waitAction(ActOrderPlaced)["recStep"].(float64)); got != 4 {
		t.Fatalf("挂单应当是第 4 步，实际 %d", got)
	}

	// 3. 机器人操作：画一条 + 步进（和机器人 tick 完全相同的那几条路径）→ 第 5、6 步
	hub.mu.Lock()
	hub.botTickProbeLocked(inst)
	hub.mu.Unlock()
	if got := inst.rec.state().Step; got != 6 {
		t.Fatalf("机器人画图 + 步进之后应当是第 6 步，实际 %d", got)
	}

	// 4. 录音挂到**当前**这根 K 线上，并且自己占一步
	audioBar := inst.Bars[inst.Cursor]
	audioEv := sendAudio(t, hub, a, instID, testWAV(400))
	if got := int(audioEv["recStep"].(float64)); got != 7 {
		t.Fatalf("录音应当是第 7 步（录音本身就是一次操作），实际 %d", got)
	}
	audioPayload := audioEv["payload"].(map[string]any)
	clip := audioPayload["audio"].(map[string]any)
	if clip["time"] != audioBar.Time {
		t.Fatalf("录音应当挂在当前 K 线 %s 上，实际 %v", audioBar.Time, clip["time"])
	}
	if clip["step"].(float64) != 7 {
		t.Fatalf("录音应当挂在第 7 步，实际 %v", clip["step"])
	}

	// 5. 再步进一次 → 第 8 步
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)

	// 6. 结束录制
	a.send(hub, map[string]any{"type": "record_stop", "instanceId": instID})
	stopped := a.waitAction(ActRecordingStopped)
	stopPayload := stopped["payload"].(map[string]any)
	if stopPayload["error"] != nil && stopPayload["error"] != "" {
		t.Fatalf("录像保存失败：%v", stopPayload["error"])
	}
	sum := stopPayload["recording"].(map[string]any)
	recID, _ := sum["id"].(string)
	if recID == "" {
		t.Fatalf("结束后没有录像 id：%v", sum)
	}
	if got := int(sum["stepCount"].(float64)); got != 8 {
		t.Fatalf("录像应当是 8 步（1 起点 + 7 次操作），实际 %v", got)
	}
	if got := int(sum["bars"].(float64)); got != 4 {
		t.Fatalf("录像里推进了 4 根 K 线（起点 + 3 次步进），实际 %v", got)
	}
	if got := int(sum["eventCount"].(float64)); got != 6 {
		t.Fatalf("应当有 6 个改盘面的操作（起点和录音步不算），实际 %v", got)
	}
	if got := sum["audioCount"].(float64); got != 1 {
		t.Fatalf("录像应当有 1 条录音，实际 %v", got)
	}

	// 7. 落盘结构：recording.json + audio-1.wav
	folder := filepath.Join(recDir, recordingFolder(recID))
	recPath := filepath.Join(folder, recordingFileName)
	if _, err := os.Stat(recPath); err != nil {
		t.Fatalf("录像没有落盘（%s）：%v", recPath, err)
	}
	if _, err := os.Stat(filepath.Join(folder, "audio-1.wav")); err != nil {
		t.Fatalf("录音没有落盘：%v", err)
	}

	// 8. 列表 / 取详情
	a.send(hub, map[string]any{"type": "list_recordings"})
	list := a.waitFor(MsgRecordings)
	items := list["recordings"].([]any)
	if len(items) != 1 {
		t.Fatalf("录像列表应当有 1 条，实际 %d", len(items))
	}
	a.send(hub, map[string]any{"type": "get_recording", "recordingId": recID})
	got := a.waitFor(MsgRecording)
	raw, _ := json.Marshal(got["recording"])
	var rec Recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("录像反序列化失败：%v", err)
	}
	if rec.Format != RecordingFormat {
		t.Fatalf("新录的录像应当是 format=%d，实际 %d", RecordingFormat, rec.Format)
	}
	if len(rec.Steps) != 8 {
		t.Fatalf("读回来的录像应当有 8 步，实际 %d", len(rec.Steps))
	}
	// 数组字段必须序列化成 `[]` 而不是 `null`：起点和录音步天然没有事件，前端步进条
	// 直接读 `s.events.length`，null 会让「打开回放」当场抛 TypeError
	// （2026-10 实测：冒烟时整个播放器渲染不出来，顶栏却已经进了回放态）。
	if !strings.Contains(string(raw), `"events":[]`) {
		t.Fatalf("events 不能是 null，必须是 []（原始 JSON 里找不到 \"events\":[]）：%s", snippetForLog(raw))
	}
	if !strings.Contains(string(raw), `"audios":[`) {
		t.Fatalf("audios 不能是 null，必须是数组：%s", snippetForLog(raw))
	}
	if len(rec.Audios) != 1 || rec.Audios[0].Time != audioBar.Time {
		t.Fatalf("读回来的录音不对：%+v", rec.Audios)
	}

	// 9. 步序表：步号连续、Kind 一一对应、每步只有一条事件（起点/录音步没有事件）
	wantKind := []string{
		StepBase, ActBarAdvanced, ActAnnotationAdded, ActOrderPlaced,
		ActAnnotationAdded, ActBarAdvanced, ActRecordingAudio, ActBarAdvanced,
	}
	for i, st := range rec.Steps {
		if st.Step != i+1 {
			t.Fatalf("第 %d 个元素的步号应当是 %d，实际 %d（步号必须连续）", i, i+1, st.Step)
		}
		if i < len(wantKind) && st.Kind != wantKind[i] {
			t.Fatalf("第 %d 步的类型应当是 %s，实际 %s", i+1, wantKind[i], st.Kind)
		}
		if st.Time == "" {
			t.Fatalf("第 %d 步没有盘面锚点（K 线时间）", st.Step)
		}
		if st.At == 0 {
			t.Fatalf("第 %d 步没有真实时刻（At）", st.Step)
		}
		switch st.Kind {
		case StepBase, ActRecordingAudio:
			if len(st.Events) != 0 {
				t.Fatalf("%s 这一步不该有事件：%+v", st.Kind, st.Events)
			}
		default:
			if len(st.Events) != 1 {
				t.Fatalf("第 %d 步（%s）应当正好 1 条事件，实际 %d 条", st.Step, st.Kind, len(st.Events))
			}
		}
	}
	if rec.Steps[6].Audio == nil || rec.Steps[6].Audio.ID != rec.Audios[0].ID {
		t.Fatalf("第 7 步应当挂着那段录音，实际 %+v", rec.Steps[6].Audio)
	}
	if rec.Steps[2].Events[0].By != "human" || rec.Steps[2].Events[0].Action != ActAnnotationAdded {
		t.Fatalf("第 3 步应当是真人画的那条射线：%+v", rec.Steps[2].Events[0])
	}
	if rec.Steps[4].Events[0].By != "bot" {
		t.Fatalf("第 5 步应当是机器人画的线（机器人走同一出口，没有后门）：%+v", rec.Steps[4].Events[0])
	}
	// 第 3、4 步是同一条 K 线上的两次操作 —— 这正是 v0.15.0 要的形状
	if rec.Steps[2].Time != rec.Steps[3].Time {
		t.Fatalf("第 3、4 步应当在同一条 K 线上（%s / %s）", rec.Steps[2].Time, rec.Steps[3].Time)
	}

	// 10. **重放等价**：Base + 事件演一遍，最终状态必须和实例完全一致
	replayed := replayRecording(t, &rec)
	assertSameState(t, inst, replayed)
	// 逐步重放：第 N 步之后，游标必须等于录像里记的那一个 —— 跳步不会演错的前提
	for _, st := range rec.Steps {
		partial := replaySteps(t, &rec, st.Step)
		if partial.Cursor != st.Cursor {
			t.Fatalf("演到第 %d 步时游标应当是 %d，实际 %d", st.Step, st.Cursor, partial.Cursor)
		}
	}

	// 11. 音频能通过 HTTP 路由取回
	srv := httptest.NewServer(NewHandler(hub, t.TempDir()))
	defer srv.Close()
	url := fmt.Sprintf("%s/api/recordings/%s/audio/audio-1.wav", srv.URL, recID)
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("取录音失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("录音应当 200，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "audio/wav" {
		t.Fatalf("录音的 Content-Type 应当是 audio/wav，实际 %q", ct)
	}
	// 路径穿越要被挡住
	bad, err := http.Get(fmt.Sprintf("%s/api/recordings/%s/audio/..%%2Frecording.json", srv.URL, recID))
	if err == nil {
		defer bad.Body.Close()
		if bad.StatusCode == http.StatusOK {
			t.Fatal("不该能通过 audio 路由读到 recording.json")
		}
	}

	// 12. 删除：列表清空、文件夹消失
	a.send(hub, map[string]any{"type": "delete_recording", "recordingId": recID})
	after := a.waitFor(MsgRecordings)
	if n := len(after["recordings"].([]any)); n != 0 {
		t.Fatalf("删除后列表应当是空的，实际 %d 条", n)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("删除后录像目录应当没了，实际 err=%v", err)
	}
}

// TestRecordingStepsFollowRealOperations 是需求原话的形状：
// 「点击下一根 K 线」→「绘制图形」→「录音」→「再次绘制图形」，**四次操作 = 四步**。
//
// v0.14 及以前这四件事全落在同一根 K 线的那一步里，回放时一次性摆出来，
// 看不出用户先画了线、再讲了话、又画了一条。这条用例把「逐步出现」钉死：
// 演到第 3 步只有第一个图形，演到第 5 步才有第二个。
func TestRecordingStepsFollowRealOperations(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	if err := hub.EnableRecordings(t.TempDir()); err != nil {
		t.Fatalf("启用录像目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	a.send(hub, map[string]any{"type": "record_start", "instanceId": instID})
	a.waitAction(ActRecordingStarted)

	// 1) 点击下一根
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	bar := inst.Bars[inst.Cursor]

	// 2) 绘制图形
	addRay := func(price float64) {
		a.send(hub, map[string]any{
			"type": "annotation_add", "instanceId": instID, "kind": AnnRay,
			"start": map[string]any{"time": bar.Time, "price": price},
			"end":   map[string]any{"time": bar.Time, "price": price},
		})
		a.waitAction(ActAnnotationAdded)
	}
	addRay(bar.Close)

	// 3) 录音
	ev := sendAudio(t, hub, a, instID, testWAV(64))
	if got := int(ev["recStep"].(float64)); got != 4 {
		t.Fatalf("录音应当是第 4 步，实际 %d", got)
	}

	// 4) 再次绘制图形（同一根 K 线）
	addRay(bar.Open)

	steps := inst.rec.rec.Steps
	want := []string{StepBase, ActBarAdvanced, ActAnnotationAdded, ActRecordingAudio, ActAnnotationAdded}
	if len(steps) != len(want) {
		t.Fatalf("四次操作应当录成 5 步（含起点），实际 %d 步：%+v", len(steps), steps)
	}
	for i, k := range want {
		if steps[i].Kind != k {
			t.Fatalf("第 %d 步应当是 %s，实际 %s", i+1, k, steps[i].Kind)
		}
	}
	// 四次操作都发生在同一根 K 线上（除了第 2 步本身就是换根）
	for _, st := range steps[1:] {
		if st.Time != bar.Time {
			t.Fatalf("这几步都该挂在 %s 上，第 %d 步却是 %s", bar.Time, st.Step, st.Time)
		}
	}
	if len(steps[3].Events) != 0 || steps[3].Audio == nil {
		t.Fatalf("录音步不该有事件、但必须带着那段音频：%+v", steps[3])
	}

	// 逐步出现：第 3 步只有 1 个图形，第 4 步还只有 1 个（录音不改盘面），第 5 步才有 2 个
	if n := len(replaySteps(t, inst.rec.rec, 3).Annotations); n != 1 {
		t.Fatalf("演到第 3 步应当只有 1 个图形，实际 %d", n)
	}
	if n := len(replaySteps(t, inst.rec.rec, 4).Annotations); n != 1 {
		t.Fatalf("录音那一步不该改变盘面（还是 1 个图形），实际 %d", n)
	}
	if n := len(replaySteps(t, inst.rec.rec, 5).Annotations); n != 2 {
		t.Fatalf("演到第 5 步应当有 2 个图形，实际 %d", n)
	}
}

// TestRecordingAudioLimit 钉住「**同一根 K 线**最多 9 条，整条录像不限」和「必须是 WAV」。
//
// 这条限制的用处是防止用户在同一根 K 线上反复重录；换到下一根 K 线，
// 额度就该重新从 0 开始 —— 所以下面第 3 段要明确验「步进之后又能录」。
//
// 同时钉住 v0.15.0 的新口径：**每段录音自己占一步**（录音是一次操作），
// 但额度按 K 线数，不按步数 —— 否则同一根 K 线上画几个图形就会莫名其妙少几条额度。
func TestRecordingAudioLimit(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	if err := hub.EnableRecordings(t.TempDir()); err != nil {
		t.Fatalf("启用录像目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")

	a.send(hub, map[string]any{"type": "record_start", "instanceId": instID})
	a.waitAction(ActRecordingStarted)

	// 不是 WAV 的二进制不收（先验这条，否则录满之后只会先报「已经录满了」）
	a.send(hub, map[string]any{
		"type": "record_audio", "instanceId": instID,
		"audioData": base64.StdEncoding.EncodeToString([]byte("not a wav file at all................")),
	})
	if e := a.waitFor(MsgError); !strings.Contains(fmt.Sprint(e["message"]), "WAV") {
		t.Fatalf("非 WAV 数据应当被拒，实际 %v", e["message"])
	}

	// 同一根 K 线上灌满 9 条：计数按「本根 K 线」走，步数按「一条一步」走
	wav := testWAV(64)
	for i := 0; i < maxAudiosPerBar; i++ {
		ev := sendAudio(t, hub, a, instID, wav)
		st := ev["payload"].(map[string]any)["state"].(map[string]any)
		if int(st["barAudios"].(float64)) != i+1 {
			t.Fatalf("第 %d 条录音后的**本根**计数不对：%v", i+1, st["barAudios"])
		}
		if int(st["audios"].(float64)) != i+1 {
			t.Fatalf("第 %d 条录音后的总数不对：%v", i+1, st["audios"])
		}
		// 起点 + i 条录音 = 第 i+2 步
		if got := int(st["step"].(float64)); got != i+2 {
			t.Fatalf("第 %d 段录音应当自己占第 %d 步，实际第 %d 步", i+1, i+2, got)
		}
		if got := st["kind"].(string); got != ActRecordingAudio {
			t.Fatalf("录音步的 kind 应当是 %s，实际 %s", ActRecordingAudio, got)
		}
		if n := len(st["clips"].([]any)); n != i+1 {
			t.Fatalf("state 里应当带着本根的 %d 条录音，实际 %d", i+1, n)
		}
	}

	// 第 10 条：明确报错，而不是静默丢掉
	a.drain()
	a.send(hub, map[string]any{
		"type": "record_audio", "instanceId": instID,
		"audioData": base64.StdEncoding.EncodeToString(wav),
	})
	e := a.waitFor(MsgError)
	if msg, _ := e["message"].(string); msg == "" || !strings.Contains(msg, "最多") {
		t.Fatalf("超限时应当明确报错，实际 %v", e["message"])
	}

	// 步进一次 → 新的一根 K 线，额度重新开始（这就是「整条录像不限」）
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	inst := hub.mustInstance(t, instID)
	st := inst.rec.state()
	if st.BarNo != 2 {
		t.Fatalf("步进之后应当是第 2 根 K 线，实际 %d", st.BarNo)
	}
	if st.Step != 11 {
		t.Fatalf("步进之后应当是第 11 步（起点 + 9 段录音 + 1 次步进），实际 %d", st.Step)
	}
	ev := sendAudio(t, hub, a, instID, wav)
	payload := ev["payload"].(map[string]any)
	got := payload["state"].(map[string]any)
	if n := int(got["barAudios"].(float64)); n != 1 {
		t.Fatalf("新的一根应当从 1 开始计数，实际 %d", n)
	}
	if n := int(got["audios"].(float64)); n != maxAudiosPerBar+1 {
		t.Fatalf("整条录像的总数应当继续累加（%d），实际 %d", maxAudiosPerBar+1, n)
	}
	if n := int(got["step"].(float64)); n != 12 {
		t.Fatalf("换了一根再录，应当是第 12 步，实际 %d", n)
	}
	if n := len(hub.mustInstance(t, instID).rec.audios); n != maxAudiosPerBar+1 {
		t.Fatalf("内存里应当有 %d 条录音，实际 %d", maxAudiosPerBar+1, n)
	}
}

// TestRecordingClearBarAudio 钉住「一键清空当前这根 K 线的录音」：
// 只清本根、磁盘上的文件也删掉、**那几步跟着一起摘掉且步号重排**、清完还能接着录。
func TestRecordingClearBarAudio(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用录像目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	a.send(hub, map[string]any{"type": "record_start", "instanceId": instID})
	a.waitAction(ActRecordingStarted)

	wav := testWAV(64)
	sendAudio(t, hub, a, instID, wav)
	ev := sendAudio(t, hub, a, instID, wav)
	if got := int(ev["recStep"].(float64)); got != 3 {
		t.Fatalf("第 2 段录音应当是第 3 步（起点 + 两条录音），实际 %d", got)
	}

	// 录制中音频已经落盘（HUD 的预览播放要靠它），所以此刻文件就该在
	inst := hub.mustInstance(t, instID)
	dir := inst.rec.dir
	for _, name := range []string{"audio-1.wav", "audio-2.wav"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("录制中 %s 就该在磁盘上（预览播放要用）：%v", name, err)
		}
	}

	// 清空本根
	a.drain()
	a.send(hub, map[string]any{"type": "record_audio_delete", "instanceId": instID})
	ev = a.waitAction(ActRecordingAudioCleared)
	p := ev["payload"].(map[string]any)
	if n := len(p["removed"].([]any)); n != 2 {
		t.Fatalf("应当删掉 2 条，实际 %d", n)
	}
	st := p["state"].(map[string]any)
	if got := int(st["barAudios"].(float64)); got != 0 {
		t.Fatalf("清空后本根计数应当是 0，实际 %d", got)
	}
	if n := len(st["clips"].([]any)); n != 0 {
		t.Fatalf("清空后 state 里不该还有录音，实际 %d 条", n)
	}
	// 录音步被一起摘掉：只剩起点那一步
	if got := int(st["total"].(float64)); got != 1 {
		t.Fatalf("清空后应当只剩起点 1 步（录音步跟着删了），实际 %d", got)
	}
	for _, name := range []string{"audio-1.wav", "audio-2.wav"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("清空后 %s 应该被删掉（err=%v）", name, err)
		}
	}

	// 换一根继续录：清空只影响那一根，别的 K 线不受影响
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	ev = sendAudio(t, hub, a, instID, wav)
	if got := int(ev["recStep"].(float64)); got != 3 {
		t.Fatalf("重排之后：起点 + 步进 + 录音 = 第 3 步，实际 %d", got)
	}

	// 清完还能接着录（同一根 K 线上的额度也回来了）
	a.send(hub, map[string]any{
		"type": "record_audio", "instanceId": instID,
		"audioData": base64.StdEncoding.EncodeToString(wav),
	})
	ev = a.waitAction(ActRecordingAudio)
	got := ev["payload"].(map[string]any)["state"].(map[string]any)
	if n := int(got["barAudios"].(float64)); n != 2 {
		t.Fatalf("第 2 根 K 线上应当有 2 条录音，实际 %d", n)
	}
	if n := int(got["step"].(float64)); n != 4 {
		t.Fatalf("第 4 步应当是第 2 根上的第 2 段录音，实际 %d", n)
	}

	// 落盘之后：audios 只剩后面那两条，被清掉的不复活，步号与录音步号一致
	a.send(hub, map[string]any{"type": "record_stop", "instanceId": instID})
	stop := a.waitAction(ActRecordingStopped)
	id := stop["payload"].(map[string]any)["recording"].(map[string]any)["id"].(string)
	a.send(hub, map[string]any{"type": "get_recording", "recordingId": id})
	got2 := a.waitFor(MsgRecording)
	var rec Recording
	raw, _ := json.Marshal(got2["recording"])
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("录像反序列化失败：%v", err)
	}
	if len(rec.Audios) != 2 {
		t.Fatalf("录像里应当只剩后面那 2 条录音，实际 %d 条：%+v", len(rec.Audios), rec.Audios)
	}
	if len(rec.Steps) != 4 {
		t.Fatalf("录像应当是 4 步（起点 / 步进 / 录音 / 录音），实际 %d", len(rec.Steps))
	}
	for i, st := range rec.Steps {
		if st.Step != i+1 {
			t.Fatalf("清空重排之后步号必须连续：第 %d 个元素是 %d", i, st.Step)
		}
	}
	for _, au := range rec.Audios {
		if au.Step < 1 || au.Step > len(rec.Steps) {
			t.Fatalf("录音挂到了不存在的步上：%+v", au)
		}
		if st := rec.Steps[au.Step-1]; st.Audio == nil || st.Audio.ID != au.ID {
			t.Fatalf("第 %d 步上挂的录音和 audios 里那条对不上：%+v / %+v", au.Step, st.Audio, au)
		}
	}
	// 目录里也不该留下孤儿音频（audio-1/2 被删了，重录的两条是 audio-3/4）
	entries, err := os.ReadDir(filepath.Join(recDir, recordingFolder(id)))
	if err != nil {
		t.Fatalf("读录像目录失败：%v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "audio-") && e.Name() != rec.Audios[0].File && e.Name() != rec.Audios[1].File {
			t.Fatalf("录完的目录里还有没人引用的音频：%s", e.Name())
		}
	}
}

// TestRecordingStopsWhenOperatorLeaves 操作者关掉页面时，录像要收尾并保存，
// 否则这个实例会永远停在「已经在录制了」。
func TestRecordingStopsWhenOperatorLeaves(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用录像目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	a.send(hub, map[string]any{"type": "record_start", "instanceId": instID})
	a.waitAction(ActRecordingStarted)

	hub.Unregister(a.conn)

	inst := mustInstance(t, hub, instID)
	if inst.rec != nil {
		t.Fatal("操作者离开后不该还在录制")
	}
	// 录像写在 Handle/Unregister 之外的锁外路径上（Unregister 是同步的），
	// 这里直接看目录：有 recording.json 才算真的保存了。
	entries, err := os.ReadDir(recDir)
	if err != nil {
		t.Fatalf("读录像目录失败：%v", err)
	}
	found := false
	for _, e := range entries {
		if e.IsDir() {
			if _, err := os.Stat(filepath.Join(recDir, e.Name(), recordingFileName)); err == nil {
				found = true
			}
		}
	}
	if !found {
		t.Fatal("操作者离开后录像没有落盘")
	}
}

// ---------------------------------------------------------------------------
// TestRecordingRejectsOldFormat 钉住「旧录像不读」：v0.15.0 的 Steps 语义变了，
// 旧文件（format=1，按 K 线分步）如果被硬读出来，步进条会显示成一堆「一步蹦出一整根
// K 线变化」的怪物。宁可明确说「这条不兼容」，也不要让人以为回放坏了。
func TestRecordingRejectsOldFormat(t *testing.T) {
	recDir := t.TempDir()
	// 造一条旧格式录像（没有 format 字段 = 1）
	old := filepath.Join(recDir, "rec-20260101-000000")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	body := `{"id":"rec-20260101-000000","symbol":"CME_MINI:MNQ1!","name":"旧录像",
		"base":{"cursor":0,"bars":[]},"steps":[{"step":1,"time":"2026-01-01 09:00","events":[]}]}`
	if err := os.WriteFile(filepath.Join(old, recordingFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("写旧格式录像失败：%v", err)
	}

	var logs []string
	st, err := OpenRecordingStore(recDir, func(f string, a ...any) {
		logs = append(logs, fmt.Sprintf(f, a...))
	})
	if err != nil {
		t.Fatalf("打开录像目录失败：%v", err)
	}
	if n := len(st.List()); n != 0 {
		t.Fatalf("旧格式录像不该出现在列表里，实际 %d 条", n)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "旧格式") {
		t.Fatalf("跳过旧格式录像时应当说一句，实际日志：%s", joined)
	}
	if _, err := st.Load("rec-20260101-000000"); err == nil || !strings.Contains(err.Error(), "旧格式") {
		t.Fatalf("读旧格式录像应当明确报「旧格式」，实际 %v", err)
	}
	// 旧目录**不自动删**：别人的数据，说一下就够
	if _, err := os.Stat(filepath.Join(old, recordingFileName)); err != nil {
		t.Fatalf("不该擅自删掉旧录像目录：%v", err)
	}
}

// ---------------------------------------------------------------------------
// 测试辅助
// ---------------------------------------------------------------------------

func (h *Hub) mustInstance(t *testing.T, id string) *Instance {
	t.Helper()
	h.mu.Lock()
	defer h.mu.Unlock()
	inst, ok := h.instances[id]
	if !ok {
		t.Fatalf("找不到实例 %s", id)
	}
	return inst
}

func mustInstance(t *testing.T, hub *Hub, id string) *Instance {
	t.Helper()
	return hub.mustInstance(t, id)
}

// botTickProbeLocked 模拟机器人「干过几件事」：画一条标注 + 步进一次。
//
// 直接走 h.do* 而不是 runBot，是为了确定性 —— 机器人 tick 本身就是随机挑一个
// do* 调用的薄壳，这里验的是「do* -> 广播 -> 录制」这条链路。
func (h *Hub) botTickProbeLocked(inst *Instance) {
	idx := inst.Cursor
	if idx > 1 {
		idx -= 1
	}
	a := inst.Bars[maxInt(0, idx-1)]
	b := inst.Bars[idx]
	_, _ = h.doAddAnnotationLocked(inst, "bot", AnnTrend,
		Point{Time: a.Time, Price: a.Low}, Point{Time: b.Time, Price: b.High}, AnnotationStyle{}, true)
	_ = h.doStepLocked(inst, "bot")
}

// replayRecording 用录像的 Base + 每一步的事件重建一个实例状态。
//
// 它是前端 stores/instance-reducer.ts 在 Go 里的最小镜像：只实现「改盘面」的
// 那几个动作。存在的意义不是给生产代码用，而是**证明录像里的事件足够还原当时**：
// 少录一个字段、动作漏挂一个，这里就会和实例对不上。
func replayRecording(t *testing.T, rec *Recording) *Instance {
	t.Helper()
	return replaySteps(t, rec, len(rec.Steps))
}

// replaySteps 只演到第 upto 步（1 起）。
//
// 前端「跳到第 N 步」做的是同一件事：从 Base 重算，而不是在上一帧上做减法 ——
// 这样任意跳步都不会错位。这里顺带钉住每一步自称的 Cursor 和演出来的对得上，
// 否则步号和盘面对不上，用户点步进条会看到错位的画面。
func replaySteps(t *testing.T, rec *Recording, upto int) *Instance {
	t.Helper()
	inst := &Instance{
		Symbol:      rec.Symbol,
		Folder:      rec.Folder,
		Tick:        rec.Tick,
		TickValue:   rec.TickValue,
		Bars:        append([]Bar(nil), rec.Base.Bars...),
		Cursor:      rec.Base.Cursor,
		Annotations: append([]Annotation(nil), rec.Base.Annotations...),
		Orders:      append([]Order(nil), rec.Base.Orders...),
		Trades:      append([]Trade(nil), rec.Base.Trades...),
		Position:    rec.Base.Position,
		RealizedPnl: rec.Base.RealizedPnl,
	}
	if upto > len(rec.Steps) {
		upto = len(rec.Steps)
	}
	for _, step := range rec.Steps[:maxInt(0, upto)] {
		for _, ev := range step.Events {
			applyRecorded(t, inst, ev)
		}
	}
	return inst
}

func applyRecorded(t *testing.T, inst *Instance, ev RecordingEvent) {
	t.Helper()
	decode := func(dst any) bool {
		if len(ev.Payload) == 0 {
			return false
		}
		if err := json.Unmarshal(ev.Payload, dst); err != nil {
			t.Fatalf("第 %d 条事件（%s）的 payload 坏了：%v", ev.Seq, ev.Action, err)
		}
		return true
	}
	switch ev.Action {
	case ActBarAdvanced:
		var p BarAdvancedPayload
		if !decode(&p) {
			t.Fatalf("bar_advanced 没有 payload")
		}
		inst.Bars = append(inst.Bars, p.Bar)
		inst.Cursor = p.Cursor
		inst.Orders = p.Orders
		inst.Trades = p.Trades
		inst.Position = p.Position
		inst.RealizedPnl = p.RealizedPnl
	case ActAnnotationAdded:
		var p AnnotationPayload
		if decode(&p) {
			inst.Annotations = append(inst.Annotations, p.Annotation)
		}
	case ActAnnotationMoved:
		var p AnnotationPayload
		if decode(&p) {
			for i := range inst.Annotations {
				if inst.Annotations[i].ID == p.Annotation.ID {
					inst.Annotations[i] = p.Annotation
				}
			}
		}
	case ActAnnotationDelete:
		var p AnnotationDeletedPayload
		if decode(&p) {
			kept := inst.Annotations[:0]
			for _, a := range inst.Annotations {
				if a.ID != p.AnnotationID {
					kept = append(kept, a)
				}
			}
			inst.Annotations = kept
		}
	case ActAnnotationClear:
		inst.Annotations = nil
	case ActOrderPlaced, ActOrderCancelled:
		var p OrdersPayload
		if decode(&p) {
			inst.Orders = p.Orders
		}
	case ActOrderMoved:
		var p OrderMovedPayload
		if decode(&p) {
			inst.Orders = p.Orders
		}
	case ActOrderFilled:
		var p OrderFilledPayload
		if decode(&p) {
			inst.Orders = p.Orders
			inst.Trades = p.Trades
			inst.Position = p.Position
			inst.RealizedPnl = p.RealizedPnl
		}
	case ActRecordingAudio:
		// 录音步不改盘面：步里没有事件，这条分支只是把「允许出现」说清楚
	default:
		t.Fatalf("录像里出现了不该被录的动作：%s", ev.Action)
	}
}

// assertSameState 逐项比「重放出来的」和「真实实例」。
func assertSameState(t *testing.T, live, replayed *Instance) {
	t.Helper()
	if len(replayed.Bars) != live.Cursor+1 {
		t.Fatalf("重放出来的 K 线应当是 %d 根（已揭示部分），实际 %d", live.Cursor+1, len(replayed.Bars))
	}
	if replayed.Cursor != live.Cursor {
		t.Fatalf("游标不一致：重放 %d / 实例 %d", replayed.Cursor, live.Cursor)
	}
	if len(replayed.Annotations) != len(live.Annotations) {
		t.Fatalf("标注数量不一致：重放 %d / 实例 %d", len(replayed.Annotations), len(live.Annotations))
	}
	for i := range live.Annotations {
		if replayed.Annotations[i].ID != live.Annotations[i].ID ||
			replayed.Annotations[i].Kind != live.Annotations[i].Kind ||
			replayed.Annotations[i].Start != live.Annotations[i].Start {
			t.Fatalf("第 %d 个标注不一致：重放 %+v / 实例 %+v", i, replayed.Annotations[i], live.Annotations[i])
		}
	}
	if len(replayed.Orders) != len(live.Orders) {
		t.Fatalf("挂单数量不一致：重放 %d / 实例 %d", len(replayed.Orders), len(live.Orders))
	}
	for i := range live.Orders {
		if replayed.Orders[i].ID != live.Orders[i].ID || replayed.Orders[i].Status != live.Orders[i].Status {
			t.Fatalf("第 %d 张单不一致：重放 %+v / 实例 %+v", i, replayed.Orders[i], live.Orders[i])
		}
	}
	if len(replayed.Trades) != len(live.Trades) {
		t.Fatalf("成交数量不一致：重放 %d / 实例 %d", len(replayed.Trades), len(live.Trades))
	}
	if replayed.Position != live.Position {
		t.Fatalf("持仓不一致：重放 %+v / 实例 %+v", replayed.Position, live.Position)
	}
	if fmt.Sprintf("%.6f", replayed.RealizedPnl) != fmt.Sprintf("%.6f", live.RealizedPnl) {
		t.Fatalf("已实现盈亏不一致：重放 %v / 实例 %v", replayed.RealizedPnl, live.RealizedPnl)
	}
}
