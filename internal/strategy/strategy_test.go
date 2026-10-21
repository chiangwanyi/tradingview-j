package strategy

// 时间口径、参数合并、注册表 —— 这三块是「策略之外」的公共设施，
// 出错的后果统一直觉不到：交易日算错会让每日上限跨天累计，参数合并写错
// 会让前端填的数悄悄不生效。

import (
	"math"
	"testing"
)

// 06:00 之前属于前一个交易日：这是数据按自然日切文件带来的坑，必须钉住。
func TestTradeDaySplitsAtSixAM(t *testing.T) {
	cases := []struct{ at, want string }{
		{"2026-10-20 06:00", "2026-10-20"},
		{"2026-10-20 12:00", "2026-10-20"},
		{"2026-10-20 23:55", "2026-10-20"},
		{"2026-10-21 00:00", "2026-10-20"}, // 夜盘跨零点，仍算 20 日
		{"2026-10-21 04:55", "2026-10-20"}, // 最后一根仍属于前一日
		{"2026-10-21 05:59", "2026-10-20"},
		{"2026-10-21 06:00", "2026-10-21"}, // 新交易日
		{"2026-03-02 00:30", "2026-03-01"}, // 跨月也要对
		{"2026-01-01 02:00", "2025-12-31"}, // 跨年也要对
	}
	for _, c := range cases {
		if got := TradeDay(c.at); got != c.want {
			t.Errorf("TradeDay(%q) = %q，期望 %q", c.at, got, c.want)
		}
	}
}

func TestSessionEndWindow(t *testing.T) {
	cases := []struct {
		at   string
		want bool
	}{
		{"2026-10-21 04:45", false},
		{"2026-10-21 04:50", true},
		{"2026-10-21 04:55", true},
		{"2026-10-21 05:55", true},
		{"2026-10-21 06:00", false},
		{"2026-10-21 21:30", false},
		{"2026-10-21 00:00", false},
	}
	for _, c := range cases {
		if got := SessionEnd(c.at); got != c.want {
			t.Errorf("SessionEnd(%q) = %v，期望 %v", c.at, got, c.want)
		}
	}
}

// 日历日收盘只认 23:55 及以后；04:50~05:55 的结算收尾是另一条规则，两者不重叠。
func TestDayCloseWindow(t *testing.T) {
	cases := []struct {
		at   string
		want bool
	}{
		{"2026-10-21 23:45", false},
		{"2026-10-21 23:50", false},
		{"2026-10-21 23:55", true},
		{"2026-10-21 23:59", true}, // 非 5 分钟对齐的异常行也不该漏
		{"2026-10-21 00:00", false},
		{"2026-10-21 04:50", false}, // 结算收尾不是日历日收盘
		{"2026-10-21 09:30", false},
		{"2026-10-21", false}, // 没有时刻
		{"", false},
	}
	for _, c := range cases {
		if got := DayClose(c.at); got != c.want {
			t.Errorf("DayClose(%q) = %v，期望 %v", c.at, got, c.want)
		}
	}
}

// 日历日和交易日差 6 小时：00:00~05:59 属于「前一晚」这个日历日的文件，交易日照旧归前一天。
func TestCalendarDayVersusTradeDay(t *testing.T) {
	cases := []struct {
		at      string
		cal     string
		tradeDy string
	}{
		{"2026-10-21 23:55", "2026-10-21", "2026-10-21"},
		{"2026-10-22 00:00", "2026-10-22", "2026-10-21"},
		{"2026-10-22 05:55", "2026-10-22", "2026-10-21"},
		{"2026-10-22 06:00", "2026-10-22", "2026-10-22"},
	}
	for _, c := range cases {
		if got := CalendarDay(c.at); got != c.cal {
			t.Errorf("CalendarDay(%q) = %q，期望 %q", c.at, got, c.cal)
		}
		if got := TradeDay(c.at); got != c.tradeDy {
			t.Errorf("TradeDay(%q) = %q，期望 %q", c.at, got, c.tradeDy)
		}
	}
	if got := CalendarDay("bad"); got != "" {
		t.Errorf("畸形输入该返回空串，实际 %q", got)
	}
}

// 时间解析要能扛住各种畸形输入，不能 panic 也不能把 24:00 当成有效时间。
func TestMinutesOfDayRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "2026-10-20", "2026-10-20 2:00", "2026-10-20 24:00", "2026-10-20 12:60"} {
		if m := MinutesOfDay(bad); m != -1 {
			t.Errorf("MinutesOfDay(%q) = %d，期望 -1", bad, m)
		}
	}
	if m := MinutesOfDay("2026-10-20 21:30"); m != 21*60+30 {
		t.Errorf("21:30 应当是 %d，实际 %d", 21*60+30, m)
	}
}

