package replay

// 回放延伸（v0.27.0）的回归测试。
//
// 这一组用例盯的是四件最容易出错、而且「看起来能用」的事：
//
//  1. **延伸真的落盘**（TestRecordingExtendPersists）：加载一根 = 记录里多一步，
//     重开记录目录之后还在。只在内存里成立的话，换台机器那几根就凭空消失了。
//  2. **延伸不动交易状态**（同一条用例里的断言）：延伸步只带 Bar，不带持仓 ——
//     平仓之后多看了几根 K 线，回放里的仓位不能被改写成「还开着」。
//  3. **语音挂在延伸出来的那根上**（TestRecordingExtendVoice）：和复盘录音同一套
//     额度与格式校验，落成 audio-N.wav + 一个语音步；重新读回来的记录里
//     「时间-步号-文件」三者互相对得上。
//  4. **边界说得清**（TestRecordingExtendEdges）：数据到头、交易还在进行中
//     都明确报错，而不是静默什么都没发生。

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// closeTradeAndGetID 开一笔再平掉，返回（记录 id、最后一根 K 线的时间）。
func closeTradeAndGetID(t *testing.T, hub *Hub, fc *fakeClient, instID, anchor string) (string, string) {
	t.Helper()
	inst := mustInstance(t, hub, instID)
	marketOrder(t, hub, fc, instID, "buy", 1)
	lastBar := inst.Bars[inst.Cursor].Time
	marketOrder(t, hub, fc, instID, "sell", 1)
	msg := fc.waitFor(MsgTradeRecorded)
	sum, _ := msg["recording"].(map[string]any)
	id, _ := sum["id"].(string)
	if id == "" {
		t.Fatalf("记录没有 id：%v", sum)
	}
	return id, lastBar
}

// extendOnce 发一次「往后加载一根」，返回消息体。
func extendOnce(t *testing.T, hub *Hub, fc *fakeClient, recID string, count int) map[string]any {
	t.Helper()
	req := map[string]any{"type": ReqRecordingExtend, "recordingId": recID}
	if count > 0 {
		req["count"] = count
	}
	fc.send(hub, req)
	return fc.waitFor(MsgRecordingExtended)
}

// stepsOfMsg 取消息里的 steps 数组。
func stepsOfMsg(t *testing.T, msg map[string]any) []any {
	t.Helper()
	steps, ok := msg["steps"].([]any)
	if !ok {
		t.Fatalf("延伸回复里没有 steps：%v", msg)
	}
	return steps
}

