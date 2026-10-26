package replay

// 交易记录（v0.18.0）的回归测试。
//
// v0.18.0 把「录制」从一个用户要记得按的开关，改成了交易本身的副产品：**开仓开始、
// 平仓结束**。所以这一组用例的重点不是「按钮好不好使」，而是四件更容易出错的事：
//
//  1. **触发条件是仓位**（TestTradeRecordingIsAutomatic）：空仓时画图、换根都不产生
//     记录；仓位从 0 变成非 0 的那一刻自动开始；回到 0 的那一刻自动收尾，并且
//     「平仓那一笔」必须在记录里 —— 那是整笔交易最该被看见的一步。
//  2. **第 1 步是开仓之前那一屏**：Base 取的是成交**之前**的盘面，所以回放从空仓演起，
//     「怎么进的场」看得见。挂单成交开仓同样如此（TestTradeRecordingStartsOnLimitFill）：
//     快照必须在 advance 之前抓，否则第一帧就已经带着仓位。
//  3. **统计口径**（TestTradeRecordingStatsAreComputed / ...ForShort）：盈亏、MFE/MAE
//     全部由后端按录到的成交和 K 线算，多头空头符号不能反；同一根内往返时两项都是 0。
//  4. **记录独立于实例**（TestTradeRecordingSurvivesInstanceDeletion）：删实例不删记录、
//     删记录不动实例，两边各自完整。
//
// 旧的手动开关（record_start / record_stop）现在只回一句「已经改为自动」的解释 ——
// 直接删掉会让老前端收到「不认识的消息类型」，那看起来像 bug（见 ...ManualSwitchesGoAway）。

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// 辅助
// ---------------------------------------------------------------------------

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

// sendAudio 走的是**老入口** record_audio —— 它现在是 voice_add 的别名。
// 交易记录测试也用它：老前端和外部脚本还在发这个类型，它必须照旧能录。
func sendAudio(t *testing.T, hub *Hub, fc *fakeClient, instID string, wav []byte) map[string]any {
	t.Helper()
	fc.send(hub, map[string]any{
		"type":       "record_audio",
		"instanceId": instID,
		"audioData":  base64.StdEncoding.EncodeToString(wav),
		"durationMs": 1000,
	})
	return fc.waitAction(ActVoiceAdded)
}

// audioStep 取广播里「这段语音在交易记录里是第几步」（没有记录时是 0）。
func audioStep(t *testing.T, ev map[string]any) int {
	t.Helper()
	p, ok := ev["payload"].(map[string]any)
	if !ok {
		t.Fatalf("广播没有 payload：%v", ev)
	}
	clip, ok := p["audio"].(map[string]any)
	if !ok {
		t.Fatalf("广播里没有 audio：%v", p)
	}
	n, _ := clip["step"].(float64)
	return int(n)
}

// marketOrder 下市价单并等成交广播，返回 payload。
func marketOrder(t *testing.T, hub *Hub, fc *fakeClient, instID, side string, qty float64) map[string]any {
	t.Helper()
	fc.send(hub, map[string]any{
		"type": "order_market", "instanceId": instID, "side": side, "quantity": qty,
	})
	p, ok := fc.waitAction(ActOrderFilled)["payload"].(map[string]any)
	if !ok {
		t.Fatalf("成交广播没有 payload")
	}
	return p
}

// tradePrice 取成交广播里那一笔的成交价。
func tradePrice(t *testing.T, payload map[string]any) float64 {
	t.Helper()
	tr, ok := payload["trade"].(map[string]any)
	if !ok {
		t.Fatalf("成交广播里没有 trade：%v", payload)
	}
	price, _ := tr["price"].(float64)
	if price <= 0 {
		t.Fatalf("成交价不对：%v", tr)
	}
	return price
}

// addRay 在当前 K 线上画一条射线。
func addRay(t *testing.T, hub *Hub, fc *fakeClient, instID string, from, to Bar) map[string]any {
	t.Helper()
	fc.send(hub, map[string]any{
		"type": "annotation_add", "instanceId": instID, "kind": AnnRay,
		"start": map[string]any{"time": from.Time, "price": from.Close},
		"end":   map[string]any{"time": to.Time, "price": to.High},
	})
	return fc.waitAction(ActAnnotationAdded)
}

// tradeSteps 取「这一笔正在记的」步。
func tradeSteps(t *testing.T, inst *Instance) []RecordingStep {
	t.Helper()
	if inst.trade == nil {
		t.Fatal("这一刻应当在记一笔交易，实际没有")
	}
	return inst.trade.rec.Steps
}

// near 比较浮点（录像里存的是 JSON，往返一趟可能有末位误差）。
func near(got, want float64) bool { return math.Abs(got-want) < 1e-6 }

func wantNear(t *testing.T, what string, got, want float64) {
	t.Helper()
	if !near(got, want) {
		t.Fatalf("%s 应当是 %.4f，实际 %.4f", what, want, got)
	}
}

// num 取 map 里的数字（缺失按 0）。
func numOf(m map[string]any, key string) float64 {
	v, _ := m[key].(float64)
	return v
}

// snippetForLog 把一段 JSON 截短，失败信息里不要把整份记录（几百根 K 线）都打出来。
//
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

// ---------------------------------------------------------------------------
// 主用例：整套流程走一遍协议
// ---------------------------------------------------------------------------

