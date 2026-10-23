package replay

// 实例持久化的测试：**真的写文件、真的换一个 Hub 重新读回来**。
//
// 这类测试最容易写成「把结构体存进去又取出来，断言相等」—— 那只是在测 encoding/json。
// 这里刻意走完整链路：建实例 → 步进 → 画标注 → 挂单 → 落盘 → 新建 Hub 恢复，
// 断言的是「用户重启后看到的东西」：游标、标注、挂单、操作权、实例编号、日文件定位。

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// newPersistentHub 起一个带临时的数据目录 + 状态目录 + 偏好目录的 Hub。
func newPersistentHub(t *testing.T, root, stateDir string) *Hub {
	t.Helper()
	return newPersistentHubWith(t, root, stateDir, t.TempDir())
}

// newPersistentHubWith 和上面一样，但偏好目录由调用方指定 ——
// 需要「换一个 Hub 读回同一份模板」的用例传两次同一个目录。
func newPersistentHubWith(t *testing.T, root, stateDir, presetDir string) *Hub {
	t.Helper()
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	if err := hub.EnablePersistence(stateDir); err != nil {
		t.Fatalf("启用持久化失败：%v", err)
	}
	if err := hub.EnablePresets(presetDir); err != nil {
		t.Fatalf("启用偏好目录失败：%v", err)
	}
	return hub
}

func TestInstancesSurviveRestart(t *testing.T) {
	root := writeTestData(t, 5)
	stateDir := t.TempDir()

	// ---- 第一次运行：建实例、步进、标注、挂单 ----
	hub := newPersistentHub(t, root, stateDir)
	a := newFakeClient(t, hub)

	a.send(hub, map[string]any{
		"type":   "create_instance",
		"symbol": testSymbol,
		"time":   "2026-01-06 10:00",
		"bars":   40,
	})
	st := a.waitFor(MsgInstanceState)
	detail := st["instance"].(map[string]any)
	id := detail["id"].(string)
	bars := detail["bars"].([]any)
	cursorBefore := int(detail["cursor"].(float64))
	anchorBar := bars[len(bars)-1].(map[string]any)
	barTime := anchorBar["time"].(string)

	for i := 0; i < 2; i++ {
		a.send(hub, map[string]any{"type": "step", "instanceId": id})
		a.waitAction(ActBarAdvanced)
	}
	a.send(hub, map[string]any{
		"type":       "annotation_add",
		"instanceId": id,
		"kind":       "rect",
		"start":      map[string]any{"time": barTime, "price": 101},
		"end":        map[string]any{"time": barTime, "price": 103},
		"snap":       false,
	})
	a.waitAction(ActAnnotationAdded)
	a.send(hub, map[string]any{
		"type":       "order_place",
		"instanceId": id,
		"side":       "buy",
		"kind":       "limit",
		"price":      1,
		"quantity":   2,
	})
	a.waitAction(ActOrderPlaced)

	hub.flushState() // 不等到 2 秒的定时器
	if _, err := os.Stat(filepath.Join(stateDir, stateFileName)); err != nil {
		t.Fatalf("状态索引没有写出来：%v", err)
	}
	// v2 起按实例分文件夹：条条数据在自己的文件里，索引只记 id
	for _, name := range []string{metaFileName, barsFileName, annFileName, orderFileName, tradeFileName, eventFileName} {
		p := filepath.Join(stateDir, instanceDirName, id, name)
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("实例的 %s 没有写出来：%v", name, err)
		}
	}
	hub.Close()

	// ---- 第二次运行：换一个 Hub，指向同一个状态目录 ----
	hub2 := newPersistentHub(t, root, stateDir)
	b := newFakeClient(t, hub2)
	b.send(hub2, map[string]any{"type": "list_instances"})
	list := b.waitFor(MsgInstances)["instances"].([]any)
	if len(list) != 1 {
		t.Fatalf("恢复后应当只有 1 个实例，实际 %d 个", len(list))
	}

	b.send(hub2, map[string]any{"type": "join_instance", "instanceId": id})
	st2 := b.waitFor(MsgInstanceState)
	if you := st2["you"].(map[string]any); you["operator"] != true {
		t.Fatal("恢复后操作权应当空着，谁进来谁就能接管")
	}
	d2 := st2["instance"].(map[string]any)
	if got := int(d2["cursor"].(float64)); got != cursorBefore+2 {
		t.Fatalf("游标没有恢复：期望 %d，实际 %d", cursorBefore+2, got)
	}
	if n := len(d2["bars"].([]any)); n != cursorBefore+3 {
		t.Fatalf("已揭示的 K 线数量不对：%d", n)
	}
	anns := d2["annotations"].([]any)
	if len(anns) != 1 {
		t.Fatalf("标注没有恢复，实际 %d 个", len(anns))
	}
	if ann := anns[0].(map[string]any); ann["id"] != "a1" || ann["kind"] != "rect" {
		t.Fatalf("标注内容不对：%v", ann)
	}
	orders := d2["orders"].([]any)
	if len(orders) != 1 || orders[0].(map[string]any)["status"] != OrderOpen {
		t.Fatalf("挂单没有恢复：%v", orders)
	}
	// 编号自增不能被重置，否则新标注会和旧的撞号
	if detail2 := d2["events"].([]any); len(detail2) == 0 {
		t.Fatal("操作记录没有恢复")
	}

	// ---- 恢复后还能继续步进（日文件是重新索引出来的，不是保存下来的下标）----
	b.drain()
	b.send(hub2, map[string]any{"type": "step", "instanceId": id})
	ev := b.waitAction(ActBarAdvanced)
	p := ev["payload"].(map[string]any)
	if got := int(p["cursor"].(float64)); got != cursorBefore+3 {
		t.Fatalf("恢复后步进没有前进：游标 %d", got)
	}

	// 编号接着往下走：新实例不能叫 ins-1
	b.drain()
	b.send(hub2, map[string]any{
		"type":   "create_instance",
		"symbol": testSymbol,
		"time":   "2026-01-07 10:00",
		"bars":   20,
	})
	st3 := b.waitFor(MsgInstanceState)
	if got := st3["instance"].(map[string]any)["id"]; got == id {
		t.Fatalf("新实例复用了旧编号 %v", got)
	}
	hub2.Close()
}

