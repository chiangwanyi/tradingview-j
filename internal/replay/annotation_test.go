package replay

import (
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
		a, err := inst.addAnnotation("human", AnnLine,
			Point{Time: "2026-01-05 09:00", Price: c.price},
			Point{Time: "2026-01-05 09:00", Price: c.price}, "", "", true)
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
		Point{Time: "2026-01-05 09:10", Price: 102.71}, "", "", false)
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
	_, err := inst.addAnnotation("human", AnnLine,
		Point{Time: "2026-01-05 09:10", Price: 102},
		Point{Time: "2026-01-05 09:10", Price: 103}, "", "", false)
	if err == nil {
		t.Fatal("画在还没出现的 K 线上应当报错")
	}
	if !strings.Contains(err.Error(), "已经出现") {
		t.Fatalf("错误信息应当说清楚原因，实际：%v", err)
	}
}

func TestAnnotationUnknownTimeSnapsToNearestBar(t *testing.T) {
	inst := newTradingInstance()
	inst.Cursor = 2
	a, err := inst.addAnnotation("human", AnnText,
		Point{Time: "2026-01-05 09:07", Price: 101}, Point{}, "看看这里", "", false)
	if err != nil {
		t.Fatalf("加标注失败：%v", err)
	}
	if a.Start.Time != "2026-01-05 09:05" && a.Start.Time != "2026-01-05 09:10" {
		t.Fatalf("应当落到相邻的 K 线上，实际 %s", a.Start.Time)
	}
	if a.Kind != AnnText || a.Text != "看看这里" {
		t.Fatalf("文字标注内容不对：%+v", a)
	}
}

func TestAnnotationTextIsTruncatedAndValidated(t *testing.T) {
	inst := newTradingInstance()
	long := strings.Repeat("字", maxAnnotationText+50)
	a, err := inst.addAnnotation("human", AnnText,
		Point{Time: "2026-01-05 09:00", Price: 100}, Point{}, long, "", false)
	if err != nil {
		t.Fatalf("加标注失败：%v", err)
	}
	if n := len([]rune(a.Text)); n != maxAnnotationText {
		t.Fatalf("文字应当截断到 %d 个字，实际 %d", maxAnnotationText, n)
	}
	if _, err := inst.addAnnotation("human", AnnText,
		Point{Time: "2026-01-05 09:00", Price: 100}, Point{}, "   ", "", false); err == nil {
		t.Fatal("空文字应当报错")
	}
	if _, err := inst.addAnnotation("human", "circle",
		Point{Time: "2026-01-05 09:00", Price: 100}, Point{}, "", "", false); err == nil {
		t.Fatal("不认识的类型应当报错")
	}
}

func TestAnnotationMoveAndDelete(t *testing.T) {
	inst := newTradingInstance()
	inst.Cursor = 2
	a, err := inst.addAnnotation("human", AnnLine,
		Point{Time: "2026-01-05 09:00", Price: 100},
		Point{Time: "2026-01-05 09:05", Price: 101}, "", "", false)
	if err != nil {
		t.Fatalf("加标注失败：%v", err)
	}
	if a.ID != "a1" {
		t.Fatalf("id 应当从 a1 开始，实际 %s", a.ID)
	}

	moved, err := inst.moveAnnotation("human", a.ID,
		Point{Time: "2026-01-05 09:10", Price: 102.2},
		Point{Time: "2026-01-05 09:10", Price: 103.3}, true)
	if err != nil {
		t.Fatalf("移动失败：%v", err)
	}
	if moved.Start.Time != "2026-01-05 09:10" || moved.Start.Price != 102 {
		t.Fatalf("移动后应当吸附到 09:10 的 OHLC：%+v", moved.Start)
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
		a, err := inst.addAnnotation("human", AnnText,
			Point{Time: "2026-01-05 09:00", Price: 100}, Point{}, "x", "", false)
		if err != nil {
			t.Fatalf("加标注失败：%v", err)
		}
		want := []string{"a1", "a2", "a3"}[i]
		if a.ID != want {
			t.Fatalf("第 %d 个标注 id 应当是 %s，实际 %s", i+1, want, a.ID)
		}
	}
}