// TestTradeRecordingIsAutomatic 是这次改动的核心用例。步序表（一步一个操作）：
//
//	空仓阶段（不进记录）
//	  画射线、点【下一根】
//
//	市价买入 2 手 → 记录自动开始
//	1 base             开仓**之前**那一屏（空仓，但已经有那条射线）
//	2 order_filled     开仓成交
//	3 annotation_added 持仓中画一条
//	4 recording_audio  持仓中讲一段（语音自己占一步）
//	5 bar_advanced     换一根（全程持仓、无成交 → 唯一能算 MFE/MAE 的那一根）
//	6 order_filled     平仓 → 记录自动收尾
//
// 然后验：通知里的统计、落盘文件、读回来的步序、重放等价、删除互不影响。
func TestTradeRecordingIsAutomatic(t *testing.T) {
	hub, _ := newTestHub(t, 3)
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	// ---- 空仓：画图 + 换根都不该开始记录 ----
	firstBar := inst.Bars[inst.Cursor]
	prevBar := inst.Bars[inst.Cursor-1]
	addRay(t, hub, a, instID, prevBar, firstBar)
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	if n := inst.TradeRecordCount(); n != 0 {
		t.Fatalf("空仓时不该开始记录，实际记了 %d 笔", n)
	}
	if n := len(hub.Records().List()); n != 0 {
		t.Fatalf("空仓时不该有交易记录，实际 %d 条", n)
	}

	// ---- 市价开仓：自动开始，第 1 步是「开仓之前」那一屏 ----
	openBar := inst.Bars[inst.Cursor]
	openCursor := inst.Cursor
	entryPrice := tradePrice(t, marketOrder(t, hub, a, instID, "buy", 2))
	if inst.TradeRecordCount() != 1 {
		t.Fatal("开仓之后应当自动开始记一笔交易")
	}
	steps := tradeSteps(t, inst)
	if len(steps) != 2 {
		t.Fatalf("开仓之后应当有 2 步（开仓前那一屏 + 开仓成交），实际 %d 步", len(steps))
	}
	if steps[0].Kind != StepBase || len(steps[0].Events) != 0 {
		t.Fatalf("第 1 步应当是起点（无事件），实际 %+v", steps[0])
	}
	if steps[0].Time != openBar.Time || steps[0].Cursor != openCursor {
		t.Fatalf("第 1 步应当锚在开仓那根 K 线 %s（游标 %d），实际 %s / %d",
			openBar.Time, openCursor, steps[0].Time, steps[0].Cursor)
	}
	if steps[0].By != "human" {
		t.Fatalf("记录应当记下是谁开的这一笔，实际 %q", steps[0].By)
	}
	base := inst.trade.rec.Base
	if base.Position.Quantity != 0 {
		t.Fatalf("第 1 步必须是**空仓**那一屏，实际仓位 %v", base.Position)
	}
	if len(base.Annotations) != 1 {
		t.Fatalf("第 1 步应当包含开仓前画的那条射线，实际 %d 个图形", len(base.Annotations))
	}
	if steps[1].Kind != ActOrderFilled || len(steps[1].Events) != 1 {
		t.Fatalf("第 2 步应当是开仓成交，实际 %+v", steps[1])
	}
	if steps[1].Events[0].Action != ActOrderFilled || steps[1].Events[0].By != "human" {
		t.Fatalf("第 2 步的事件不对：%+v", steps[1].Events[0])
	}

	// ---- 持仓期间：机器人动手（同一份记录）、真人画图、说一段、换根 ----
	//
	// 机器人走的是和真人一模一样的 do*Locked，所以它的两步也会出现在**同一份记录**里
	// （by="bot"）—— 这就是「用户操作与机器人操作完全等价」在数据上的样子。
	hub.mu.Lock()
	hub.botTickProbeLocked(inst) // 画一条（第 3 步）+ 换根（第 4 步）
	hub.mu.Unlock()
	if n := len(tradeSteps(t, inst)); n != 4 {
		t.Fatalf("机器人动手之后应当是第 4 步，实际 %d 步", n)
	}
	addRay(t, hub, a, instID, openBar, openBar)
	if got := audioStep(t, sendAudio(t, hub, a, instID, testWAV(400))); got != 6 {
		t.Fatalf("持仓中录的语音应当是第 6 步（语音就是一次操作），实际 %d", got)
	}
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	exitBar := inst.Bars[inst.Cursor]
	if n := len(tradeSteps(t, inst)); n != 7 {
		t.Fatalf("再换一根之后应当是第 7 步，实际 %d 步", n)
	}

	// 统计只能拿「全程持仓、且这一根没有成交」的整根 K 线算 MFE/MAE
	// （开仓那一根、出场那一根都不算 —— 一根 K 线看不出高低点谁先来）
	pv := inst.pointValue()
	var wantMfe, wantMae float64
	for _, b := range inst.Bars[openCursor+1 : inst.Cursor] {
		wantMfe = math.Max(wantMfe, math.Max((b.High-entryPrice)*2*pv, (b.Low-entryPrice)*2*pv))
		wantMae = math.Min(wantMae, math.Min((b.High-entryPrice)*2*pv, (b.Low-entryPrice)*2*pv))
	}
	wantBarsHeld := inst.Cursor - (openCursor + 1)

	// ---- 平仓：自动收尾，并收到一条 trade_recorded ----
	exitPrice := tradePrice(t, marketOrder(t, hub, a, instID, "sell", 2))
	if n := inst.TradeRecordCount(); n != 0 {
		t.Fatalf("平仓之后记录应当自动收尾，实际还记着 %d 笔", n)
	}
	msg := a.waitFor(MsgTradeRecorded)
	if dir, _ := msg["dir"].(string); dir != recDir {
		t.Fatalf("记录应当落在 %s，实际 %q", recDir, msg["dir"])
	}
	if w, _ := msg["warning"].(string); w != "" {
		t.Fatalf("正常保存不该带警告，实际 %q", w)
	}
	sum, ok := msg["recording"].(map[string]any)
	if !ok {
		t.Fatalf("通知里没有记录摘要：%v", msg)
	}
	recID, _ := sum["id"].(string)
	if recID == "" {
		t.Fatalf("记录没有 id：%v", sum)
	}
	if sum["instanceId"] != instID {
		t.Fatalf("摘要应当记下发生在哪个实例上，实际 %v", sum["instanceId"])
	}
	if got := int(numOf(sum, "stepCount")); got != 8 {
		t.Fatalf("整笔交易应当是 8 步（入场前 / 开仓 / 机器人画图 / 机器人换根 / 真人画图 / 语音 / 换根 / 平仓），实际 %d", got)
	}
	if got := int(numOf(sum, "bars")); got != 3 {
		t.Fatalf("记录里推进了 3 根 K 线（入场那根 + 换了两次），实际 %d", got)
	}
	if got := int(numOf(sum, "eventCount")); got != 6 {
		t.Fatalf("改盘面的操作应当有 6 个（语音步不算），实际 %d", got)
	}
	if got := int(numOf(sum, "audioCount")); got != 1 {
		t.Fatalf("记录里应当有 1 条语音，实际 %d", got)
	}

	// ---- 统计：一并算好，前端只排版 ----
	stats, ok := sum["stats"].(map[string]any)
	if !ok {
		t.Fatalf("摘要里没有 stats：%v", sum)
	}
	if stats["side"] != "long" {
		t.Fatalf("方向应当是 long，实际 %v", stats["side"])
	}
	wantNear(t, "开仓手数", numOf(stats, "quantity"), 2)
	wantNear(t, "最大手数", numOf(stats, "maxQuantity"), 2)
	if got := int(numOf(stats, "fills")); got != 2 {
		t.Fatalf("这一笔应当有 2 次成交（开 + 平），实际 %d", got)
	}
	if stats["closed"] != true {
		t.Fatalf("正常平完的一笔应当标成 closed：%v", stats["closed"])
	}
	if stats["entryTime"] != openBar.Time || stats["exitTime"] != exitBar.Time {
		t.Fatalf("进出场时间应当是 %s → %s，实际 %v → %v",
			openBar.Time, exitBar.Time, stats["entryTime"], stats["exitTime"])
	}
	wantNear(t, "开仓价", numOf(stats, "entryPrice"), entryPrice)
	wantNear(t, "平仓价", numOf(stats, "exitPrice"), exitPrice)
	wantNear(t, "已实现盈亏", numOf(stats, "realizedPnl"), (exitPrice-entryPrice)*2*pv)
	wantNear(t, "最大有利波动", numOf(stats, "mfe"), wantMfe)
	wantNear(t, "最大不利波动", numOf(stats, "mae"), wantMae)
	if got := int(numOf(stats, "barsHeld")); got != wantBarsHeld {
		t.Fatalf("持仓走过的整根 K 线应当是 %d 根，实际 %d", wantBarsHeld, got)
	}
	if got := int(numOf(stats, "steps")); got != 8 {
		t.Fatalf("统计里的步数应当和摘要一致（8），实际 %d", got)
	}

	// ---- 落盘结构：recording.json + audio-1.wav ----
	folder := filepath.Join(recDir, recordingFolder(recID))
	recPath := filepath.Join(folder, recordingFileName)
	if _, err := os.Stat(recPath); err != nil {
		t.Fatalf("记录没有落盘（%s）：%v", recPath, err)
	}
	if _, err := os.Stat(filepath.Join(folder, "audio-1.wav")); err != nil {
		t.Fatalf("语音没有随记录落盘：%v", err)
	}

	// ---- 列表 / 详情 ----
	a.send(hub, map[string]any{"type": "list_recordings"})
	list := a.waitFor(MsgRecordings)
	items, _ := list["recordings"].([]any)
	if len(items) != 1 {
		t.Fatalf("记录列表应当有 1 条，实际 %d", len(items))
	}
	a.send(hub, map[string]any{"type": "get_recording", "recordingId": recID})
	raw, _ := json.Marshal(a.waitFor(MsgRecording)["recording"])
	var rec Recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("记录反序列化失败：%v", err)
	}
	if rec.Format != RecordingFormat {
		t.Fatalf("新记录应当是 format=%d，实际 %d", RecordingFormat, rec.Format)
	}
	// 数组字段必须序列化成 `[]` 而不是 `null`：前端步进条直接读 `s.events.length`，
	// null 会让「打开回放」当场抛 TypeError（2026-10 实测：整个播放器渲染不出来）。
	if !strings.Contains(string(raw), `"events":[]`) {
		t.Fatalf("events 不能是 null，必须是 []：%s", snippetForLog(raw))
	}
	wantKind := []string{
		StepBase, ActOrderFilled, ActAnnotationAdded, ActBarAdvanced,
		ActAnnotationAdded, ActRecordingAudio, ActBarAdvanced, ActOrderFilled,
	}
	if len(rec.Steps) != len(wantKind) {
		t.Fatalf("读回来的记录应当有 %d 步，实际 %d", len(wantKind), len(rec.Steps))
	}
	for i, st := range rec.Steps {
		if st.Step != i+1 {
			t.Fatalf("第 %d 个元素的步号应当是 %d，实际 %d（步号必须连续）", i, i+1, st.Step)
		}
		if st.Kind != wantKind[i] {
			t.Fatalf("第 %d 步的类型应当是 %s，实际 %s", i+1, wantKind[i], st.Kind)
		}
		if st.Time == "" || st.At == 0 {
			t.Fatalf("第 %d 步缺盘面锚点或真实时刻：%+v", st.Step, st)
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
	if rec.Steps[5].Audio == nil || rec.Steps[5].Audio.ID != rec.Audios[0].ID {
		t.Fatalf("第 6 步应当挂着那段语音，实际 %+v", rec.Steps[5].Audio)
	}
	// 机器人画的那条也在同一份记录里（by=bot），机器人没有后门
	if rec.Steps[2].Events[0].By != "bot" {
		t.Fatalf("第 3 步应当是机器人画的线：%+v", rec.Steps[2].Events[0])
	}
	if rec.Steps[4].Events[0].By != "human" {
		t.Fatalf("第 5 步应当是真人画的线：%+v", rec.Steps[4].Events[0])
	}
	// 画图和说话发生在同一根 K 线上，但必须是**两步**（「一步 = 一次操作」要的形状）
	if rec.Steps[4].Time != rec.Steps[5].Time {
		t.Fatalf("第 5、6 步应当在同一条 K 线上（%s / %s）", rec.Steps[4].Time, rec.Steps[5].Time)
	}

	// ---- 重放等价：Base + 事件演一遍，最终状态必须和实例完全一致 ----
	replayed := replayRecording(t, &rec)
	assertSameState(t, inst, replayed)
	for _, st := range rec.Steps {
		if partial := replaySteps(t, &rec, st.Step); partial.Cursor != st.Cursor {
			t.Fatalf("演到第 %d 步时游标应当是 %d，实际 %d", st.Step, st.Cursor, partial.Cursor)
		}
	}

	// ---- 音频能通过 HTTP 路由取回 ----
	srv := httptest.NewServer(NewHandler(hub, t.TempDir()))
	defer srv.Close()
	resp, err := http.Get(fmt.Sprintf("%s/api/recordings/%s/audio/audio-1.wav", srv.URL, recID))
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
	bad, err := http.Get(fmt.Sprintf("%s/api/recordings/%s/audio/..%%2Frecording.json", srv.URL, recID))
	if err == nil {
		defer bad.Body.Close()
		if bad.StatusCode == http.StatusOK {
			t.Fatal("不该能通过 audio 路由读到 recording.json")
		}
	}

	// ---- 删记录不动实例：盘面、成交、盈亏一条都不能少 ----
	before := len(inst.Trades)
	pnlBefore := inst.RealizedPnl
	a.send(hub, map[string]any{"type": "delete_recording", "recordingId": recID})
	after := a.waitFor(MsgRecordings)
	if n := len(after["recordings"].([]any)); n != 0 {
		t.Fatalf("删除后列表应当是空的，实际 %d 条", n)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("删除后记录目录应当没了，实际 err=%v", err)
	}
	alive := mustInstance(t, hub, instID)
	if len(alive.Trades) != before {
		t.Fatalf("删记录不该动实例的成交：删前 %d 笔、删后 %d 笔", before, len(alive.Trades))
	}
	if !near(alive.RealizedPnl, pnlBefore) {
		t.Fatalf("删记录不该动实例的盈亏：%v → %v", pnlBefore, alive.RealizedPnl)
	}
	if len(alive.Annotations) != 3 {
		t.Fatalf("删记录不该动实例的图形（开仓前 1 个 + 机器人 1 个 + 持仓中 1 个 = 3），实际 %d", len(alive.Annotations))
	}

	// ---- 再开一笔：记录按仓位自动重来，不会因为「已经记过一次」而失效 ----
	marketOrder(t, hub, a, instID, "buy", 1)
	if n := alive.TradeRecordCount(); n != 1 {
		t.Fatal("第二次开仓应当再自动开始一笔新记录")
	}
	if got := tradeSteps(t, alive)[0].Kind; got != StepBase {
		t.Fatalf("新一笔的第 1 步仍然是起点，实际 %s", got)
	}
}

// TestTradeRecordingStatsAreComputed 专门钉空头的统计符号。
//
// 多头那一条已经由主用例覆盖，但「空头时 High 是浮亏、Low 是浮盈」正好相反 ——
// 这里如果写反，数字看起来照样"合理"（都是正数/负数），只有拿一个只有空头的
// 用例才能发现。
func TestTradeRecordingStatsAreComputed(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	if err := hub.EnableRecordings(t.TempDir()); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	// 卖开 1 手 → 空头
	entryPrice := tradePrice(t, marketOrder(t, hub, a, instID, "sell", 1))
	// 走两根：第一根是「全程持仓且无成交」，第二根是出场那根（它的高低点不算）
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	holdBar := inst.Bars[inst.Cursor]
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	pv := inst.pointValue()
	// 空头：数量是负的，(high - avg) * qty 天然是负的（浮亏），(low - avg) * qty 是正的
	up := (holdBar.High - entryPrice) * -1 * pv
	dn := (holdBar.Low - entryPrice) * -1 * pv

	exitPrice := tradePrice(t, marketOrder(t, hub, a, instID, "buy", 1))
	sum := a.waitFor(MsgTradeRecorded)["recording"].(map[string]any)
	stats, _ := sum["stats"].(map[string]any)
	if stats["side"] != "short" {
		t.Fatalf("方向应当是 short，实际 %v", stats["side"])
	}
	wantNear(t, "空头已实现盈亏", numOf(stats, "realizedPnl"), (entryPrice-exitPrice)*1*pv)
	wantNear(t, "空头 MFE", numOf(stats, "mfe"), math.Max(0, math.Max(up, dn)))
	wantNear(t, "空头 MAE", numOf(stats, "mae"), math.Min(0, math.Min(up, dn)))
	if numOf(stats, "mfe") < 0 || numOf(stats, "mae") > 0 {
		t.Fatalf("MFE 不该是负的、MAE 不该是正的：%v / %v", stats["mfe"], stats["mae"])
	}
	// 出场那一根不算：持仓走过的整根 K 线只有 1 根
	if got := int(numOf(stats, "barsHeld")); got != 1 {
		t.Fatalf("barsHeld 应当是 1（出场那根不算），实际 %d", got)
	}
}

// TestTradeRecordingStartsOnLimitFill 钉住「挂单成交开仓也要能被记上，而且第 1 步
// 仍然在成交之前」。
//
// 这条路径和第 2 步的市价成交不同：快照必须在 advance **之前**抓（见 doStepLocked），
// 否则记录的第一帧就已经带着仓位，「怎么进的场」永远看不到。第 2 步是一条
// bar_advanced（成交撮合的结果就带在它的 Fills 里），不是 order_filled。
func TestTradeRecordingStartsOnLimitFill(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	if err := hub.EnableRecordings(t.TempDir()); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	bar := inst.Bars[inst.Cursor]
	// 买在现价：下一根的低点一定会打穿它
	a.send(hub, map[string]any{
		"type": "order_place", "instanceId": instID,
		"side": "buy", "kind": KindLimit, "price": bar.Close, "quantity": 1,
	})
	a.waitAction(ActOrderPlaced)
	if inst.TradeRecordCount() != 0 {
		t.Fatal("挂单还没成交，不该开始记录（空仓）")
	}

	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	adv := a.waitAction(ActBarAdvanced)
	fills, _ := adv["payload"].(map[string]any)["fills"].([]any)
	if len(fills) != 1 {
		t.Fatalf("这一根应当撮合掉那张挂单，实际 %d 笔成交", len(fills))
	}
	if inst.TradeRecordCount() != 1 {
		t.Fatal("挂单成交把空仓变成有仓，应当自动开始记录")
	}
	steps := tradeSteps(t, inst)
	if len(steps) != 2 {
		t.Fatalf("应当有 2 步（成交前那一屏 + 成交那一根），实际 %d", len(steps))
	}
	if steps[0].Kind != StepBase || steps[0].Time != bar.Time {
		t.Fatalf("第 1 步应当锚在成交之前那根 %s，实际 %+v", bar.Time, steps[0])
	}
	if q := inst.trade.rec.Base.Position.Quantity; q != 0 {
		t.Fatalf("第 1 步必须是空仓那一屏，实际仓位 %v", q)
	}
	if n := len(inst.trade.rec.Base.Orders); n != 1 {
		t.Fatalf("第 1 步应当还带着那张挂单（成交发生在下一步），实际 %d 张", n)
	}
	if steps[1].Kind != ActBarAdvanced {
		t.Fatalf("第 2 步应当是换根（成交结果在 Fills 里），实际 %s", steps[1].Kind)
	}
	if steps[1].Time != inst.Bars[inst.Cursor].Time {
		t.Fatalf("第 2 步应当锚在成交那一根，实际 %s", steps[1].Time)
	}

	// 直接平掉：这一笔一根都没完整持仓过，MFE/MAE 都是 0
	marketOrder(t, hub, a, instID, "sell", 1)
	stats := a.waitFor(MsgTradeRecorded)["recording"].(map[string]any)["stats"].(map[string]any)
	if stats["closed"] != true {
		t.Fatalf("平完之后应当是 closed：%v", stats["closed"])
	}
	if got := int(numOf(stats, "fills")); got != 2 {
		t.Fatalf("应当有 2 次成交（挂单成交 + 平仓），实际 %d", got)
	}
	wantNear(t, "MFE", numOf(stats, "mfe"), 0)
	wantNear(t, "MAE", numOf(stats, "mae"), 0)
	if got := int(numOf(stats, "barsHeld")); got != 0 {
		t.Fatalf("没有完整持仓过的 K 线，barsHeld 应当是 0，实际 %d", got)
	}
}

// TestTradeRecordingSameBarRoundTrip 钉住「同一根 K 线里开了又平」：
// 那也是一笔完整的交易，记录不能丢，而且 MFE/MAE 都是 0（看不出高低点谁先来）。
func TestTradeRecordingSameBarRoundTrip(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	bar := inst.Bars[inst.Cursor]
	entry := tradePrice(t, marketOrder(t, hub, a, instID, "buy", 3))
	if n := len(tradeSteps(t, inst)); n != 2 {
		t.Fatalf("开仓后应当是 2 步，实际 %d", n)
	}
	exit := tradePrice(t, marketOrder(t, hub, a, instID, "sell", 3))
	if entry != exit {
		t.Fatalf("同一根 K 线内往返的成交价应当相同，实际 %v / %v", entry, exit)
	}
	sum := a.waitFor(MsgTradeRecorded)["recording"].(map[string]any)
	stats := sum["stats"].(map[string]any)
	if got := int(numOf(sum, "stepCount")); got != 3 {
		t.Fatalf("同一根内往返应当是 3 步（起点 + 开 + 平），实际 %d", got)
	}
	if stats["closed"] != true || numOf(stats, "fills") != 2 {
		t.Fatalf("往返应当是一笔已平完的交易：%v", stats)
	}
	wantNear(t, "往返盈亏", numOf(stats, "realizedPnl"), 0)
	wantNear(t, "MFE", numOf(stats, "mfe"), 0)
	wantNear(t, "MAE", numOf(stats, "mae"), 0)
	if got := int(numOf(stats, "barsHeld")); got != 0 {
		t.Fatalf("同一根内往返不该有持仓根数，实际 %d", got)
	}
	// 名字要能一眼看出是哪一笔
	name, _ := sum["name"].(string)
	if !strings.Contains(name, "多") || !strings.Contains(name, "3 手") {
		t.Fatalf("记录名字应当带方向和手数，实际 %q", name)
	}
	// 同一根 K 线上的三步必须挂着同一个时间锚点
	a.send(hub, map[string]any{"type": "get_recording", "recordingId": sum["id"]})
	var rec Recording
	raw, _ := json.Marshal(a.waitFor(MsgRecording)["recording"])
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("记录反序列化失败：%v", err)
	}
	for _, st := range rec.Steps {
		if st.Time != bar.Time {
			t.Fatalf("第 %d 步应当锚在 %s，实际 %s", st.Step, bar.Time, st.Time)
		}
	}
}