// TestRecordingExtendPersists 是主用例：加载 → 落盘 → 重新读回来。
func TestRecordingExtendPersists(t *testing.T) {
	hub, _ := newTestHub(t, 3)
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	// 锚点放在第二天：前一天的 25 根会作为历史一起载入，EMA20 才有值
	// （第一天当锚点时已揭示的根数不够 20，指标线本来就是断的）。
	instID := createInstance(t, hub, a, "2026-01-06 10:00")
	recID, lastBar := closeTradeAndGetID(t, hub, a, instID, "2026-01-06 10:00")
	if lastBar != "2026-01-06 10:00" {
		t.Fatalf("这一笔应当收在 10:00，实际 %s", lastBar)
	}

	// ---- 加载一根：多一步、Time 是下一根、HasMore 为真 ----
	msg := extendOnce(t, hub, a, recID, 1)
	steps := stepsOfMsg(t, msg)
	if len(steps) != 1 {
		t.Fatalf("加载一根应当只多一步，实际 %d 步", len(steps))
	}
	st, _ := steps[0].(map[string]any)
	if st["kind"] != ActBarExtended {
		t.Fatalf("延伸步的类型应当是 %s，实际 %v", ActBarExtended, st["kind"])
	}
	if st["time"] != "2026-01-06 10:05" {
		t.Fatalf("延伸出来的应当是 10:05 那一根，实际 %v", st["time"])
	}
	if msg["hasMore"] != true {
		t.Fatalf("第一天后面还有行情，hasMore 应当为真：%v", msg["hasMore"])
	}
	// 延伸步的 payload 里**不能**有持仓/订单：那段时间根本没有成交
	evs, _ := st["events"].([]any)
	if len(evs) != 1 {
		t.Fatalf("延伸步应当只有一个事件，实际 %v", evs)
	}
	ev, _ := evs[0].(map[string]any)
	if ev["action"] != ActBarExtended {
		t.Fatalf("事件动作应当是 %s，实际 %v", ActBarExtended, ev["action"])
	}
	payload, _ := ev["payload"].(map[string]any)
	if payload == nil {
		t.Fatalf("延伸步的事件没有 payload：%v", ev)
	}
	for _, bad := range []string{"position", "orders", "trades", "closedLots"} {
		if _, has := payload[bad]; has {
			t.Fatalf("延伸步不该带 %s（会改写回放里的交易状态）：%v", bad, payload)
		}
	}
	bar, _ := payload["bar"].(map[string]any)
	if bar["time"] != "2026-01-06 10:05" {
		t.Fatalf("payload 里的 K 线不对：%v", bar)
	}
	if bar["ema20"] == nil {
		t.Fatalf("延伸出来的 K 线也该有 EMA20（要和实时盘面同一口径）：%v", bar)
	}

	// ---- 再加载两根：一次请求多根也要按顺序接着走 ----
	msg = extendOnce(t, hub, a, recID, 2)
	steps = stepsOfMsg(t, msg)
	if len(steps) != 2 {
		t.Fatalf("一次加载两根应当多两步，实际 %d", len(steps))
	}
	if s0, _ := steps[0].(map[string]any); s0["time"] != "2026-01-06 10:10" {
		t.Fatalf("第二根应当是 10:10，实际 %v", s0["time"])
	}
	if s1, _ := steps[1].(map[string]any); s1["time"] != "2026-01-06 10:15" {
		t.Fatalf("第三根应当是 10:15，实际 %v", s1["time"])
	}

	// ---- 落盘：重新打开记录目录，延伸的步还在 ----
	reopened, err := OpenRecordingStore(recDir, t.Logf)
	if err != nil {
		t.Fatalf("重开记录目录失败：%v", err)
	}
	rec, err := reopened.Load(recID)
	if err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if got := len(rec.Steps); got != 6 {
		t.Fatalf("记录应当有 6 步（3 步交易 + 3 步延伸），实际 %d", got)
	}
	if got := rec.StepCount; got != 6 {
		t.Fatalf("摘要里的步数应当跟着变（6），实际 %d", got)
	}
	if rec.Steps[5].Kind != ActBarExtended || rec.Steps[5].Time != "2026-01-06 10:15" {
		t.Fatalf("最后一步应当是 10:15 的延伸步，实际 %+v", rec.Steps[5])
	}
	// 摘要里的「根数」把延伸的也算进来：顶栏要显示「第 9/6 根」就不对了
	if got := rec.Bars; got != 4 {
		t.Fatalf("根数应当是 4（入场那根 + 延伸 3 根），实际 %d", got)
	}
	for i, st := range rec.Steps {
		if st.Step != i+1 {
			t.Fatalf("步号必须连续：第 %d 个元素写着第 %d 步", i, st.Step)
		}
	}
}