// 参数合并：默认值打底、覆盖生效、未知键忽略、越界夹紧。
func TestMergeParams(t *testing.T) {
	params := []Param{
		{Key: "a", Default: 1, Min: 0, Max: 10},
		{Key: "b", Default: 0.5, Min: 0, Max: 1},
	}
	v := Merge(params, Values{"a": 5, "b": 9, "nope": 3})
	if v["a"] != 5 {
		t.Errorf("a 应当被覆盖成 5，实际 %v", v["a"])
	}
	if v["b"] != 1 {
		t.Errorf("b 越界应当夹到上限 1，实际 %v", v["b"])
	}
	if _, ok := v["nope"]; ok {
		t.Error("未知参数不该出现在结果里")
	}
	if got := Merge(params, nil); got["a"] != 1 || got["b"] != 0.5 {
		t.Errorf("没有覆盖时应当全是默认值，实际 %+v", got)
	}
}

// 价格对齐：两个方向都要落在刻度上，而且不能把已经对齐的价格推到下一格。
func TestAlignTickDirections(t *testing.T) {
	cases := []struct{ price, tick, up, down float64 }{
		{102.3, 0.25, 102.5, 102.25},
		{102.25, 0.25, 102.25, 102.25}, // 已经对齐：两个方向都不动
		{94.3, 0.25, 94.5, 94.25},
		{100.04, 0.1, 100.1, 100},
		{100, 0.01, 100, 100},
		{100.123, 0, 100.123, 100.123}, // tick 未知时原样返回
	}
	for _, c := range cases {
		if got := AlignTickUp(c.price, c.tick); math.Abs(got-c.up) > 1e-9 {
			t.Errorf("AlignTickUp(%v, %v) = %v，期望 %v", c.price, c.tick, got, c.up)
		}
		if got := AlignTickDown(c.price, c.tick); math.Abs(got-c.down) > 1e-9 {
			t.Errorf("AlignTickDown(%v, %v) = %v，期望 %v", c.price, c.tick, got, c.down)
		}
	}
}

// 注册表：Caveman 已登记、能按 id 造出来、未知 id 报错里带可用列表。
func TestRegistryListsCaveman(t *testing.T) {
	ids := IDList()
	found := false
	for _, id := range ids {
		if id == "caveman" {
			found = true
		}
	}
	if !found {
		t.Fatalf("注册表里应当有 caveman，实际 %v", ids)
	}
	for _, info := range List() {
		if info.ID != "caveman" {
			continue
		}
		if info.Name == "" || info.Summary == "" || len(info.Params) == 0 {
			t.Fatalf("策略描述不完整：%+v", info)
		}
	}

	s, err := New("caveman", Values{"targetR": 2})
	if err != nil {
		t.Fatalf("按 id 创建失败：%v", err)
	}
	if s.ID() != "caveman" {
		t.Fatalf("创建出来的不是 caveman：%q", s.ID())
	}

	if _, err := New("不存在", nil); err == nil {
		t.Fatal("未知策略应当报错")
	}
}

// New 出来的实例必须是干净的：上一条实例的状态不能漏到下一条。
func TestNewStartsClean(t *testing.T) {
	a, _ := New("caveman", Values{"targetR": 2})
	if err := a.Configure(Values{"targetR": 3}); err != nil {
		t.Fatalf("Configure 失败：%v", err)
	}
	b, _ := New("caveman", nil)
	// 直接看参数：b 的 targetR 应当是默认值 1，而不是 a 改过的 3
	if got := b.Params(); len(got) == 0 {
		t.Fatal("参数列表不该为空")
	}
	bars := []Bar{
		flat("2026-10-20 10:00"),
		bar("2026-10-20 10:05", 100, 102, 99.8, 101.8),
		bar("2026-10-20 10:10", 101.8, 101.9, 99.5, 99.6),
	}
	sigs := feed(t, b, bars, 0.25)
	if len(sigs) != 1 {
		t.Fatalf("应当有 1 个信号，实际 %d", len(sigs))
	}
	// 默认 1R：入场 99.6，止损 102.25，R=2.65，目标 96.95 向下对齐 = 96.75
	if math.Abs(sigs[0].Target-96.75) > 1e-9 {
		t.Fatalf("新实例应当用默认 1R（目标 96.75），实际 %v", sigs[0].Target)
	}
}