// TestTradeRecordingIgnoresManualSwitches 钉住「开关没了，但老消息不会变成哑巴」：
// record_start / record_stop 仍然认，回一句「已经改为自动」的解释；期间不会有任何记录。
func TestTradeRecordingIgnoresManualSwitches(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	if err := hub.EnableRecordings(t.TempDir()); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	for _, typ := range []string{"record_start", "record_stop"} {
		a.drain()
		a.send(hub, map[string]any{"type": typ, "instanceId": instID})
		e := a.waitFor(MsgError)
		msg, _ := e["message"].(string)
		if !strings.Contains(msg, "自动") {
			t.Fatalf("%s 应当解释「已经改为自动」，实际 %q", typ, msg)
		}
	}
	if n := inst.TradeRecordCount(); n != 0 {
		t.Fatalf("手动开关不该让记录开始，实际 %d 笔", n)
	}
	if n := len(hub.Records().List()); n != 0 {
		t.Fatalf("手动开关不该产生记录，实际 %d 条", n)
	}
	// 空仓做操作也不产生记录
	addRay(t, hub, a, instID, inst.Bars[inst.Cursor], inst.Bars[inst.Cursor])
	if n := inst.TradeRecordCount(); n != 0 {
		t.Fatalf("空仓画图不该开始记录，实际 %d 笔", n)
	}
}