// TestRecordingExtendVoice 在延伸出来的那根上录音：落盘、限额、清除后重排。
func TestRecordingExtendVoice(t *testing.T) {
	hub, _ := newTestHub(t, 3)
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	recID, _ := closeTradeAndGetID(t, hub, a, instID, "2026-01-05 10:00")

	// 还没延伸过：没地方挂语音，必须明确拒绝
	a.send(hub, map[string]any{
		"type": ReqRecordingVoiceAdd, "recordingId": recID,
		"audioData": base64.StdEncoding.EncodeToString(testWAV(400)), "durationMs": 1000,
	})
	errMsg := a.waitFor(MsgError)
	if s, _ := errMsg["message"].(string); !strings.Contains(s, "加载下一根") {
		t.Fatalf("应提示先加载一根，实际 %q", s)
	}

	extendOnce(t, hub, a, recID, 1)

	// ---- 录一段：新的语音步 + audio-1.wav ----
	sendVoice := func(n int) (map[string]any, bool) {
		a.send(hub, map[string]any{
			"type": ReqRecordingVoiceAdd, "recordingId": recID,
			"audioData": base64.StdEncoding.EncodeToString(testWAV(400 + n)), "durationMs": 1000,
		})
		// 成功回 recording_voice，失败回 error
		deadline := a.next()
		for deadline["type"] != MsgRecordingVoice && deadline["type"] != MsgError {
			deadline = a.next()
		}
		return deadline, deadline["type"] == MsgRecordingVoice
	}
	msg, ok := sendVoice(0)
	if !ok {
		t.Fatalf("录音失败：%v", msg)
	}
	clip, _ := msg["audio"].(map[string]any)
	if clip == nil {
		t.Fatalf("回复里没有 audio：%v", msg)
	}
	if clip["time"] != "2026-01-05 10:05" {
		t.Fatalf("语音应当挂在延伸出来的 10:05 那根上，实际 %v", clip["time"])
	}
	if clip["step"] != float64(5) {
		t.Fatalf("语音自己占一步，应当是第 5 步，实际 %v", clip["step"])
	}
	if clip["file"] != "audio-1.wav" {
		t.Fatalf("文件应当从 audio-1.wav 开始，实际 %v", clip["file"])
	}
	if _, err := os.Stat(filepath.Join(recDir, recordingFolder(recID), "audio-1.wav")); err != nil {
		t.Fatalf("语音文件没有落盘：%v", err)
	}
	// 语音步里那份副本必须和 Audios 里的正本同一个步号
	step, _ := msg["step"].(map[string]any)
	if step["kind"] != ActRecordingAudio {
		t.Fatalf("语音步的类型应当是 %s，实际 %v", ActRecordingAudio, step["kind"])
	}
	if step["audio"] == nil {
		t.Fatalf("语音步里应当带着那一段：%v", step)
	}
	// 回给前端的步号必须是**真的那个**：0 会让播放器跳到第 1 步（曾经就是这样）
	if step["step"] != float64(5) {
		t.Fatalf("回复里的语音步应当是第 5 步，实际 %v", step["step"])
	}

	// ---- 同一根最多 9 条：第 10 条要明确报错 ----
	for i := 1; i < maxClipsPerBar; i++ {
		if _, ok := sendVoice(i); !ok {
			t.Fatalf("第 %d 条应当还能录（每根 %d 条）", i+1, maxClipsPerBar)
		}
	}
	if m, ok := sendVoice(99); ok {
		t.Fatalf("第 %d 条应当被拒绝，实际成功了：%v", maxClipsPerBar+1, m)
	}

	// ---- 再加载一根：录音位置跟着走到新的末尾那根 ----
	extendOnce(t, hub, a, recID, 1)
	msg, ok = sendVoice(120)
	if !ok {
		t.Fatalf("在新的末尾那根上录音失败：%v", msg)
	}
	clip, _ = msg["audio"].(map[string]any)
	if clip["time"] != "2026-01-05 10:10" {
		t.Fatalf("加载之后应当录在 10:10 那根上，实际 %v", clip["time"])
	}

	// ---- 清空当前这根：语音步一起摘掉，后面的步号往前挪 ----
	a.send(hub, map[string]any{"type": ReqRecordingVoiceClear, "recordingId": recID})
	msg = a.waitFor(MsgRecordingVoice)
	removed, _ := msg["removed"].([]any)
	if len(removed) != 1 {
		t.Fatalf("这根上只有 1 条，应当清掉 1 条，实际 %d", len(removed))
	}

	// ---- 重新读回来：步号连续、语音清单与步一一对应 ----
	reopened, err := OpenRecordingStore(recDir, t.Logf)
	if err != nil {
		t.Fatalf("重开记录目录失败：%v", err)
	}
	rec, err := reopened.Load(recID)
	if err != nil {
		t.Fatalf("读不回来：%v", err)
	}
	if rec.AudioCount != maxClipsPerBar {
		t.Fatalf("清掉 1 条之后应当剩 %d 条语音，实际 %d", maxClipsPerBar, rec.AudioCount)
	}
	wantSteps := 3 + 2 + maxClipsPerBar // 交易 3 + 两根延伸 + 9 条语音
	if len(rec.Steps) != wantSteps {
		t.Fatalf("应当有 %d 步，实际 %d", wantSteps, len(rec.Steps))
	}
	for i, st := range rec.Steps {
		if st.Step != i+1 {
			t.Fatalf("清空之后步号必须重排：第 %d 个元素写着第 %d 步", i, st.Step)
		}
	}
	stepOf := map[string]int{}
	for _, st := range rec.Steps {
		if st.Audio != nil {
			stepOf[st.Audio.ID] = st.Step
		}
	}
	for _, c := range rec.Audios {
		if n, ok := stepOf[c.ID]; !ok || n != c.Step {
			t.Fatalf("语音 %s 的步号（%d）和它那一步（%d/%v）对不上", c.ID, c.Step, n, ok)
		}
	}
	// 被清掉的那条文件应当真的没了
	if _, err := os.Stat(filepath.Join(recDir, recordingFolder(recID), "audio-10.wav")); !os.IsNotExist(err) {
		t.Fatalf("清空之后 audio-10.wav 应当被删掉，实际 err=%v", err)
	}
}

