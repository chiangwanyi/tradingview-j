package replay

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAnnotationSnapsToNearestOHLC(t *testing.T) {
	inst := newTradingInstance() // 09:00 O100 H101 L99 C100
	cases := []struct {
		price float64
		want  float64
	}{
		{97.6, 99},   // 离最低价最近
		{100.9, 101}, // 离最高价最近
		{100.2, 100}, // 离开盘价最近
	}
	for _, c := range cases {
		a, err := inst.addAnnotation("human", AnnTrend,
			Point{Time: "2026-01-05 09:00", Price: c.price},
			Point{Time: "2026-01-05 09:00", Price: c.price}, AnnotationStyle{}, true)
		if err != nil {
			t.Fatalf("加标注失败：%v", err)
		}
		if a.Start.Price != c.want {
			t.Fatalf("价格 %v 应当吸附到 %v，实际 %v", c.price, c.want, a.Start.Price)
		}
		if !a.Snapped {
			t.Fatal("应当记住这次吸附过")
		}
	}
}

func TestAnnotationKeepsPriceWhenNotSnapped(t *testing.T) {
	inst := newTradingInstance()
	inst.Cursor = 2
	a, err := inst.addAnnotation("human", AnnRect,
		Point{Time: "2026-01-05 09:00", Price: 100.37},
		Point{Time: "2026-01-05 09:10", Price: 102.71}, AnnotationStyle{}, false)
	if err != nil {
		t.Fatalf("加标注失败：%v", err)
	}
	if a.Start.Price != 100.37 || a.End.Price != 102.71 {
		t.Fatalf("不吸附时价格应当原样保留：%+v", a)
	}
	if a.Snapped {
		t.Fatal("没吸附就不该标记")
	}
}

func TestAnnotationCannotTargetFutureBar(t *testing.T) {
	inst := newTradingInstance() // cursor = 0
	_, err := inst.addAnnotation("human", AnnTrend,
		Point{Time: "2026-01-05 09:10", Price: 102},
		Point{Time: "2026-01-05 09:10", Price: 103}, AnnotationStyle{}, false)
	if err == nil {
		t.Fatal("画在还没出现的 K 线上应当报错")
	}
	if !strings.Contains(err.Error(), "已经出现") {
		t.Fatalf("错误信息应当说清楚原因，实际：%v", err)
	}
}

// 端点可以往右越过最后一根已揭示的 K 线：锚点仍然是真实 K 线，只多一个「向右几根」的偏移。
// 这条是「趋势线/矩形/箭头线/斐波那契都能画到未来」在服务端的入口。
func TestAnnotationCanExtendPastLastBar(t *testing.T) {
	inst := newTradingInstance() // cursor = 0：只有 09:00 已揭示，09:05 / 09:10 还藏着
	a, err := inst.addAnnotation("human", AnnTrend,
		Point{Time: "2026-01-05 09:00", Price: 100},
		Point{Time: "2026-01-05 09:00", Price: 103, Offset: 40}, AnnotationStyle{}, false)
	if err != nil {
		t.Fatalf("端点应当能画到最后一根右侧：%v", err)
	}
	if a.Start.Offset != 0 {
		t.Fatalf("落在 K 线上的端点 offset 应当是 0，实际 %d", a.Start.Offset)
	}
	if a.End.Offset != 40 {
		t.Fatalf("向右偏移应当原样保留成 40，实际 %d", a.End.Offset)
	}
	if a.End.Time != "2026-01-05 09:00" {
		t.Fatalf("锚点必须还是那根真实 K 线的时间，实际 %q", a.End.Time)
	}
	if a.End.Price != 103 {
		t.Fatalf("不吸附时价格不该被改，实际 %v", a.End.Price)
	}
}

// 五种图形的终点都能越过最后一根。
//
// 逻辑上它们共用 resolvePoint，一条就够；但「趋势线/矩形/箭头线/斐波那契都要能画到未来」
// 是需求原文，逐类钉一遍，将来谁给某一种加了特殊校验会立刻断在这里。
func TestAllKindsCanEndPastLastBar(t *testing.T) {
	inst := newTradingInstance()
	inst.Cursor = 1 // 只揭示到 09:05
	for _, kind := range drawKinds {
		a, err := inst.addAnnotation("human", kind,
			Point{Time: "2026-01-05 09:00", Price: 100},
			Point{Time: "2026-01-05 09:05", Price: 102, Offset: 25}, AnnotationStyle{}, false)
		if err != nil {
			t.Fatalf("%s 的终点应当能画过最后一根：%v", kind, err)
		}
		if a.End.Offset != 25 {
			t.Fatalf("%s 的向右偏移没保留下来：%d", kind, a.End.Offset)
		}
		if a.End.Time != "2026-01-05 09:05" {
			t.Fatalf("%s 的锚点应当还是最后一根：%q", kind, a.End.Time)
		}
	}
}