// TestTradeRecordingSurvivesInstanceDeletion 钉住「记录独立于实例」：
// 删实例时这一笔**立即收尾并保存**（Closed 仍是 false —— 那笔仓位确实还没平），
// 记录文件留在 <recordingsDir> 下，和实例存档目录互不牵连。
func TestTradeRecordingSurvivesInstanceDeletion(t *testing.T) {
	root := writeTestData(t, 2)
	stateDir := t.TempDir()
	recDir := t.TempDir()
	hub := newPersistentHub(t, root, stateDir)
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	marketOrder(t, hub, a, instID, "buy", 2)
	addRay(t, hub, a, instID, inst.Bars[inst.Cursor], inst.Bars[inst.Cursor])
	hub.flushState() // 先把实例存档写出来，删之前它确实存在

	a.send(hub, map[string]any{"type": "delete_instance", "instanceId": instID})
	if m := a.waitFor(MsgDeleted); m["instanceId"] != instID {
		t.Fatalf("删除回执不对：%v", m)
	}
	// 实例已经没了，但这条记录刚被保存 —— 通知仍然要发出去（否则列表看着像丢了）
	msg := a.waitFor(MsgTradeRecorded)
	sum := msg["recording"].(map[string]any)
	if sum["instanceId"] != instID {
		t.Fatalf("记录应当记下原实例 id（追溯用），实际 %v", sum["instanceId"])
	}
	stats := sum["stats"].(map[string]any)
	if stats["closed"] != false {
		t.Fatalf("实例被删时仓位还在，closed 必须是 false：%v", stats["closed"])
	}
	if got := int(numOf(stats, "fills")); got != 1 {
		t.Fatalf("这一笔只成交过一次（开仓），实际 %d", got)
	}

	recID := sum["id"].(string)
	recPath := filepath.Join(recDir, recordingFolder(recID), recordingFileName)
	if _, err := os.Stat(recPath); err != nil {
		t.Fatalf("记录没有落盘：%v", err)
	}
	hub.flushState()
	if _, err := os.Stat(filepath.Join(stateDir, "instances", instanceFolder(instID))); !os.IsNotExist(err) {
		t.Fatalf("实例存档目录应当被删掉，实际 err=%v", err)
	}

	// 重新打开记录目录（= 重启程序）：崩溃/删除留下的「持仓中」记录不能被当成坏文件
	st, err := OpenRecordingStore(recDir, t.Logf)
	if err != nil {
		t.Fatalf("打开记录目录失败：%v", err)
	}
	items := st.List()
	if len(items) != 1 {
		t.Fatalf("重新扫描后应当还有 1 条记录，实际 %d", len(items))
	}
	if items[0].ID != recID || items[0].Stats.Closed {
		t.Fatalf("读回来的摘要不对：%+v", items[0])
	}
	if !strings.Contains(items[0].Name, "持仓中") {
		t.Fatalf("没平完的记录名字里应当写「持仓中」，实际 %q", items[0].Name)
	}
	rec, err := st.Load(recID)
	if err != nil {
		t.Fatalf("读回记录失败：%v", err)
	}
	if rec.Base.Position.Quantity != 0 || len(rec.Steps) != 3 {
		t.Fatalf("记录内容不对：仓位 %v / %d 步", rec.Base.Position, len(rec.Steps))
	}
}

