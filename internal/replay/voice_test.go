package replay

// 语音讲解（v0.16.0）的回归测试。
//
// 这些用例钉住的是**解耦之后的三条底线**：
//
//  1. **不录制也能录**：没有 record_start 一样能往当前那根 K 线上挂语音，
//     快照里能看见、HTTP 能取回、重启之后还在。
//  2. **额度还是每根 9 条**（不按步数、不按是否在录制），换一根就重新有 9 条。
//  3. **录制是下游**：录制期间录的那几条会额外抄一份进录像（照旧是一个
//     recording_audio 步），录制结束后再录的就只留在盘面上。
//
// 还有一条兼容用例：老的 record_audio / record_audio_delete 仍然可用、语义不变。

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// sendVoice 发一条 voice_add 并等 ActVoiceAdded。
func sendVoice(t *testing.T, hub *Hub, fc *fakeClient, instID string, wav []byte) map[string]any {
	t.Helper()
	fc.send(hub, map[string]any{
		"type":       "voice_add",
		"instanceId": instID,
		"audioData":  base64.StdEncoding.EncodeToString(wav),
		"durationMs": 1000,
	})
	return fc.waitAction(ActVoiceAdded)
}

// voicePayload 取广播里 payload 的两个部分，省得每处都写断言。
func voicePayload(t *testing.T, ev map[string]any) (clip, state map[string]any) {
	t.Helper()
	p, ok := ev["payload"].(map[string]any)
	if !ok {
		t.Fatalf("广播没有 payload：%v", ev)
	}
	clip, _ = p["audio"].(map[string]any)
	state, _ = p["voice"].(map[string]any)
	if clip == nil || state == nil {
		t.Fatalf("payload 里应当有 audio 和 voice：%v", p)
	}
	return clip, state
}

// TestVoiceWorksWithoutRecording 是这次改动的核心：
// **一次 record_start 都没发过**，照样能录、能听、能清空。
func TestVoiceWorksWithoutRecording(t *testing.T) {
	root := writeTestData(t, 3)
	stateDir := t.TempDir()
	hub := newPersistentHub(t, root, stateDir)
	// 语音不再依赖录像目录：即使完全没有 recordings，录音也该成立。
	if err := hub.EnableRecordings(t.TempDir()); err != nil {
		t.Fatalf("启用录像目录失败：%v", err)
	}

	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)
	barTime := inst.Bars[inst.Cursor].Time

	// 快照里就有 voice（空对象，不是 null）—— 建实例那一份已经由 createInstance 消费，
	// 这里只验「第一次录之前它是空的」，避免界面要靠猜。
	if v := inst.voiceState(); v.Total != 0 || len(v.Clips) != 0 || v.Max != maxClipsPerBar {
		t.Fatalf("刚开始不该有语音，状态应当是空的（上限 %d）：%+v", maxClipsPerBar, v)
	}

	ev := sendVoice(t, hub, a, instID, testWAV(400))
	clip, state := voicePayload(t, ev)
	if clip["time"] != barTime {
		t.Fatalf("语音应当挂在当前 K 线 %s 上，实际 %v", barTime, clip["time"])
	}
	if clip["by"] != "human" {
		t.Fatalf("语音应当记下是谁录的，实际 %v", clip["by"])
	}
	if clip["step"].(float64) != 0 {
		t.Fatalf("没在录制时录的语音不该有录像步号，实际 %v", clip["step"])
	}
	if n := len(state["clips"].([]any)); n != 1 {
		t.Fatalf("本根应当有 1 条语音，实际 %d", n)
	}
	if state["max"].(float64) != maxClipsPerBar || state["total"].(float64) != 1 {
		t.Fatalf("状态不对：%v", state)
	}
	if ev["recStep"] != nil && ev["recStep"].(float64) != 0 {
		t.Fatalf("语音不是录像里的一步（recStep 应当缺省或 0），实际 %v", ev["recStep"])
	}
	if inst.rec != nil {
		t.Fatal("没开录制，实例上不该有录制缓冲")
	}

	// 换一根：清单整体换成新那根的（上一根的还在，只是不显示）
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	adv := a.waitAction(ActBarAdvanced)
	advVoice := adv["payload"].(map[string]any)["voice"].(map[string]any)
	if n := len(advVoice["clips"].([]any)); n != 0 {
		t.Fatalf("新的一根应当是空清单，实际 %d 条", n)
	}
	if advVoice["barTime"] != inst.Bars[inst.Cursor].Time {
		t.Fatalf("步进广播里的语音状态应当指向新的那根：%v", advVoice["barTime"])
	}
	if advVoice["total"].(float64) != 1 {
		t.Fatalf("整个实例的总数应当继续是 1，实际 %v", advVoice["total"])
	}

	// 新的快照里也带着这根的状态（观众进来看到的是同一份）
	b := newFakeClient(t, hub)
	b.waitFor(MsgWelcome)
	b.send(hub, map[string]any{"type": "join_instance", "instanceId": instID})
	st := b.waitFor(MsgInstanceState)["instance"].(map[string]any)
	if _, ok := st["voice"].(map[string]any); !ok {
		t.Fatalf("快照里必须带 voice 对象（不能是 null）：%v", st["voice"])
	}
	// 全量清单也要在：复盘不能倒回，别的 K 线上的语音只能靠它找到
	if n := len(st["voiceAll"].([]any)); n != 1 {
		t.Fatalf("快照里的 voiceAll 应当有 1 条（第 1 根上那条），实际 %d", n)
	}

	// 音频走 HTTP 取回
	srv := httptest.NewServer(NewHandler(hub, t.TempDir()))
	defer srv.Close()
	url := fmt.Sprintf("%s/api/instances/%s/voice/%s", srv.URL, instID, clip["file"])
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("取语音失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("语音应当 200，实际 %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "audio/wav" {
		t.Fatalf("语音的 Content-Type 应当是 audio/wav，实际 %q", ct)
	}
	// 路径穿越要被挡住
	bad, err := http.Get(fmt.Sprintf("%s/api/instances/%s/voice/..%%2Fmeta.json", srv.URL, instID))
	if err == nil {
		defer bad.Body.Close()
		if bad.StatusCode == http.StatusOK {
			t.Fatal("不该能通过语音路由读到 meta.json")
		}
	}
}

