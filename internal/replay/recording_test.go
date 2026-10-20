package replay

// 录制与回放的回归测试。
//
// 这里钉住三件事：
//
//  1. **录的是操作，不是画面**：第 1 步 = 开始录制时那一屏，之后每步进一次开一段，
//     段内按顺序排着那一步发生的操作（标注、挂单、成交）。
//  2. **机器人走的是同一条通道**：机器人不经过任何后门，它的操作会以 by="bot"
//     出现在同一份录像里 —— 这条用例就是需求里「验证前端用户操作与后端机器人操作
//     完全等价」的机器化版本：把录像重放一遍，最终状态必须和实例一模一样。
//  3. **录音按 K 线时间挂载、最多 9 条**，并且音频文件能被 HTTP 路由正确取回。

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
func snippetForLog(raw []byte) string {
	const max = 400
	if len(raw) <= max {
		return string(raw)
	}
	return string(raw[:max]) + "…（已截断）"
}

// TestRecordingCapturesOpsAndReplays 是主用例：录一段「真人 + 机器人混着来」的操作，
// 再把它重放出来，逐项比对最终状态。
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
	if state["startTime"] != inst.Bars[inst.Cursor].Time {
		t.Fatalf("第 1 步应当对应当前 K 线 %s，实际 %v", inst.Bars[inst.Cursor].Time, state["startTime"])
	}

	// 2. 真人操作：步进一次、画一条射线、挂一张限价单
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)

	nowBar := inst.Bars[inst.Cursor]
	prevBar := inst.Bars[inst.Cursor-1]
	a.send(hub, map[string]any{
		"type": "annotation_add", "instanceId": instID, "kind": AnnRay,
		"start": map[string]any{"time": prevBar.Time, "price": prevBar.Close},
		"end":   map[string]any{"time": nowBar.Time, "price": nowBar.High},
	})
	a.waitAction(ActAnnotationAdded)
	a.send(hub, map[string]any{
		"type": "order_place", "instanceId": instID,
		"side": "buy", "kind": KindLimit, "price": nowBar.Low - 1, "quantity": 2,
	})
	a.waitAction(ActOrderPlaced)

	// 3. 机器人操作：直接调 Hub 的内部动作（和机器人的 tick 完全相同的那几条路径）
	hub.mu.Lock()
	hub.botTickProbeLocked(inst)
	hub.mu.Unlock()

	// 4. 录音挂到**当前**这根 K 线上（机器人刚刚步进过，所以是新的那根）
	audioBar := inst.Bars[inst.Cursor]
	audioEv := sendAudio(t, hub, a, instID, testWAV(400))
	audioPayload := audioEv["payload"].(map[string]any)
	clip := audioPayload["audio"].(map[string]any)
	if clip["time"] != audioBar.Time {
		t.Fatalf("录音应当挂在当前 K 线 %s 上，实际 %v", audioBar.Time, clip["time"])
	}
	if clip["step"].(float64) != 3 {
		t.Fatalf("录音应当挂第 3 步（机器人在第 3 步步进），实际 %v", clip["step"])
	}

	// 5. 再步进一次 → 第 4 步
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
	if got := sum["stepCount"].(float64); got != 4 {
		t.Fatalf("录像应当是 4 步（开始 + 真人 1 次 + 机器人 1 次 + 真人 1 次），实际 %v", got)
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
	if len(rec.Steps) != 4 {
		t.Fatalf("读回来的录像应当有 4 步，实际 %d", len(rec.Steps))
	}
	// 数组字段必须序列化成 `[]` 而不是 `null`：第 1 步天然没有事件，前端步进条
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

	// 9. 事件内容：第 2 步里既有真人的标注/挂单，也有机器人画的线
	var humanAnn, botSeen int
	for _, ev := range rec.Steps[1].Events {
		switch {
		case ev.Action == ActAnnotationAdded && ev.By == "human":
			humanAnn++
		case ev.By == "bot":
			botSeen++
		}
	}
	if humanAnn != 1 {
		t.Fatalf("第 2 步里应当有 1 条真人标注，实际 %d", humanAnn)
	}
	if botSeen == 0 {
		t.Fatal("机器人操作没有被录下来 —— 它和真人走的应当是同一出口")
	}

	// 10. **重放等价**：Base + 事件演一遍，最终状态必须和实例完全一致
	replayed := replayRecording(t, &rec)
	assertSameState(t, inst, replayed)

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

// TestRecordingAudioLimit 钉住「最多 9 条」和「必须是 WAV」。
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

	wav := testWAV(64)
	for i := 0; i < maxRecordAudios; i++ {
		ev := sendAudio(t, hub, a, instID, wav)
		st := ev["payload"].(map[string]any)["state"].(map[string]any)
		if int(st["audios"].(float64)) != i+1 {
			t.Fatalf("第 %d 条录音后的计数不对：%v", i+1, st["audios"])
		}
	}
	if len(hub.mustInstance(t, instID).rec.audios) != maxRecordAudios {
		t.Fatalf("内存里应当有 %d 条录音", maxRecordAudios)
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
	for _, step := range rec.Steps {
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