// TestTradeRecordingStepsFollowRealOperations 是需求原话的形状：
// 「点击下一根 K 线」→「绘制图形」→「录音」→「再次绘制图形」，**四次操作 = 四步**。
//
// v0.14 及以前这四件事全落在同一根 K 线的那一步里，回放时一次性摆出来，
// 看不出用户先画了线、再讲了话、又画了一条。这条用例把「逐步出现」钉死。
func TestTradeRecordingStepsFollowRealOperations(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	if err := hub.EnableRecordings(t.TempDir()); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	marketOrder(t, hub, a, instID, "buy", 1) // 第 1、2 步
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	bar := inst.Bars[inst.Cursor]

	addRay(t, hub, a, instID, bar, bar)
	if got := audioStep(t, sendAudio(t, hub, a, instID, testWAV(64))); got != 5 {
		t.Fatalf("语音应当是第 5 步，实际 %d", got)
	}
	addRay(t, hub, a, instID, bar, bar)

	steps := tradeSteps(t, inst)
	want := []string{
		StepBase, ActOrderFilled, ActBarAdvanced,
		ActAnnotationAdded, ActRecordingAudio, ActAnnotationAdded,
	}
	if len(steps) != len(want) {
		t.Fatalf("应当是 %d 步，实际 %d 步", len(want), len(steps))
	}
	for i, k := range want {
		if steps[i].Kind != k {
			t.Fatalf("第 %d 步应当是 %s，实际 %s", i+1, k, steps[i].Kind)
		}
	}
	// 后四步都发生在同一根 K 线上（除了第 3 步本身就是换根）
	for _, st := range steps[2:] {
		if st.Time != bar.Time {
			t.Fatalf("这几步都该挂在 %s 上，第 %d 步却是 %s", bar.Time, st.Step, st.Time)
		}
	}
	if len(steps[4].Events) != 0 || steps[4].Audio == nil {
		t.Fatalf("语音步不该有事件、但必须带着那段音频：%+v", steps[4])
	}

	// 逐步出现：演到第 4 步只有 1 个图形，第 5 步还只有 1 个（语音不改盘面），第 6 步才有 2 个
	rec := inst.trade.rec
	if n := len(replaySteps(t, rec, 4).Annotations); n != 1 {
		t.Fatalf("演到第 4 步应当只有 1 个图形，实际 %d", n)
	}
	if n := len(replaySteps(t, rec, 5).Annotations); n != 1 {
		t.Fatalf("语音那一步不该改变盘面（还是 1 个图形），实际 %d", n)
	}
	if n := len(replaySteps(t, rec, 6).Annotations); n != 2 {
		t.Fatalf("演到第 6 步应当有 2 个图形，实际 %d", n)
	}
}

