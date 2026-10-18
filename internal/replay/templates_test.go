package replay

// 标注模板的测试：出厂预设、增删改、单人改完所有人都收到、重启后还在。
//
// 模板是全局的（不属于实例），所以这里不建实例，直接对着 Hub 发消息 ——
// 顺便钉住「模板消息不需要操作权」这件事。

import (
	"os"
	"path/filepath"
	"testing"
)

func templatesOf(t *testing.T, fc *fakeClient, hub *Hub) []any {
	t.Helper()
	fc.send(hub, map[string]any{"type": "list_templates"})
	m := fc.waitFor(MsgTemplates)
	list, _ := m["templates"].([]any)
	if list == nil {
		t.Fatalf("templates 消息里没有列表：%v", m)
	}
	return list
}

func TestDefaultTemplatesCoverAllTools(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)

	list := templatesOf(t, a, hub)
	seen := map[string]int{}
	for _, item := range list {
		tpl := item.(map[string]any)
		kind := tpl["kind"].(string)
		seen[kind]++
		style := tpl["style"].(map[string]any)
		if style["color"] == "" || style["color"] == nil {
			t.Fatalf("%s 的模板没有颜色：%v", kind, style)
		}
		if style["width"].(float64) <= 0 {
			t.Fatalf("%s 的模板线宽不对：%v", kind, style)
		}
		if kind == AnnFib {
			levels, _ := style["levels"].([]any)
			if len(levels) != 4 {
				t.Fatalf("斐波那契模板应当有 4 个水平，实际 %d", len(levels))
			}
		}
	}
	for _, kind := range drawKinds {
		if seen[kind] == 0 {
			t.Fatalf("%s 没有出厂模板，打开就不能直接画", kind)
		}
	}
}

func TestTemplateSaveBroadcastsToEveryone(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	b := newFakeClient(t, hub)
	b.waitFor(MsgWelcome)

	a.send(hub, map[string]any{
		"type": "save_template",
		"template": map[string]any{
			"kind":  AnnTrend,
			"name":  "我的趋势线",
			"style": map[string]any{"color": "#0EA5E9", "width": 3, "dash": "dashed"},
		},
	})
	msg := a.waitFor(MsgTemplates)
	id, _ := msg["lastSavedId"].(string)
	if id == "" {
		t.Fatalf("新建模板应当回一个 lastSavedId：%v", msg)
	}
	// 另一个人也要收到（模板是全局的，不是「谁建谁看得见」）
	if got := b.waitFor(MsgTemplates); got["lastSavedId"] != id {
		t.Fatalf("第二个连接没收到新模板：%v", got)
	}

	found := false
	for _, item := range templatesOf(t, a, hub) {
		tpl := item.(map[string]any)
		if tpl["id"] != id {
			continue
		}
		found = true
		style := tpl["style"].(map[string]any)
		if tpl["name"] != "我的趋势线" || style["dash"] != DashDashed || style["width"].(float64) != 3 {
			t.Fatalf("存下来的模板内容不对：%v", tpl)
		}
	}
	if !found {
		t.Fatal("新建的模板没出现在列表里")
	}

	// 就地改同一条：id 不变、条数不变
	before := len(templatesOf(t, a, hub))
	a.send(hub, map[string]any{
		"type": "save_template",
		"template": map[string]any{
			"id":    id,
			"kind":  AnnTrend,
			"name":  "改过名字",
			"style": map[string]any{"color": "#EC4899", "width": 2, "dash": "solid"},
		},
	})
	a.waitFor(MsgTemplates)
	after := templatesOf(t, a, hub)
	if len(after) != before {
		t.Fatalf("改一条不该新增：改前 %d 条，改后 %d 条", before, after)
	}
	for _, item := range after {
		tpl := item.(map[string]any)
		if tpl["id"] == id && tpl["name"] != "改过名字" {
			t.Fatalf("名字没改掉：%v", tpl)
		}
	}

	// 删掉：列表里就没有了
	a.send(hub, map[string]any{"type": "delete_template", "templateId": id})
	a.waitFor(MsgTemplates)
	if len(templatesOf(t, a, hub)) != before-1 {
		t.Fatal("删掉之后条数没减少")
	}
	// 删不存在的：回错误，不是静默成功
	a.send(hub, map[string]any{"type": "delete_template", "templateId": "tpl-999"})
	if e := a.waitFor(MsgError); e["message"] == "" {
		t.Fatal("删不存在的模板应当报错")
	}
}