// 偏移是几何量：负数（想画到第一根左边）和离谱的大数都夹进合法范围，而不是让整条消息失败。
func TestAnnotationOffsetIsClamped(t *testing.T) {
	inst := newTradingInstance()
	a, err := inst.addAnnotation("human", AnnRay,
		Point{Time: "2026-01-05 09:00", Price: 100},
		Point{Time: "2026-01-05 09:00", Price: 100, Offset: -5}, AnnotationStyle{}, false)
	if err != nil {
		t.Fatalf("负偏移不该让整条消息失败：%v", err)
	}
	if a.End.Offset != 0 {
		t.Fatalf("负偏移应当夹到 0，实际 %d", a.End.Offset)
	}

	b, err := inst.addAnnotation("human", AnnRect,
		Point{Time: "2026-01-05 09:00", Price: 100},
		Point{Time: "2026-01-05 09:00", Price: 101, Offset: maxForwardBars + 999}, AnnotationStyle{}, false)
	if err != nil {
		t.Fatalf("超上限的偏移不该让整条消息失败：%v", err)
	}
	if b.End.Offset != maxForwardBars {
		t.Fatalf("超上限的偏移应当夹到 %d，实际 %d", maxForwardBars, b.End.Offset)
	}
}

// 拖动：偏移端点能继续往右拖，也能拖回历史里 —— 后者要把 offset 归零、锚点换成那根 K 线，
// 否则前端会拿着「最后一根 + 负偏移」这种自相矛盾的坐标。
func TestAnnotationMoveAcrossLastBar(t *testing.T) {
	inst := newTradingInstance()
	inst.Cursor = 2
	a, err := inst.addAnnotation("human", AnnTrend,
		Point{Time: "2026-01-05 09:00", Price: 100},
		Point{Time: "2026-01-05 09:10", Price: 102, Offset: 10}, AnnotationStyle{}, false)
	if err != nil {
		t.Fatalf("加标注失败：%v", err)
	}

	// 再往右 30 根：锚点和价格不变，偏移变大
	m, err := inst.moveAnnotation("human", a.ID,
		Point{Time: a.Start.Time, Price: a.Start.Price},
		Point{Time: a.End.Time, Price: a.End.Price, Offset: 40}, false)
	if err != nil {
		t.Fatalf("往右拖失败：%v", err)
	}
	if m.End.Offset != 40 || m.End.Time != "2026-01-05 09:10" {
		t.Fatalf("往右拖应当保留锚点、只改偏移：%+v", m.End)
	}

	// 拖回历史：锚点换成 09:05，offset 归零
	m2, err := inst.moveAnnotation("human", a.ID,
		Point{Time: "2026-01-05 09:00", Price: 100},
		Point{Time: "2026-01-05 09:05", Price: 101}, false)
	if err != nil {
		t.Fatalf("拖回历史失败：%v", err)
	}
	if m2.End.Offset != 0 || m2.End.Time != "2026-01-05 09:05" {
		t.Fatalf("拖回 K 线后 offset 应当是 0：%+v", m2.End)
	}
}

// 旧存档没有 offset 字段（等价 0），手改过的存档可能是负数/大数：读进来都要夹一次。
func TestNormalizeClampsOffsetOnLoad(t *testing.T) {
	a := Annotation{
		Kind:  AnnTrend,
		Start: Point{Time: "2026-01-05 09:00", Price: 100, Offset: -3},
		End:   Point{Time: "2026-01-05 09:00", Price: 101, Offset: maxForwardBars + 1},
	}
	if !a.normalize() {
		t.Fatal("trend 不该被丢掉")
	}
	if a.Start.Offset != 0 || a.End.Offset != maxForwardBars {
		t.Fatalf("读存档时应当把偏移夹进 [0,%d]：%+v / %+v", maxForwardBars, a.Start, a.End)
	}
}

// offset = 0 时不出现在 JSON 里：旧存档、旧客户端看到的 Point 和以前一模一样。
func TestPointOffsetOmittedWhenZero(t *testing.T) {
	buf, err := json.Marshal(Point{Time: "2026-01-05 09:00", Price: 100.5})
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if strings.Contains(string(buf), "offset") {
		t.Fatalf("offset 为 0 时不该出现在 JSON 里：%s", buf)
	}
	buf, err = json.Marshal(Point{Time: "2026-01-05 09:00", Price: 100.5, Offset: 7})
	if err != nil {
		t.Fatalf("序列化失败：%v", err)
	}
	if !strings.Contains(string(buf), `"offset":7`) {
		t.Fatalf("非 0 偏移要能存下来：%s", buf)
	}
}