// TestTradeRecordingAudioLimit 钉住「**同一根 K 线**最多 9 条，整条记录不限」。
//
// 这条限制的用处是防止用户在同一根 K 线上反复重录；换到下一根，额度就该重新从 0
// 开始。语音步自己占一步，但额度按 K 线数，不按步数 —— 否则同一根 K 线上画几个
// 图形就会莫名其妙少几条额度。
func TestTradeRecordingAudioLimit(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	if err := hub.EnableRecordings(t.TempDir()); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	marketOrder(t, hub, a, instID, "buy", 1) // 第 1、2 步
	// 不是 WAV 的二进制不收（先验这条，否则录满之后只会先报「已经录满了」）
	a.send(hub, map[string]any{
		"type": "record_audio", "instanceId": instID,
		"audioData": base64.StdEncoding.EncodeToString([]byte("not a wav file at all................")),
	})
	if e := a.waitFor(MsgError); !strings.Contains(fmt.Sprint(e["message"]), "WAV") {
		t.Fatalf("非 WAV 数据应当被拒，实际 %v", e["message"])
	}

	wav := testWAV(64)
	for i := 0; i < maxAudiosPerBar; i++ {
		if got := audioStep(t, sendAudio(t, hub, a, instID, wav)); got != i+3 {
			t.Fatalf("第 %d 段语音应当是第 %d 步，实际 %d", i+1, i+3, got)
		}
	}
	if n := len(tradeSteps(t, inst)); n != maxAudiosPerBar+2 {
		t.Fatalf("起点 + 开仓 + %d 段语音 = %d 步，实际 %d", maxAudiosPerBar, maxAudiosPerBar+2, n)
	}

	// 第 10 条：明确报错，而不是静默丢掉，也不该多出一步
	a.drain()
	a.send(hub, map[string]any{
		"type": "record_audio", "instanceId": instID,
		"audioData": base64.StdEncoding.EncodeToString(wav),
	})
	if e := a.waitFor(MsgError); !strings.Contains(fmt.Sprint(e["message"]), "最多") {
		t.Fatalf("超限时应当明确报错，实际 %v", e["message"])
	}
	if n := len(tradeSteps(t, inst)); n != maxAudiosPerBar+2 {
		t.Fatalf("被拒的那条不该占一步，实际 %d 步", n)
	}

	// 换一根 → 额度重新开始（这就是「整条记录不限」）
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	if got := audioStep(t, sendAudio(t, hub, a, instID, wav)); got != maxAudiosPerBar+4 {
		t.Fatalf("新一根上的第一条应当接着排在第 %d 步，实际 %d", maxAudiosPerBar+4, got)
	}

	// 平仓收尾：记录里 10 条语音、14 步
	marketOrder(t, hub, a, instID, "sell", 1)
	sum := a.waitFor(MsgTradeRecorded)["recording"].(map[string]any)
	if got := int(numOf(sum, "audioCount")); got != maxAudiosPerBar+1 {
		t.Fatalf("记录里应当有 %d 条语音，实际 %d", maxAudiosPerBar+1, got)
	}
	if got := int(numOf(sum, "stepCount")); got != maxAudiosPerBar+5 {
		t.Fatalf("记录应当是 %d 步，实际 %d", maxAudiosPerBar+5, got)
	}
}