// TestVoiceLimitIsPerBar 钉住「每根 K 线 9 条」—— 这是解耦前后**唯一不许变**的那条约束。
func TestVoiceLimitIsPerBar(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")

	wav := testWAV(64)
	for i := 0; i < maxClipsPerBar; i++ {
		_, state := voicePayload(t, sendVoice(t, hub, a, instID, wav))
		if n := len(state["clips"].([]any)); n != i+1 {
			t.Fatalf("第 %d 条之后本根应当有 %d 条，实际 %d", i+1, i+1, n)
		}
		if state["total"].(float64) != float64(i+1) {
			t.Fatalf("总数不对：%v", state["total"])
		}
	}
	if n := len(mustInstance(t, hub, instID).voiceNotes().clips); n != maxClipsPerBar {
		t.Fatalf("实例上应当有 %d 条语音，实际 %d", maxClipsPerBar, n)
	}

	// 第 10 条：明确报错，而不是静默丢掉
	a.drain()
	a.send(hub, map[string]any{
		"type": "voice_add", "instanceId": instID,
		"audioData": base64.StdEncoding.EncodeToString(wav),
	})
	e := a.waitFor(MsgError)
	if msg, _ := e["message"].(string); !strings.Contains(msg, "最多") {
		t.Fatalf("超限时应当说清上限，实际 %v", e["message"])
	}

	// 换一根 → 额度重新开始；总数继续累加
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	_, state := voicePayload(t, sendVoice(t, hub, a, instID, wav))
	if n := len(state["clips"].([]any)); n != 1 {
		t.Fatalf("新的一根应当从 1 开始计数，实际 %d", n)
	}
	if state["total"].(float64) != maxClipsPerBar+1 {
		t.Fatalf("总数应当累加到 %d，实际 %v", maxClipsPerBar+1, state["total"])
	}
}

