package replay

// 交易记录 v0.21.0 的**两个新维度**：操作来源（真人 / 机器人·策略）与自定义标签。
//
// 这两件事都不改「一笔交易是什么」——记录本体、统计口径、回放链路一个字没动。
// 它们只是给列表页的日历 / 统计 / 筛选提供两个**可持久化**的维度，所以测试的重点
// 也放在「存得住、读得回、清得掉」上，而不是界面：
//
//  1. **来源在开仓那一刻就定下来**（TestRecordingSource）：机器人下的单，回放里
//     每一步的 by 都是 bot，记录上的来源也必须是 bot；旧记录没有这个字段时
//     按 CreatedBy 反推（bot / 真人），不能让升级后的列表里出现「来源未知」。
//  2. **标签的清洗规则只有一份**（TestRecordingTagsPersist）：去空白、同名后者赢、
//     限长、颜色必须是 #rrggbb。标签写进 tags.json，**重开一次记录目录还在**；
//     清空之后也必须还是空的 —— 这一条最容易坏：recording.json 里那份旧的 Tags
//     会在扫描时把「已清空」悄悄还原回去。

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// openAndCloseTrade 做一整笔（市价开 → 市价平），返回收尾通知里的那行摘要。
// 记录在平仓那一刻自动收尾、写盘，所以返回时它已经在列表里了。
func openAndCloseTrade(t *testing.T, hub *Hub, fc *fakeClient, instID string, qty float64) map[string]any {
	t.Helper()
	marketOrder(t, hub, fc, instID, "buy", qty)
	marketOrder(t, hub, fc, instID, "sell", qty)
	msg := fc.waitFor(MsgTradeRecorded)
	sum, ok := msg["recording"].(map[string]any)
	if !ok {
		t.Fatalf("收尾通知里没有记录摘要：%v", msg)
	}
	return sum
}

// recordingFromList 取当前列表里 id 对应的那一行。
func recordingFromList(t *testing.T, hub *Hub, fc *fakeClient, id string) map[string]any {
	t.Helper()
	fc.send(hub, map[string]any{"type": "list_recordings"})
	list := fc.waitFor(MsgRecordings)
	items, _ := list["recordings"].([]any)
	for _, it := range items {
		row, _ := it.(map[string]any)
		if row["id"] == id {
			return row
		}
	}
	t.Fatalf("列表里没有这条记录：%s", id)
	return nil
}

// tagPairs 把摘要里的 tags 压成 `名字#颜色` 的列表，断言起来一眼能读完。
func tagPairs(t *testing.T, sum map[string]any) []string {
	t.Helper()
	raw, _ := sum["tags"].([]any)
	out := make([]string, 0, len(raw))
	for _, it := range raw {
		tag, ok := it.(map[string]any)
		if !ok {
			t.Fatalf("标签不是对象：%v", it)
		}
		name, _ := tag["name"].(string)
		color, _ := tag["color"].(string)
		out = append(out, name+" "+color)
	}
	return out
}