// TestTradeRecordingClearBarAudio 钉住「一键清空当前这根 K 线的语音」：
// 只清本根、磁盘上的文件也删掉、**那几步跟着一起摘掉且步号重排**、清完还能接着录。
func TestTradeRecordingClearBarAudio(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	marketOrder(t, hub, a, instID, "buy", 1) // 第 1、2 步
	wav := testWAV(64)
	sendAudio(t, hub, a, instID, wav)
	if got := audioStep(t, sendAudio(t, hub, a, instID, wav)); got != 4 {
		t.Fatalf("第 2 段语音应当是第 4 步，实际 %d", got)
	}

	// 记录进行中语音已经落盘（浮窗的预览播放要靠它）
	dir := inst.trade.dir
	if dir == "" {
		t.Fatal("配了记录目录，进行中的记录也该有目录（语音要能试听）")
	}
	for _, name := range []string{"audio-1.wav", "audio-2.wav"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("记录进行中 %s 就该在磁盘上（预览播放要用）：%v", name, err)
		}
	}

	// 清空本根（老入口 = 新入口，两者同一条路）
	a.drain()
	a.send(hub, map[string]any{"type": "record_audio_delete", "instanceId": instID})
	cleared := a.waitAction(ActVoiceCleared)
	p := cleared["payload"].(map[string]any)
	if n := len(p["removed"].([]any)); n != 2 {
		t.Fatalf("应当删掉 2 条，实际 %d", n)
	}
	// 语音步被一起摘掉：只剩起点 + 开仓两步，步号重排
	steps := tradeSteps(t, inst)
	if len(steps) != 2 {
		t.Fatalf("清空后应当只剩 2 步（起点 + 开仓），实际 %d", len(steps))
	}
	for i, st := range steps {
		if st.Step != i+1 {
			t.Fatalf("清空重排之后步号必须连续：第 %d 个元素是 %d", i, st.Step)
		}
	}
	if n := len(inst.trade.rec.Audios); n != 0 {
		t.Fatalf("清空后记录里不该还有语音，实际 %d 条", n)
	}
	for _, name := range []string{"audio-1.wav", "audio-2.wav"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("清空后 %s 应该被删掉（err=%v）", name, err)
		}
	}

	// 换一根继续录：清空只影响那一根
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	if got := audioStep(t, sendAudio(t, hub, a, instID, wav)); got != 4 {
		t.Fatalf("重排之后：起点 + 开仓 + 换根 + 语音 = 第 4 步，实际 %d", got)
	}
	if got := audioStep(t, sendAudio(t, hub, a, instID, wav)); got != 5 {
		t.Fatalf("第 2 根上的第 2 段语音应当是第 5 步，实际 %d", got)
	}

	// 收尾之后：audios 只剩后面那两条，被清掉的不复活
	marketOrder(t, hub, a, instID, "sell", 1)
	sum := a.waitFor(MsgTradeRecorded)["recording"].(map[string]any)
	recID := sum["id"].(string)
	a.send(hub, map[string]any{"type": "get_recording", "recordingId": recID})
	raw, _ := json.Marshal(a.waitFor(MsgRecording)["recording"])
	var rec Recording
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("记录反序列化失败：%v", err)
	}
	if len(rec.Audios) != 2 {
		t.Fatalf("记录里应当只剩后面那 2 条语音，实际 %d 条：%+v", len(rec.Audios), rec.Audios)
	}
	if len(rec.Steps) != 6 {
		t.Fatalf("记录应当是 6 步（起点/开仓/换根/语音/语音/平仓），实际 %d", len(rec.Steps))
	}
	for i, st := range rec.Steps {
		if st.Step != i+1 {
			t.Fatalf("清空重排之后步号必须连续：第 %d 个元素是 %d", i, st.Step)
		}
	}
	for _, au := range rec.Audios {
		if au.Step < 1 || au.Step > len(rec.Steps) {
			t.Fatalf("语音挂到了不存在的步上：%+v", au)
		}
		if st := rec.Steps[au.Step-1]; st.Audio == nil || st.Audio.ID != au.ID {
			t.Fatalf("第 %d 步上挂的语音和 audios 里那条对不上：%+v / %+v", au.Step, st.Audio, au)
		}
	}
	// 目录里也不该留下孤儿音频（audio-1/2 被删了，重录的两条是 audio-3/4）
	entries, err := os.ReadDir(filepath.Join(recDir, recordingFolder(recID)))
	if err != nil {
		t.Fatalf("读记录目录失败：%v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "audio-") && e.Name() != rec.Audios[0].File && e.Name() != rec.Audios[1].File {
			t.Fatalf("录完的目录里还有没人引用的音频：%s", e.Name())
		}
	}
}

// TestTradeRecordingRejectsOldFormat 钉住「旧记录不读」：v0.15.0 的 Steps 语义变了，
// 旧文件（format=1，按 K 线分步）如果被硬读出来，步进条会显示成一堆「一步蹦出一整根
// K 线变化」的怪物。宁可明确说「这条不兼容」，也不要让人以为回放坏了。
func TestTradeRecordingRejectsOldFormat(t *testing.T) {
	recDir := t.TempDir()
	// 造一条旧格式记录（没有 format 字段 = 1）
	old := filepath.Join(recDir, "rec-20260101-000000")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	body := `{"id":"rec-20260101-000000","symbol":"CME_MINI:MNQ1!","name":"旧记录",
		"base":{"cursor":0,"bars":[]},"steps":[{"step":1,"time":"2026-01-01 09:00","events":[]}]}`
	if err := os.WriteFile(filepath.Join(old, recordingFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("写旧格式记录失败：%v", err)
	}

	var logs []string
	st, err := OpenRecordingStore(recDir, func(f string, a ...any) {
		logs = append(logs, fmt.Sprintf(f, a...))
	})
	if err != nil {
		t.Fatalf("打开记录目录失败：%v", err)
	}
	if n := len(st.List()); n != 0 {
		t.Fatalf("旧格式记录不该出现在列表里，实际 %d 条", n)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "旧格式") {
		t.Fatalf("跳过旧格式记录时应当说一句，实际日志：%s", joined)
	}
	if _, err := st.Load("rec-20260101-000000"); err == nil || !strings.Contains(err.Error(), "旧格式") {
		t.Fatalf("读旧格式记录应当明确报「旧格式」，实际 %v", err)
	}
	// 旧目录**不自动删**：别人的数据，说一下就够
	if _, err := os.Stat(filepath.Join(old, recordingFileName)); err != nil {
		t.Fatalf("不该擅自删掉旧记录目录：%v", err)
	}
}

// ---------------------------------------------------------------------------
// 录制 / 重放的测试辅助（重放器是前端 instance-reducer.ts 的 Go 镜像）
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
// do* 调用的薄壳，这里验的是「do* -> 广播 -> 记录」这条链路。
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

// replayRecording 用记录的 Base + 每一步的事件重建一个实例状态。
//
// 它是前端 stores/instance-reducer.ts 在 Go 里的最小镜像：只实现「改盘面」的
// 那几个动作。存在的意义不是给生产代码用，而是**证明记录里的事件足够还原当时**：
// 少录一个字段、动作漏挂一个，这里就会和实例对不上。
func replayRecording(t *testing.T, rec *Recording) *Instance {
	t.Helper()
	return replaySteps(t, rec, len(rec.Steps))
}

// replaySteps 只演到第 upto 步（1 起）。
//
// 前端「跳到第 N 步」做的是同一件事：从 Base 重算，而不是在上一帧上做减法 ——
// 这样任意跳步都不会错位。这里顺带钉住每一步自称的 Cursor 和演出来的对得上。
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
		// 语音步不改盘面：步里没有事件，这条分支只是把「允许出现」说清楚
	default:
		t.Fatalf("记录里出现了不该被录的动作：%s", ev.Action)
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

// ---------------------------------------------------------------------------
// 开仓之前的语音（v0.26.1）
// ---------------------------------------------------------------------------

// TestTradeRecordingKeepsPreOpenVoice 钉住「开仓前录的讲解也要在记录里」。
//
// 用户报过的现象：复盘时在 B116 讲了一段（那时还没开仓），走到 B151 开仓、B158 平仓，
// 再打开这条记录 —— B116 在轴上、图形也在，**唯独那段讲解没了**。
//
// 根因不是数据丢了：语音归实例（voice.go），录的时候 inst.trade 还是 nil，只会写进
// 实例那一份；而记录的 Base 只抄了当时的图形和挂单。同一时刻盘面上的讲解没跟着进来，
// 回放就成了「看得见 K 线、看不见讲解」。这里验的是「开仓那一刻把它抄一份进来」。
func TestTradeRecordingKeepsPreOpenVoice(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	// 语音要落到状态目录里才有字节可抄（没配 state 目录时只在内存里）
	if err := hub.EnablePersistence(t.TempDir()); err != nil {
		t.Fatalf("启用状态目录失败：%v", err)
	}
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)

	// ---- 空仓时讲一段 ----
	preBar := inst.Bars[inst.Cursor].Time
	preWAV := testWAV(320)
	if got := audioStep(t, sendAudio(t, hub, a, instID, preWAV)); got != 0 {
		t.Fatalf("开仓前录的语音不属于任何一步（step=0），实际 %d", got)
	}
	// 再往前走一根：让「开仓那一刻」的 Base 里确实含着一根更早的 K 线，
	// 也就是用户说的 B116 —— 它不是开仓那根，但确实在快照里。
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	if n := inst.TradeRecordCount(); n != 0 {
		t.Fatalf("空仓时不该开记录，实际记了 %d 笔", n)
	}

	// ---- 开仓：Base 里应当带上那段语音 ----
	tradePrice(t, marketOrder(t, hub, a, instID, "buy", 1))
	rec := inst.trade.rec
	if len(rec.Audios) != 1 {
		t.Fatalf("开仓前那段语音应当在记录里，实际 %d 条：%+v", len(rec.Audios), rec.Audios)
	}
	pre := rec.Audios[0]
	if pre.Step != 0 {
		t.Fatalf("开仓前的语音不占步，Step 应当是 0，实际 %d", pre.Step)
	}
	if pre.Time != preBar {
		t.Fatalf("语音应当挂在那根 K 线 %s 上，实际 %s", preBar, pre.Time)
	}
	if pre.Size != int64(len(preWAV)) || pre.DurationMs != 1000 {
		t.Fatalf("抄进来的语音元数据不对：%+v", pre)
	}
	// 文件得真的在记录目录里（记录要能单独拷走，不能指到实例目录去）
	path := filepath.Join(recDir, recordingFolder(rec.ID), pre.File)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("开仓前的语音没有写进记录目录 %s：%v", path, err)
	}
	if len(got) != len(preWAV) {
		t.Fatalf("抄出来的音频长度不对：%d / %d", len(got), len(preWAV))
	}

	// ---- 持仓中再讲一段：编号接着往下走，不能和抄进来的那条重号 ----
	if got := audioStep(t, sendAudio(t, hub, a, instID, testWAV(160))); got != 3 {
		t.Fatalf("第 3 步应当是持仓中录的语音，实际 %d", got)
	}
	all := inst.trade.rec.Audios
	if len(all) != 2 {
		t.Fatalf("记录里应当有 2 条语音，实际 %d", len(all))
	}
	if all[1].File != "audio-2.wav" || all[1].ID != "au2" {
		t.Fatalf("持仓中那条应当接着编号成 au2 / audio-2.wav，实际 %+v", all[1])
	}

	// ---- 平仓落盘，读回来的还是这两条 ----
	tradePrice(t, marketOrder(t, hub, a, instID, "sell", 1))
	msg := a.waitFor(MsgTradeRecorded)
	sum, _ := msg["recording"].(map[string]any)
	id, _ := sum["id"].(string)
	if id == "" {
		t.Fatalf("收尾通知里没有记录 id：%v", msg)
	}
	loaded, err := hub.records.Load(id)
	if err != nil {
		t.Fatalf("读回记录失败：%v", err)
	}
	if len(loaded.Audios) != 2 {
		t.Fatalf("落盘再读回来应当还是 2 条语音，实际 %d", len(loaded.Audios))
	}
	if n, _ := sum["audioCount"].(float64); int(n) != 2 {
		t.Fatalf("列表摘要里的语音数应当是 2，实际 %v", sum["audioCount"])
	}
	if loaded.Audios[0].Step != 0 || loaded.Audios[0].Time != preBar {
		t.Fatalf("读回来的开仓前语音不对：%+v", loaded.Audios[0])
	}
}