func TestDeletedInstanceStaysDeleted(t *testing.T) {
	root := writeTestData(t, 3)
	stateDir := t.TempDir()

	hub := newPersistentHub(t, root, stateDir)
	a := newFakeClient(t, hub)
	id := createInstance(t, hub, a, "2026-01-06 10:00")

	a.send(hub, map[string]any{"type": "delete_instance", "instanceId": id})
	a.waitFor(MsgDeleted)
	hub.flushState()
	hub.Close()

	hub2 := newPersistentHub(t, root, stateDir)
	b := newFakeClient(t, hub2)
	b.send(hub2, map[string]any{"type": "list_instances"})
	if list := b.waitFor(MsgInstances)["instances"].([]any); len(list) != 0 {
		t.Fatalf("删掉的实例又回来了：%v", list)
	}
	hub2.Close()
}

// 0.5.0 存下来的实例，Bars 在「锚点日最后一根 -> 第二天第一根」之间缺了一整段。
// 恢复时必须按日文件补齐（改加载逻辑救不了已经存下来的实例），而且：
//   - 游标还停在用户原来那根上；
//   - 挂单的 PlacedIndex 跟着重算（补齐后下标全变了）；
//   - 补回来的第一根就是锚点当天锚点的下一根，不是第二天的 00:00。
func TestRestoreRepairsMissingAnchorDayTail(t *testing.T) {
	root := writeTestData(t, 4) // 每天 09:00 ~ 11:00，25 根
	stateDir := t.TempDir()

	// 1. 用当前（已修好）的代码建一个实例，拿到一份完整的存档
	hub := newPersistentHub(t, root, stateDir)
	a := newFakeClient(t, hub)
	id := createInstance(t, hub, a, "2026-01-06 10:00")
	hub.flushState()
	hub.Close()

	// 2. 把存档改回 0.5.0 的样子：删掉「锚点当天锚点之后的那些根」
	dir := filepath.Join(stateDir, instanceDirName, id)
	meta := readMetaFile(t, dir)
	bars := readBarsFile(t, dir)

	anchorIdx := -1
	for i := range bars {
		if bars[i].Time == meta.Anchor {
			anchorIdx = i
			break
		}
	}
	if anchorIdx < 0 {
		t.Fatalf("存档里找不到锚点 %s", meta.Anchor)
	}
	cut := anchorIdx + 1
	for cut < len(bars) && barDate(bars[cut].Time) == barDate(meta.Anchor) {
		cut++
	}
	tail := cut - (anchorIdx + 1)
	if tail == 0 {
		t.Fatal("造数据失败：锚点之后当天还有 K 线，才能模拟这个缺口")
	}
	bars = append(bars[:anchorIdx+1:anchorIdx+1], bars[cut:]...)

	// 模拟「老版本里已经往后翻了几根」：游标在新位置，挂单也记着当时（错位）的下标
	meta.Cursor = anchorIdx + 3
	jumped := bars[meta.Cursor].Time
	orders := readOrdersFile(t, dir)
	orders = append(orders, Order{
		ID: "o1", Side: "buy", Kind: KindLimit, Price: 100, Quantity: 1,
		Status: OrderOpen, PlacedAt: jumped, PlacedIndex: meta.Cursor, FilledIndex: -1,
	})
	writeBarsFile(t, dir, bars)
	writeOrdersFile(t, dir, orders)
	writeMetaFile(t, dir, meta)
	barsBefore := len(bars)

	// 3. 重启：缺口应当被补齐
	hub2 := newPersistentHub(t, root, stateDir)
	defer hub2.Close()
	hub2.mu.Lock()
	inst := hub2.instances[id]
	hub2.mu.Unlock()
	if inst == nil {
		t.Fatal("实例没有被恢复")
	}
	if len(inst.Bars) != barsBefore+tail {
		t.Fatalf("缺口没补齐：补前 %d 根，补后 %d 根（应当多 %d 根）", barsBefore, len(inst.Bars), tail)
	}
	if got := inst.Bars[inst.Cursor].Time; got != jumped {
		t.Fatalf("游标应当还停在 %s，实际 %s", jumped, got)
	}
	want, ok := expectedNextBar(t, root, testFolder, meta.Anchor)
	if !ok {
		t.Fatal("锚点之后应当还有 K 线")
	}
	if got := inst.Bars[anchorIdx+1].Time; got != want {
		t.Fatalf("锚点的下一根应当是 %s，实际 %s", want, got)
	}
	for i := 1; i < len(inst.Bars); i++ {
		if inst.Bars[i-1].Time >= inst.Bars[i].Time {
			t.Fatalf("补齐后不是升序：%s >= %s", inst.Bars[i-1].Time, inst.Bars[i].Time)
		}
	}
	if len(inst.Orders) != 1 {
		t.Fatalf("挂单应当还在，实际 %d 张", len(inst.Orders))
	}
	if o := inst.Orders[0]; inst.Bars[o.PlacedIndex].Time != o.PlacedAt {
		t.Fatalf("挂单下标没有跟着重算：PlacedIndex=%d 指向 %s，挂单时间是 %s",
			o.PlacedIndex, inst.Bars[o.PlacedIndex].Time, o.PlacedAt)
	}
	repaired := false
	for _, n := range inst.notes {
		if strings.Contains(n, "补上了") {
			repaired = true
		}
	}
	if !repaired {
		t.Fatalf("补了缺口却没在 notes 里说清楚：%v", inst.notes)
	}
}