func TestAnnotationUnknownTimeSnapsToNearestBar(t *testing.T) {
	inst := newTradingInstance()
	inst.Cursor = 2
	a, err := inst.addAnnotation("human", AnnArrow,
		Point{Time: "2026-01-05 09:07", Price: 101},
		Point{Time: "2026-01-05 09:07", Price: 102}, AnnotationStyle{}, false)
	if err != nil {
		t.Fatalf("加标注失败：%v", err)
	}
	if a.Start.Time != "2026-01-05 09:05" && a.Start.Time != "2026-01-05 09:10" {
		t.Fatalf("应当落到相邻的 K 线上，实际 %s", a.Start.Time)
	}
	if a.Kind != AnnArrow {
		t.Fatalf("类型不对：%+v", a)
	}
}

// 五种图形都要能画：这是「工具集」的硬约束，加一种忘了加进白名单会在这里断掉。
func TestAllFiveKindsAreDrawable(t *testing.T) {
	inst := newTradingInstance()
	inst.Cursor = 2
	for _, kind := range drawKinds {
		a, err := inst.addAnnotation("human", kind,
			Point{Time: "2026-01-05 09:00", Price: 100},
			Point{Time: "2026-01-05 09:05", Price: 101}, AnnotationStyle{}, false)
		if err != nil {
			t.Fatalf("%s 应当能画：%v", kind, err)
		}
		if a.Style.Width < minLineWidth || a.Style.Width > maxLineWidth {
			t.Fatalf("%s 的线宽没被补成合法值：%v", kind, a.Style.Width)
		}
	}
	for _, kind := range []string{"text", "circle", ""} {
		if _, err := inst.addAnnotation("human", kind,
			Point{Time: "2026-01-05 09:00", Price: 100},
			Point{Time: "2026-01-05 09:05", Price: 101}, AnnotationStyle{}, false); err == nil {
			t.Fatalf("%q 不该被接受", kind)
		}
	}
}

func TestStyleIsSanitized(t *testing.T) {
	// 非法颜色 → 出厂色；线宽越界 → 夹到 [1,6]；乱写的线型 → 实线
	got := sanitizeStyle(AnnTrend, AnnotationStyle{Color: "rgb(1,2,3)", Width: 99, Dash: "dotted"})
	if got.Color != defaultColor {
		t.Fatalf("非法颜色应当回落到 %s，实际 %s", defaultColor, got.Color)
	}
	if got.Width != maxLineWidth {
		t.Fatalf("线宽应当夹到 %v，实际 %v", maxLineWidth, got.Width)
	}
	if got.Dash != DashSolid {
		t.Fatalf("不认识的线型应当回落成实线，实际 %s", got.Dash)
	}
	if got := sanitizeStyle(AnnTrend, AnnotationStyle{Width: 0.1}); got.Width != minLineWidth {
		t.Fatalf("线宽下限是 %v，实际 %v", minLineWidth, got.Width)
	}
	if got := sanitizeStyle(AnnTrend, AnnotationStyle{Color: "#0EA5E9", Width: 2.5, Dash: DashDashed}); got.Color != "#0EA5E9" || got.Width != 2.5 || got.Dash != DashDashed {
		t.Fatalf("合法样式不该被改：%+v", got)
	}
}

// 斐波那契永远恰好四条水平，比例固定 -1/0/1/2，顺序固定；
// 前端只调颜色，比例和后端算出来的一致 —— 两边就不会画出不一样的四条线。
func TestFibLevelsAreFixed(t *testing.T) {
	// 少给、乱给、重复给，都要被补齐成标准的四条
	in := []FibLevel{
		{Ratio: 0.5, Color: "#123456"}, // 不认识的比例，丢掉
		{Ratio: 2, Color: "red"},       // 比例对、颜色非法，回落到出厂色
		{Ratio: -1, Color: "#abcdef"},  // 保留
	}
	got := sanitizeStyle(AnnFib, AnnotationStyle{Levels: in}).Levels
	if len(got) != len(fibRatios) {
		t.Fatalf("应当固定 %d 条水平，实际 %d 条：%+v", len(fibRatios), len(got), got)
	}
	for i, r := range fibRatios {
		if got[i].Ratio != r {
			t.Fatalf("第 %d 条比例应当是 %v，实际 %v", i, r, got[i].Ratio)
		}
		if !validColor(got[i].Color) {
			t.Fatalf("第 %d 条颜色不合法：%s", i, got[i].Color)
		}
	}
	if got[0].Color != "#abcdef" {
		t.Fatalf("-1 那条的自定义颜色应当保留，实际 %s", got[0].Color)
	}
	if got[3].Color != levelColorByRatio[2] {
		t.Fatalf("颜色非法的水平应当回落到出厂色，实际 %s", got[3].Color)
	}
}