// TestVoiceClearBar 清空只影响当前这根，磁盘上的 wav 一起删掉。
func TestVoiceClearBar(t *testing.T) {
	root := writeTestData(t, 2)
	stateDir := t.TempDir()
	hub := newPersistentHub(t, root, stateDir)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)
	dir := inst.voiceNotes().dir
	if dir == "" {
		t.Fatal("配了状态目录，语音就该有落盘目录")
	}

	wav := testWAV(64)
	sendVoice(t, hub, a, instID, wav)
	ev := sendVoice(t, hub, a, instID, wav)
	_, state := voicePayload(t, ev)
	clips := state["clips"].([]any)
	if len(clips) != 2 {
		t.Fatalf("应当有 2 条，实际 %d", len(clips))
	}
	for _, c := range clips {
		name := c.(map[string]any)["file"].(string)
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("语音 %s 应当已经在磁盘上（界面要能立刻试听）：%v", name, err)
		}
	}

	// 换一根录一条，用来验「清空不影响别的 K 线」
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	sendVoice(t, hub, a, instID, wav)

	// 回到上一根清空（这里直接把 barTime 指过去，等价于用户在那根上点清空）
	a.drain()
	firstBar := inst.Bars[inst.Cursor-1].Time
	hub.mu.Lock()
	removed, recRemoved := hub.clearVoiceOfBarLocked(inst, firstBar)
	hub.mu.Unlock()
	if len(removed) != 2 || len(recRemoved) != 0 {
		t.Fatalf("应当清掉 2 条盘面语音、录像里 0 条，实际 %d / %d", len(removed), len(recRemoved))
	}
	for _, c := range removed {
		if _, err := os.Stat(filepath.Join(dir, c.File)); !os.IsNotExist(err) {
			t.Fatalf("清空后 %s 应该被删掉（err=%v）", c.File, err)
		}
	}
	if n := len(inst.voiceNotes().clips); n != 1 {
		t.Fatalf("别的 K 线上的那条不该被删，实际剩 %d 条", n)
	}

	// 通过消息清空：当前这根（已经换到第 2 根）清掉之后，再清就报错
	a.send(hub, map[string]any{"type": "voice_clear", "instanceId": instID})
	cleared := a.waitAction(ActVoiceCleared)
	p := cleared["payload"].(map[string]any)
	if n := len(p["removed"].([]any)); n != 1 {
		t.Fatalf("应当清掉当前这根上的 1 条，实际 %d", n)
	}
	if n := len(p["voiceAll"].([]any)); n != 0 {
		t.Fatalf("清空之后整个实例应当一条都不剩，实际 %d", n)
	}
	a.drain()
	a.send(hub, map[string]any{"type": "voice_clear", "instanceId": instID})
	if e := a.waitFor(MsgError); !strings.Contains(fmt.Sprint(e["message"]), "还没有") {
		t.Fatalf("空清单再清应当明确报错，实际 %v", e["message"])
	}
}