func TestTemplateIsSanitized(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)

	// 类型不认识：直接拒绝
	a.send(hub, map[string]any{
		"type":     "save_template",
		"template": map[string]any{"kind": "text", "name": "x"},
	})
	if e := a.waitFor(MsgError); e["message"] == "" {
		t.Fatal("不认识的模板类型应当报错")
	}

	// 名字空、线宽越界、颜色非法：都夹成合法值，而不是拒绝
	a.send(hub, map[string]any{
		"type": "save_template",
		"template": map[string]any{
			"kind":  AnnRect,
			"name":  "   ",
			"style": map[string]any{"color": "red", "width": 99, "dash": "dotted", "fill": "#123456", "fillOpacity": 0.4},
		},
	})
	msg := a.waitFor(MsgTemplates)
	id, _ := msg["lastSavedId"].(string)
	for _, item := range templatesOf(t, a, hub) {
		tpl := item.(map[string]any)
		if tpl["id"] != id {
			continue
		}
		if tpl["name"] != kindLabel(AnnRect) {
			t.Fatalf("空名字应当补成 %q，实际 %v", kindLabel(AnnRect), tpl["name"])
		}
		style := tpl["style"].(map[string]any)
		if style["color"] != defaultColor {
			t.Fatalf("非法颜色应当回落到 %s，实际 %v", defaultColor, style["color"])
		}
		if style["width"].(float64) != maxLineWidth {
			t.Fatalf("线宽应当夹到 %v，实际 %v", maxLineWidth, style["width"])
		}
		if style["dash"] != DashSolid {
			t.Fatalf("不认识的线型应当回落成实线，实际 %v", style["dash"])
		}
		if style["fill"] != "#123456" {
			t.Fatalf("合法填充色不该被改：%v", style["fill"])
		}
	}
}

func TestTemplateLimitPerKind(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	for i := 0; i < maxTemplatesPerKind+5; i++ {
		a.send(hub, map[string]any{
			"type":     "save_template",
			"template": map[string]any{"kind": AnnRay, "name": "批量"},
		})
	}
	// 出厂模板已经占了两条，所以再建到上限之后就该开始报错
	if e := a.waitFor(MsgError); e["message"] == "" {
		t.Fatal("超过单工具模板上限应当报错")
	}
	n := 0
	for _, item := range templatesOf(t, a, hub) {
		if item.(map[string]any)["kind"] == AnnRay {
			n++
		}
	}
	if n > maxTemplatesPerKind {
		t.Fatalf("%s 的模板超过上限了：%d 条", AnnRay, n)
	}
}

func TestTemplatesSurviveRestart(t *testing.T) {
	root := writeTestData(t, 5)
	stateDir := t.TempDir()

	hub := newPersistentHub(t, root, stateDir)
	a := newFakeClient(t, hub)
	a.waitFor(MsgWelcome)
	a.send(hub, map[string]any{
		"type": "save_template",
		"template": map[string]any{
			"kind":  AnnFib,
			"name":  "重启用",
			"style": map[string]any{"width": 2.5, "dash": "dashed"},
		},
	})
	saved := a.waitFor(MsgTemplates)["lastSavedId"].(string)
	if _, err := os.Stat(filepath.Join(stateDir, templatesFileName)); err != nil {
		t.Fatalf("模板文件没有写出来：%v", err)
	}
	hub.Close()

	// 换一个 Hub 指向同一个目录：出厂模板 + 存下来的那条，顺序上出厂在前
	hub2 := newPersistentHub(t, root, stateDir)
	b := newFakeClient(t, hub2)
	b.waitFor(MsgWelcome)
	list := templatesOf(t, b, hub2)
	var got map[string]any
	seenDefault := false
	for _, item := range list {
		tpl := item.(map[string]any)
		if tpl["id"] == saved {
			got = tpl
		}
		if tpl["name"] == "金色实线" {
			seenDefault = true
		}
	}
	if got == nil {
		t.Fatal("重启后自定义模板不见了")
	}
	if !seenDefault {
		t.Fatal("重启后出厂模板不该丢（它们也是列表的一部分）")
	}
	style := got["style"].(map[string]any)
	if style["width"].(float64) != 2.5 || style["dash"] != DashDashed {
		t.Fatalf("重启后样式不对：%v", style)
	}
	if levels, _ := style["levels"].([]any); len(levels) != 4 {
		t.Fatalf("斐波那契模板的四个水平没存下来：%v", style)
	}
}