// TestRecordingBackfillsPreOpenVoice 覆盖**已经录在盘上**的老记录。
//
// 修之前生成的记录里根本没有那条语音，文件却还在实例存档里
// （<stateDir>/instances/<id>/voice/voice-N.wav）。用户在记录页打开它时补一次：
// 只追加、不动任何一步，补完落盘 —— 再打开就不该重复补。
func TestRecordingBackfillsPreOpenVoice(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	stateDir := t.TempDir()
	if err := hub.EnablePersistence(stateDir); err != nil {
		t.Fatalf("启用状态目录失败：%v", err)
	}
	recDir := t.TempDir()
	if err := hub.EnableRecordings(recDir); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	inst := mustInstance(t, hub, instID)
	preBar := inst.Bars[inst.Cursor].Time
	sendAudio(t, hub, a, instID, testWAV(320))
	a.send(hub, map[string]any{"type": "step", "instanceId": instID})
	a.waitAction(ActBarAdvanced)
	tradePrice(t, marketOrder(t, hub, a, instID, "buy", 1))
	tradePrice(t, marketOrder(t, hub, a, instID, "sell", 1))
	msg := a.waitFor(MsgTradeRecorded)
	sum, _ := msg["recording"].(map[string]any)
	id, _ := sum["id"].(string)

	// 把这条记录退回到「修复前」的样子：recording.json 里抹掉开仓前那条，wav 也删掉
	path := filepath.Join(recDir, recordingFolder(id), recordingFileName)
	var rec Recording
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读不到记录文件 %s：%v", path, err)
	}
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("记录文件不是合法 JSON：%v", err)
	}
	kept := rec.Audios[:0]
	for _, c := range rec.Audios {
		if c.Step == 0 {
			_ = os.Remove(filepath.Join(recDir, recordingFolder(id), c.File))
			continue
		}
		kept = append(kept, c)
	}
	rec.Audios = kept
	rec.AudioCount = len(kept)
	if err := writeJSONAtomic(path, &rec); err != nil {
		t.Fatalf("写回记录文件失败：%v", err)
	}

	// 打开它：应当从实例存档把开仓前那条补回来
	a.send(hub, map[string]any{"type": "get_recording", "recordingId": id})
	got, ok := a.waitFor(MsgRecording)["recording"].(map[string]any)
	if !ok {
		t.Fatal("回放消息里没有记录")
	}
	audios, _ := got["audios"].([]any)
	if len(audios) != 1 {
		t.Fatalf("补齐之后应当有 1 条语音，实际 %d：%v", len(audios), audios)
	}
	back, _ := audios[0].(map[string]any)
	if back["time"] != preBar || numOf(back, "step") != 0 {
		t.Fatalf("补回来的那条应当挂回 %s 且不占步，实际 %v", preBar, back)
	}
	name, _ := back["file"].(string)
	if _, err := os.Stat(filepath.Join(recDir, recordingFolder(id), name)); err != nil {
		t.Fatalf("补回来的音频没有写进记录目录：%v", err)
	}

	// 再打开一次：已经在记录里了，不该再补一条（否则每开一次多一条）
	a.send(hub, map[string]any{"type": "get_recording", "recordingId": id})
	again, _ := a.waitFor(MsgRecording)["recording"].(map[string]any)
	if list, _ := again["audios"].([]any); len(list) != 1 {
		t.Fatalf("补齐应当只做一次，第二次打开却变成 %d 条", len(list))
	}
}