// TestVoiceDuringRecordingIsMirroredIntoRecording 钉住「录制是下游」：
// 录制期间录的语音会**额外**抄一份进录像（连同它那一步），录制结束后录的只留在盘面。
func TestVoiceDuringRecordingIsMirroredIntoRecording(t *testing.T) {
	recDir := t.TempDir()
	hub, _ := newTestHub(t, 2)
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用录像目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	// 录制**之前**先录一条：它属于盘面，不该进录像
	sendVoice(t, hub, a, instID, testWAV(64))

	a.send(hub, map[string]any{"type": "record_start", "instanceId": instID})
	a.waitAction(ActRecordingStarted)

	// 录制中录两条：每条都占录像里的一步
	ev := sendVoice(t, hub, a, instID, testWAV(128))
	clip, _ := voicePayload(t, ev)
	if clip["step"].(float64) != 2 {
		t.Fatalf("录制中的第一条语音应当是录像第 2 步（起点之后），实际 %v", clip["step"])
	}
	recState := ev["payload"].(map[string]any)["recording"].(map[string]any)
	if got := recState["step"].(float64); got != 2 {
		t.Fatalf("广播里应当带上「这一步是第 2 步」，实际 %v", got)
	}
	ev = sendVoice(t, hub, a, instID, testWAV(128))
	clip2, state := voicePayload(t, ev)
	if clip2["step"].(float64) != 3 {
		t.Fatalf("第二条应当是第 3 步，实际 %v", clip2["step"])
	}
	// 盘面上是 3 条（录制前 1 条 + 录制中 2 条），录像里只有 2 条
	if n := len(state["clips"].([]any)); n != 3 {
		t.Fatalf("本根应当有 3 条语音，实际 %d", n)
	}

	// 结束录制
	a.send(hub, map[string]any{"type": "record_stop", "instanceId": instID})
	stop := a.waitAction(ActRecordingStopped)
	sum := stop["payload"].(map[string]any)["recording"].(map[string]any)
	recID := sum["id"].(string)
	if got := sum["audioCount"].(float64); got != 2 {
		t.Fatalf("录像里应当只有录制期间那 2 条，实际 %v", got)
	}
	if got := sum["stepCount"].(float64); got != 3 {
		t.Fatalf("录像应当是 3 步（起点 + 两条语音），实际 %v", got)
	}

	// 录像目录里是它自己的一份副本，步里挂着那段音频
	var rec Recording
	raw, _ := json.Marshal(stop)
	_ = raw
	a.send(hub, map[string]any{"type": "get_recording", "recordingId": recID})
	got := a.waitFor(MsgRecording)
	raw, _ = json.Marshal(got["recording"])
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("录像反序列化失败：%v", err)
	}
	if len(rec.Audios) != 2 {
		t.Fatalf("录像里应当有 2 条录音，实际 %d", len(rec.Audios))
	}
	for i, st := range rec.Steps[1:] {
		if st.Kind != ActRecordingAudio || st.Audio == nil || len(st.Events) != 0 {
			t.Fatalf("第 %d 步应当是一个录音步（无事件、挂音频）：%+v", i+2, st)
		}
	}
	for _, au := range rec.Audios {
		if !strings.HasPrefix(au.File, "audio-") {
			t.Fatalf("录像里的音频必须是它自己那份（audio-N.wav），实际 %s", au.File)
		}
		if _, err := os.Stat(filepath.Join(recDir, recordingFolder(recID), au.File)); err != nil {
			t.Fatalf("录像里的音频没落盘：%v", err)
		}
	}
	// 盘面那一份用的是另一套文件（voice-N.wav），两份互不覆盖
	voiceFiles := map[string]bool{}
	for _, c := range inst.voiceNotes().clips {
		if strings.HasPrefix(c.File, "voice-") {
			voiceFiles[c.File] = true
		}
	}
	if len(voiceFiles) != 3 {
		t.Fatalf("盘面上应当有 3 个 voice-N.wav，实际 %v", voiceFiles)
	}

	// 录制结束之后再录：只进盘面，录像已经定稿
	ev = sendVoice(t, hub, a, instID, testWAV(64))
	clip3, state3 := voicePayload(t, ev)
	if clip3["step"].(float64) != 0 {
		t.Fatalf("录制结束后录的语音不该有步号，实际 %v", clip3["step"])
	}
	if n := len(state3["clips"].([]any)); n != 4 {
		t.Fatalf("本根应当有 4 条语音，实际 %d", n)
	}
}

// TestVoiceNeedsOperatorViewerCanListen 钉住权限：观众能看能听，但不能往上加。
func TestVoiceNeedsOperatorViewerCanListen(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	sendVoice(t, hub, a, instID, testWAV(64))

	b := newFakeClient(t, hub)
	b.waitFor(MsgWelcome)
	b.send(hub, map[string]any{"type": "join_instance", "instanceId": instID})
	st := b.waitFor(MsgInstanceState)["instance"].(map[string]any)
	// 观众看到同一份清单（能试听）
	voice := st["voice"].(map[string]any)
	if n := len(voice["clips"].([]any)); n != 1 {
		t.Fatalf("观众也该看到这根的 1 条语音，实际 %d", n)
	}

	// 但观众不能录
	b.drain()
	b.send(hub, map[string]any{
		"type": "voice_add", "instanceId": instID,
		"audioData": base64.StdEncoding.EncodeToString(testWAV(64)),
	})
	if e := b.waitFor(MsgError); !strings.Contains(fmt.Sprint(e["message"]), "观看") {
		t.Fatalf("观众录音应当被拒（提示要看得懂），实际 %v", e["message"])
	}
}