func TestAnnotationMoveAndDelete(t *testing.T) {
	inst := newTradingInstance()
	inst.Cursor = 2
	a, err := inst.addAnnotation("human", AnnTrend,
		Point{Time: "2026-01-05 09:00", Price: 100},
		Point{Time: "2026-01-05 09:05", Price: 101},
		AnnotationStyle{Color: "#0ea5e9", Width: 3, Dash: DashDashed}, false)
	if err != nil {
		t.Fatalf("加标注失败：%v", err)
	}
	if a.ID != "a1" {
		t.Fatalf("id 应当从 a1 开始，实际 %s", a.ID)
	}

	// 只移动终点：起点原样发回。样式不许跟着变。
	moved, err := inst.moveAnnotation("human", a.ID,
		a.Start,
		Point{Time: "2026-01-05 09:10", Price: 103.3}, true)
	if err != nil {
		t.Fatalf("移动失败：%v", err)
	}
	if moved.End.Time != "2026-01-05 09:10" || !moved.Snapped {
		t.Fatalf("终点应当吸附到 09:10 的 OHLC：%+v", moved.End)
	}
	if moved.Start.Time != a.Start.Time {
		t.Fatalf("只动终点时起点不该动：%+v", moved.Start)
	}
	if moved.Style.Color != a.Style.Color || moved.Style.Width != a.Style.Width || moved.Style.Dash != a.Style.Dash {
		t.Fatalf("移动不该改样式：期望 %+v，实际 %+v", a.Style, moved.Style)
	}

	if err := inst.deleteAnnotation(a.ID); err != nil {
		t.Fatalf("删除失败：%v", err)
	}
	if len(inst.Annotations) != 0 {
		t.Fatalf("删完应当是空的，实际 %d 个", len(inst.Annotations))
	}
	if err := inst.deleteAnnotation(a.ID); err == nil {
		t.Fatal("删不存在的标注应当报错")
	}
	if _, err := inst.moveAnnotation("human", "a999",
		Point{Time: "2026-01-05 09:00", Price: 100}, Point{}, false); err == nil {
		t.Fatal("移动不存在的标注应当报错")
	}
}

func TestAnnotationIDsIncrement(t *testing.T) {
	inst := newTradingInstance()
	inst.Cursor = 1
	for i := 0; i < 3; i++ {
		a, err := inst.addAnnotation("human", AnnRay,
			Point{Time: "2026-01-05 09:00", Price: 100},
			Point{Time: "2026-01-05 09:00", Price: 101}, AnnotationStyle{}, false)
		if err != nil {
			t.Fatalf("加标注失败：%v", err)
		}
		want := []string{"a1", "a2", "a3"}[i]
		if a.ID != want {
			t.Fatalf("第 %d 个标注 id 应当是 %s，实际 %s", i+1, want, a.ID)
		}
	}
}

// 旧存档里的 line 要变成 trend、text 要丢掉 —— 这是「升级不炸」的那条线。
func TestAnnotationLegacyMigration(t *testing.T) {
	inst := &Instance{Annotations: []Annotation{
		{ID: "a1", Kind: "line", LegacyColor: "#38bdf8", Start: Point{Time: "2026-01-05 09:00", Price: 100}},
		{ID: "a2", Kind: "text", Start: Point{Time: "2026-01-05 09:00", Price: 100}},
		{ID: "a3", Kind: AnnRect, Start: Point{Time: "2026-01-05 09:00", Price: 100}},
	}}
	if dropped := migrateAnnotations(inst); dropped != 1 {
		t.Fatalf("应当丢掉 1 条文字标注，实际 %d 条", dropped)
	}
	if len(inst.Annotations) != 2 {
		t.Fatalf("应当剩 2 条，实际 %d 条", len(inst.Annotations))
	}
	if a := inst.Annotations[0]; a.Kind != AnnTrend || a.Style.Color != "#38bdf8" {
		t.Fatalf("line 应当变成 trend 并继承旧颜色：%+v", a)
	}
	if a := inst.Annotations[1]; a.Kind != AnnRect || a.Style.Fill == "" || a.Style.Width <= 0 {
		t.Fatalf("矩形应当补齐填充色与线宽：%+v", a)
	}
}
