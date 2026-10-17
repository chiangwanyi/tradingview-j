package replay

// 实例持久化的测试：**真的写文件、真的换一个 Hub 重新读回来**。
//
// 这类测试最容易写成「把结构体存进去又取出来，断言相等」—— 那只是在测 encoding/json。
// 这里刻意走完整链路：建实例 → 步进 → 画标注 → 挂单 → 落盘 → 新建 Hub 恢复，
// 断言的是「用户重启后看到的东西」：游标、标注、挂单、操作权、实例编号、日文件定位。

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// newPersistentHub 起一个带临时的数据目录 + 状态目录的 Hub。
func newPersistentHub(t *testing.T, root, stateDir string) *Hub {
	t.Helper()
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	if err := hub.EnablePersistence(stateDir); err != nil {
		t.Fatalf("启用持久化失败：%v", err)
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
		t.Fatalf("状态文件没有写出来：%v", err)
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