// TestVoiceSurvivesRestart 语音是**实例存档**的一部分：关掉程序再进来还在，也能播。
func TestVoiceSurvivesRestart(t *testing.T) {
	root := writeTestData(t, 3)
	stateDir := t.TempDir()

	hub := newPersistentHub(t, root, stateDir)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)
	barTime := inst.Bars[inst.Cursor].Time
	ev := sendVoice(t, hub, a, instID, testWAV(320))
	clip, _ := voicePayload(t, ev)
	hub.flushState()

	// 换一个 Hub（= 重启程序）读回来
	hub2 := newPersistentHub(t, root, stateDir)
	b := newFakeClient(t, hub2)
	b.waitFor(MsgWelcome)
	b.send(hub2, map[string]any{"type": "join_instance", "instanceId": instID})
	st := b.waitFor(MsgInstanceState)["instance"].(map[string]any)
	voice := st["voice"].(map[string]any)
	clips := voice["clips"].([]any)
	if len(clips) != 1 {
		t.Fatalf("重启后应当还有 1 条语音，实际 %d", len(clips))
	}
	back := clips[0].(map[string]any)
	if back["time"] != barTime || back["id"] != clip["id"] {
		t.Fatalf("读回来的语音对不上：%v", back)
	}
	if v := hub2.mustInstance(t, instID).voiceNotes(); v.dir == "" {
		t.Fatal("恢复之后语音目录也要接上（否则能看见却播不了）")
	}
	// 编号不重用：重启后新录的必须是第 2 条（不能又叫 v1 盖掉前一条）
	ev = sendVoice(t, hub2, b, instID, testWAV(64))
	clip2, _ := voicePayload(t, ev)
	if clip2["id"] == clip["id"] {
		t.Fatalf("重启后新录的语音不该复用旧编号：%v", clip2["id"])
	}

	srv := httptest.NewServer(NewHandler(hub2, t.TempDir()))
	defer srv.Close()
	resp, err := http.Get(fmt.Sprintf("%s/api/instances/%s/voice/%s", srv.URL, instID, clip["file"]))
	if err != nil {
		t.Fatalf("取旧语音失败：%v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("重启后旧语音应当还能取回，实际 %d", resp.StatusCode)
	}
}

// TestLegacyRecordAudioStillWorks 兼容：老的 record_audio 仍然要「必须在录制中」，
// 报错、步号、state 一个都不能变（前端老版本、外部脚本还在用）。
func TestLegacyRecordAudioStillWorks(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	if err := hub.EnableRecordings(t.TempDir()); err != nil {
		t.Fatalf("启用录像目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")

	// 不在录制中：老路径必须拒绝（这是它和新入口的区别）
	a.drain()
	a.send(hub, map[string]any{
		"type": "record_audio", "instanceId": instID,
		"audioData": base64.StdEncoding.EncodeToString(testWAV(64)),
	})
	if e := a.waitFor(MsgError); !strings.Contains(fmt.Sprint(e["message"]), "没有在录制") {
		t.Fatalf("不在录制中时 record_audio 应当被拒，实际 %v", e["message"])
	}

	a.send(hub, map[string]any{"type": "record_start", "instanceId": instID})
	a.waitAction(ActRecordingStarted)
	ev := sendAudio(t, hub, a, instID, testWAV(64))
	if got := ev["recStep"].(float64); got != 2 {
		t.Fatalf("老路径录音仍然是第 2 步，实际 %v", got)
	}
	p := ev["payload"].(map[string]any)
	if p["voice"] == nil {
		t.Fatal("老路径也要把盘面那份语音状态带上（否则新界面会漏一条）")
	}
	if n := len(mustInstance(t, hub, instID).voiceNotes().clips); n != 1 {
		t.Fatalf("老路径录的音也要落在盘面上，实际 %d 条", n)
	}

	// 老的清空也走同一条路：盘面和录像一起清
	a.drain()
	a.send(hub, map[string]any{"type": "record_audio_delete", "instanceId": instID})
	cleared := a.waitAction(ActRecordingAudioCleared)
	cp := cleared["payload"].(map[string]any)
	if n := len(cp["removed"].([]any)); n != 1 {
		t.Fatalf("应当清掉 1 条，实际 %d", n)
	}
	if n := len(mustInstance(t, hub, instID).voiceNotes().clips); n != 0 {
		t.Fatalf("清空之后盘面上不该还剩语音，实际 %d 条", n)
	}
}