// 健康的存档（0.5.1 之后建的）恢复时必须原封不动 —— 修复逻辑不能每次都重读日文件。
func TestRestoreLeavesHealthyArchiveAlone(t *testing.T) {
	root := writeTestData(t, 3)
	stateDir := t.TempDir()

	hub := newPersistentHub(t, root, stateDir)
	a := newFakeClient(t, hub)
	id := createInstance(t, hub, a, "2026-01-06 10:00")
	hub.flushState()
	hub.Close()

	hub2 := newPersistentHub(t, root, stateDir)
	defer hub2.Close()
	hub2.mu.Lock()
	inst := hub2.instances[id]
	hub2.mu.Unlock()
	if inst == nil {
		t.Fatal("实例没有被恢复")
	}
	for _, n := range inst.notes {
		if strings.Contains(n, "补上了") {
			t.Fatalf("健康的存档不该被改动：%v", inst.notes)
		}
	}
	// 紧邻的下一根本来就是对的，不该被动过
	if got := inst.Bars[inst.Cursor+1].Time; got != "2026-01-06 10:05" {
		t.Fatalf("锚点的下一根应当是 2026-01-06 10:05，实际 %s", got)
	}
}

func TestBrokenStateFileDoesNotCrash(t *testing.T) {
	root := writeTestData(t, 2)
	stateDir := t.TempDir()
	path := filepath.Join(stateDir, stateFileName)
	if err := os.WriteFile(path, []byte("{这不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}

	hub := newPersistentHub(t, root, stateDir) // 不该 panic，只是从空列表开始
	a := newFakeClient(t, hub)
	a.send(hub, map[string]any{"type": "list_instances"})
	if list := a.waitFor(MsgInstances)["instances"].([]any); len(list) != 0 {
		t.Fatalf("坏文件不该读出实例：%v", list)
	}
	// 坏文件必须原样留着：用户可能还想手工抢救
	raw, err := os.ReadFile(path)
	if err != nil || string(raw) != "{这不是 JSON" {
		t.Fatalf("坏的状态文件被改动了：%v %q", err, raw)
	}
	hub.Close()
}

// ---------------- 新存档布局（按实例分文件夹）的读写小工具 ----------------
//
// 测试刻意不用 writeInstanceFiles 造数据：那等于拿被测代码去准备被测代码。
// 这几个函数只做「读一个文件 / 写一个文件」，坏数据才造得出来。

func readMetaFile(t *testing.T, dir string) instanceMeta {
	t.Helper()
	var m instanceMeta
	if err := readJSONFile(filepath.Join(dir, metaFileName), &m); err != nil {
		t.Fatalf("读 meta.json 失败：%v", err)
	}
	return m
}

func writeMetaFile(t *testing.T, dir string, m instanceMeta) {
	t.Helper()
	if err := writeJSONAtomic(filepath.Join(dir, metaFileName), m); err != nil {
		t.Fatalf("写 meta.json 失败：%v", err)
	}
}

func readBarsFile(t *testing.T, dir string) []Bar {
	t.Helper()
	var bars []Bar
	if err := readJSONFile(filepath.Join(dir, barsFileName), &bars); err != nil {
		t.Fatalf("读 bars.json 失败：%v", err)
	}
	return bars
}

func writeBarsFile(t *testing.T, dir string, bars []Bar) {
	t.Helper()
	if err := writeJSONAtomic(filepath.Join(dir, barsFileName), bars); err != nil {
		t.Fatalf("写 bars.json 失败：%v", err)
	}
}

func readOrdersFile(t *testing.T, dir string) []Order {
	t.Helper()
	var orders []Order
	if err := readJSONFile(filepath.Join(dir, orderFileName), &orders); err != nil && !os.IsNotExist(err) {
		t.Fatalf("读 orders.json 失败：%v", err)
	}
	return orders
}

func writeOrdersFile(t *testing.T, dir string, orders []Order) {
	t.Helper()
	if err := writeJSONAtomic(filepath.Join(dir, orderFileName), orders); err != nil {
		t.Fatalf("写 orders.json 失败：%v", err)
	}
}

// TestLegacySingleFileArchiveMigrates 钉住 v1 -> v2 的迁移：
// 旧版把所有实例塞在一个 replay-instances.json 里，新版按实例分文件夹。
// 迁移必须**先备份旧文件**，再把数据摊到新布局，而且恢复出来的实例一条不能少。
func TestLegacySingleFileArchiveMigrates(t *testing.T) {
	root := writeTestData(t, 3)
	stateDir := t.TempDir()

	// 1. 先用当前代码造一个实例，拿到一份完整数据
	hub := newPersistentHub(t, root, stateDir)
	a := newFakeClient(t, hub)
	id := createInstance(t, hub, a, "2026-01-06 10:00")
	hub.flushState()
	hub.Close()

	dir := filepath.Join(stateDir, instanceDirName, id)
	m := readMetaFile(t, dir)
	bars := readBarsFile(t, dir)
	var anns []Annotation
	var trades []Trade
	var events []Event
	if err := readJSONFile(filepath.Join(dir, annFileName), &anns); err != nil {
		t.Fatalf("读 annotations.json 失败：%v", err)
	}
	if err := readJSONFile(filepath.Join(dir, tradeFileName), &trades); err != nil {
		t.Fatalf("读 trades.json 失败：%v", err)
	}
	if err := readJSONFile(filepath.Join(dir, eventFileName), &events); err != nil {
		t.Fatalf("读 events.json 失败：%v", err)
	}

	// 2. 把新布局整个抹掉，按 v1 的样子写回一个单文件
	//
	// 顺带造一笔「点数」口径的盈亏：v1 没有 tickValue，realizedPnl 是「点数 × 手数」，
	// 迁移后应当按 MNQ 的 2 美元/点换成美元（2.5 -> 5）。
	trades = []Trade{
		{ID: "t1", OrderID: "o1", Time: bars[m.Cursor].Time, Side: "buy", Kind: KindLimit, Price: 100, Quantity: 1},
		{ID: "t2", OrderID: "o2", Time: bars[m.Cursor].Time, Side: "sell", Kind: KindLimit, Price: 102.5, Quantity: 1, RealizedPnl: 2.5},
	}
	legacy := legacyStateFile{
		Version:      1,
		SavedAt:      time.Now().Unix(),
		NextInstance: 1,
		Instances: []legacyInstance{{
			ID: m.ID, Symbol: m.Symbol, Folder: m.Folder, Tick: m.Tick, Anchor: m.Anchor,
			Cursor: m.Cursor, Bars: bars,
			Annotations: anns, Orders: readOrdersFile(t, dir), Trades: trades,
			Position: m.Position, RealizedPnl: 2.5,
			CreatedBy: m.CreatedBy, CreatedAt: m.CreatedAt,
			Seq: m.Seq, Events: events, Notes: m.Notes,
			SeqAnnotation: m.SeqAnn, SeqOrder: m.SeqOrd, SeqTrade: m.SeqTrd,
		}},
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatalf("序列化 v1 存档失败：%v", err)
	}
	if err := os.RemoveAll(filepath.Join(stateDir, instanceDirName)); err != nil {
		t.Fatalf("清理新布局失败：%v", err)
	}
	path := filepath.Join(stateDir, stateFileName)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("写 v1 存档失败：%v", err)
	}

	// 3. 重启：应当在读的过程中迁到新布局，并且实例还在
	hub2 := newPersistentHub(t, root, stateDir)
	defer hub2.Close()
	hub2.mu.Lock()
	inst := hub2.instances[id]
	hub2.mu.Unlock()
	if inst == nil {
		t.Fatal("v1 存档里的实例没有被恢复")
	}
	if inst.Cursor != m.Cursor || len(inst.Bars) != len(bars) {
		t.Fatalf("迁移后游标/K 线不对：游标 %d（应当 %d）、K 线 %d 根（应当 %d）",
			inst.Cursor, m.Cursor, len(inst.Bars), len(bars))
	}
	// tickValue 不在 v1 存档里，要从 _symbols.json 补上（测试数据里是 MNQ 口径）
	if inst.TickValue != 0.5 {
		t.Fatalf("迁移后应当按 _symbols.json 补上 tickValue=0.5，实际 %v", inst.TickValue)
	}
	// v1 的盈亏是点数口径：2.5 点 × 2 美元/点 = 5 美元
	if math.Abs(inst.RealizedPnl-5) > 1e-9 {
		t.Fatalf("迁移后累计盈亏应当换成美元（2.5 -> 5），实际 %v", inst.RealizedPnl)
	}
	if len(inst.Trades) != 2 || math.Abs(inst.Trades[1].RealizedPnl-5) > 1e-9 {
		t.Fatalf("成交流水里的盈亏也要换成美元：%+v", inst.Trades)
	}

	// 4. 新布局已经落盘，索引是 v2（只记 id，没有 K 线/成交）
	for _, name := range []string{metaFileName, barsFileName, annFileName, orderFileName, tradeFileName, eventFileName} {
		if _, err := os.Stat(filepath.Join(stateDir, instanceDirName, id, name)); err != nil {
			t.Fatalf("迁移后缺少 %s：%v", name, err)
		}
	}
	var idx stateFile
	if err := readJSONFile(path, &idx); err != nil {
		t.Fatalf("迁移后的索引读不了：%v", err)
	}
	if idx.Version != stateVersion || len(idx.Instances) != 1 || idx.Instances[0].ID != id {
		t.Fatalf("迁移后的索引不对：%+v", idx)
	}

	// 5. 旧文件原样备份着（迁移出问题时还能拿回去用旧版本打开）
	bak := path + legacyBackupSuffix
	bakRaw, err := os.ReadFile(bak)
	if err != nil {
		t.Fatalf("迁移前的备份不见了：%v", err)
	}
	var old legacyStateFile
	if err := json.Unmarshal(bakRaw, &old); err != nil || old.Version != 1 || len(old.Instances) != 1 {
		t.Fatalf("备份文件不对：err=%v version=%d 实例 %d 个", err, old.Version, len(old.Instances))
	}
}