func TestRecordingTagsPersist(t *testing.T) {
	hub, _ := newTestHub(t, 2)
	dir := t.TempDir()
	if err := hub.EnableRecordings(dir); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	sum := openAndCloseTrade(t, hub, a, instID, 1)
	recID, _ := sum["id"].(string)
	if recID == "" {
		t.Fatalf("记录没有 id：%v", sum)
	}
	if got := tagPairs(t, sum); len(got) != 0 {
		t.Fatalf("新记录不该自带标签，实际 %v", got)
	}

	// ---- 打标签：清洗规则一次性验完 ----
	//
	// 发过去的这份里有四种「脏」：带空白的名字、同名不同色、超长名字、坏颜色。
	// 服务端的处理是：trim、后者覆盖前者、按字符截到 12、落到中性灰。
	a.send(hub, map[string]any{
		"type":        "set_recording_tags",
		"recordingId": recID,
		"tags": []map[string]any{
			{"name": "  开盘冲动 ", "color": "#EF4444"},
			{"name": "早进", "color": "#0ea5e9"},
			{"name": "开盘冲动", "color": "#22c55e"}, // 同名：覆盖颜色，不新增一条
			{"name": "这是一个特别特别特别长的标签名字", "color": "#f59e0b"},
			{"name": "坏颜色", "color": "red"},
		},
	})
	list := a.waitFor(MsgRecordings)
	items, _ := list["recordings"].([]any)
	if len(items) != 1 {
		t.Fatalf("应当只有 1 条记录，实际 %d 条", len(items))
	}
	row, _ := items[0].(map[string]any)
	got := tagPairs(t, row)
	want := []string{
		"开盘冲动 #22c55e", // 名字去空白、颜色被后一个同名的覆盖，位置不变
		"早进 #0ea5e9",
		"这是一个特别特别特别长的 #f59e0b", // 截到 12 个字符（多出来的「标」被切掉）
		"坏颜色 #64748b",              // 不认的颜色落回中性灰
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("标签清洗结果不对：\n实际 %v\n期望 %v", got, want)
	}

	// ---- 落盘：tags.json 在记录目录里，且和 recording.json 分开 ----
	folder := filepath.Join(dir, recordingFolder(recID))
	if _, err := os.Stat(filepath.Join(folder, "tags.json")); err != nil {
		t.Fatalf("标签没有落盘：%v", err)
	}

	// ---- 重开一次记录目录：标签从 tags.json 读回来（不是只在内存索引里） ----
	reopened, err := OpenRecordingStore(dir, nil)
	if err != nil {
		t.Fatalf("重开记录目录失败：%v", err)
	}
	saved := reopened.List()
	if len(saved) != 1 {
		t.Fatalf("重开后应当有 1 条记录，实际 %d 条", len(saved))
	}
	if len(saved[0].Tags) != len(want) {
		t.Fatalf("重开后标签丢了：%+v", saved[0].Tags)
	}
	for i, tag := range saved[0].Tags {
		if tag.Name+" "+tag.Color != want[i] {
			t.Fatalf("第 %d 个标签应当是 %s，实际 %s %s", i+1, want[i], tag.Name, tag.Color)
		}
	}

	// ---- 数量上限：超出 maxTagsPerRecord 的部分丢掉，但已有的不动 ----
	many := make([]map[string]any, 0, maxTagsPerRecord+3)
	for _, n := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k"} {
		many = append(many, map[string]any{"name": n, "color": "#111827"})
	}
	a.send(hub, map[string]any{"type": "set_recording_tags", "recordingId": recID, "tags": many})
	list = a.waitFor(MsgRecordings)
	row, _ = list["recordings"].([]any)[0].(map[string]any)
	if n := len(tagPairs(t, row)); n != maxTagsPerRecord {
		t.Fatalf("一笔交易最多 %d 个标签，实际留下 %d 个", maxTagsPerRecord, n)
	}

	// ---- 清空：空数组 = 全部删掉，而且**扫描时不能被 recording.json 还原** ----
	a.send(hub, map[string]any{"type": "set_recording_tags", "recordingId": recID, "tags": []any{}})
	list = a.waitFor(MsgRecordings)
	row, _ = list["recordings"].([]any)[0].(map[string]any)
	if got := tagPairs(t, row); len(got) != 0 {
		t.Fatalf("清空之后列表里不该还有标签，实际 %v", got)
	}
	reopened2, err := OpenRecordingStore(dir, nil)
	if err != nil {
		t.Fatalf("重开记录目录失败：%v", err)
	}
	if n := len(reopened2.List()[0].Tags); n != 0 {
		t.Fatalf("清空之后重开目录又冒出 %d 个标签（recording.json 里那份旧值被当成事实了）", n)
	}

	// ---- 不存在的记录要明确报错，而不是静默成功 ----
	a.send(hub, map[string]any{
		"type": "set_recording_tags", "recordingId": "rec-不存在",
		"tags": []map[string]any{{"name": "x", "color": "#000000"}},
	})
	if msg := a.waitFor(MsgError); msg == nil {
		t.Fatal("给不存在的记录打标签应当回一条错误")
	}
}