// TestRecordingExtendEdges 边界：数据到头、交易还在进行中。
func TestRecordingExtendEdges(t *testing.T) {
	hub, _ := newTestHub(t, 1) // 只有一天：锚点之后没几根
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:50")
	recID, _ := closeTradeAndGetID(t, hub, a, instID, "2026-01-05 10:50")

	// 这一天到 11:00 结束：从 10:50 往后只剩 2 根
	msg := extendOnce(t, hub, a, recID, 10)
	if n := len(stepsOfMsg(t, msg)); n != 2 {
		t.Fatalf("锚点之后只剩 2 根，实际加载了 %d 根", n)
	}
	if msg["hasMore"] != false {
		t.Fatalf("这一天之后没有数据了，hasMore 应当是 false：%v", msg["hasMore"])
	}

	// 再点一次：给的是「数据已经到最后一根」的原话，而不是一句泛泛的失败
	a.send(hub, map[string]any{"type": ReqRecordingExtend, "recordingId": recID})
	errMsg := a.waitFor(MsgError)
	if s, _ := errMsg["message"].(string); !strings.Contains(s, "最后一根") {
		t.Fatalf("应当明确说已经到最后一根，实际 %q", s)
	}

	// 交易还在进行中（草稿）：拒绝 —— 收尾时 Save 会整份覆盖，延伸会白做
	liveID := "rec-live-test"
	if _, err := hub.records.MarkLive(liveID); err != nil {
		t.Fatalf("登记 live 失败：%v", err)
	}
	rec, err := hub.records.Load(recID)
	if err != nil {
		t.Fatalf("读记录失败：%v", err)
	}
	rec.ID = liveID
	if _, err := hub.records.UpdateRecording(rec); err != nil {
		t.Fatalf("写一份进行中的记录失败：%v", err)
	}
	a.send(hub, map[string]any{"type": ReqRecordingExtend, "recordingId": liveID})
	errMsg = a.waitFor(MsgError)
	if s, _ := errMsg["message"].(string); !strings.Contains(s, "还在进行中") {
		t.Fatalf("进行中的记录应当被拒绝，实际 %q", s)
	}
}

// TestRecordingExtendKeepsAudioOnSave 是 Save 的回归点：
// 保存一条记录时「该留哪些语音」以 rec.Audios 为准，而不是以这次传进来的字节为准。
//
// 反例的表现是：补抄开仓前语音（Save 只带新增那几条）之后，之前那些 audio-*.wav
// 全被当成残留删掉了 —— 界面上就是「打开一下记录，语音没了」。
func TestRecordingExtendKeepsAudioOnSave(t *testing.T) {
	st, err := OpenRecordingStore(t.TempDir(), t.Logf)
	if err != nil {
		t.Fatalf("建记录目录失败：%v", err)
	}
	rec := &Recording{
		RecordingSummary: RecordingSummary{ID: "rec-save-test", Symbol: testSymbol},
		Format:           RecordingFormat,
		Steps:            []RecordingStep{{Step: 1, Kind: StepBase, Events: []RecordingEvent{}}},
		Audios:           []AudioClip{{ID: "au1", Step: 2, Time: "2026-01-05 10:00", File: "audio-1.wav", Size: int64(len(testWAV(10)))}},
	}
	rec.Steps = append(rec.Steps, RecordingStep{Step: 2, Kind: ActRecordingAudio, Events: []RecordingEvent{}})
	if _, err := st.Save(rec, []pendingAudio{{clip: rec.Audios[0], data: testWAV(10)}}); err != nil {
		t.Fatalf("首次保存失败：%v", err)
	}
	// 再存一次，这次**不带**任何音频字节（比如补抄场景）：audio-1.wav 必须还在
	if _, err := st.Save(rec, nil); err != nil {
		t.Fatalf("再保存失败：%v", err)
	}
	path := filepath.Join(st.Dir(), recordingFolder(rec.ID), "audio-1.wav")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("被记录引用的语音不该被 Save 清掉：%v", err)
	}
	// 反过来：真的没人引用的残留仍然要清掉
	stray := filepath.Join(st.Dir(), recordingFolder(rec.ID), "audio-9.wav")
	if err := os.WriteFile(stray, testWAV(10), 0o644); err != nil {
		t.Fatalf("造残留失败：%v", err)
	}
	if _, err := st.Save(rec, nil); err != nil {
		t.Fatalf("第三次保存失败：%v", err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("没人引用的 audio-9.wav 应当被清掉，实际 err=%v", err)
	}
	// 读回来的记录里那一份仍然完整可解析
	raw, _ := json.Marshal(rec)
	var back Recording
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("记录解析失败：%v", err)
	}
}