func TestRecordingSource(t *testing.T) {
	// ---- 真人：来源就是操作者的客户端名字 ----
	hub, _ := newTestHub(t, 2)
	dir := t.TempDir()
	if err := hub.EnableRecordings(dir); err != nil {
		t.Fatalf("启用交易记录目录失败：%v", err)
	}
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	instID := createInstance(t, hub, a, "2026-01-05 10:00")
	sum := openAndCloseTrade(t, hub, a, instID, 1)
	if sum["source"] != SourceHuman {
		t.Fatalf("真人下的单来源应当是 %q，实际 %v", SourceHuman, sum["source"])
	}
	if sum["sourceName"] != a.conn.Name {
		t.Fatalf("真人的来源名应当是客户端名字 %q，实际 %v", a.conn.Name, sum["sourceName"])
	}
	if _, has := sum["sourceId"]; has {
		t.Fatalf("真人的记录不该带策略 id：%v", sum["sourceId"])
	}

	// ---- 机器人：by=bot 的那一手开出来的仓，整笔都算机器人 ----
	//
	// 走的是 bot_strategy 的真实路径（doMarketOrderLocked + by=bot），
	// 只是把「策略什么时候开仓」换成了测试自己决定。
	inst2ID := createInstance(t, hub, a, "2026-01-06 10:00")
	hub.mu.Lock()
	inst2 := hub.instances[inst2ID]
	inst2.botStrategy = "caveman"
	_, _, err := hub.doMarketOrderLocked(inst2, SourceBot, "buy", 1)
	if err == nil {
		_, _, err = hub.doMarketOrderLocked(inst2, SourceBot, "sell", 1)
	}
	saves := hub.pendingSaves
	hub.pendingSaves = nil
	hub.mu.Unlock()
	if err != nil {
		t.Fatalf("机器人下单失败：%v", err)
	}
	for _, s := range saves {
		hub.saveTrade(s)
	}
	botRow := recordingFromList(t, hub, a, func() string {
		for _, it := range hub.Records().List() {
			if it.Source == SourceBot {
				return it.ID
			}
		}
		t.Fatal("机器人开的仓没有留下交易记录")
		return ""
	}())
	if botRow["source"] != SourceBot {
		t.Fatalf("机器人开的仓来源应当是 %q，实际 %v", SourceBot, botRow["source"])
	}
	if botRow["sourceId"] != "caveman" {
		t.Fatalf("来源细分应当是策略 id caveman，实际 %v", botRow["sourceId"])
	}
	if name, _ := botRow["sourceName"].(string); name == "" || name == "caveman" {
		t.Fatalf("来源名应当取注册表里的策略名，实际 %q", name)
	}

	// ---- 随机演示机器人：策略 id 为空，名字固定是「随机演示」 ----
	inst2.botStrategy = ""
	src, id, name := tradeSource(inst2, SourceBot)
	if src != SourceBot || id != SourceDemoBot || name != SourceDemoName {
		t.Fatalf("随机演示机器人的来源应当是 %q/%q/%q，实际 %q/%q/%q",
			SourceBot, SourceDemoBot, SourceDemoName, src, id, name)
	}

	// ---- 旧记录（v0.20.0 及以前）没有来源字段：按 CreatedBy 反推 ----
	old := RecordingSummary{CreatedBy: "bot"}
	old.normalizeSource()
	if old.Source != SourceBot || old.SourceName != SourceDemoName || old.SourceID != SourceDemoBot {
		t.Fatalf("旧记录 CreatedBy=bot 应当反推成随机演示机器人，实际 %+v", old)
	}
	oldHuman := RecordingSummary{CreatedBy: "human2"}
	oldHuman.normalizeSource()
	if oldHuman.Source != SourceHuman || oldHuman.SourceName != "human2" {
		t.Fatalf("旧记录 CreatedBy=human2 应当反推成真人，实际 %+v", oldHuman)
	}
}
