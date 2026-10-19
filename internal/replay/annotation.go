package replay

// 标注的类型与样式规则。
//
// 三条原则，改代码前先读一遍：
//
//  1. **只有五种图形**：趋势线、射线、矩形、箭头线、斐波那契回撤。它们全部用「起点 + 终点」
//     两个点描述，所以协议、校验、拖动逻辑都只有一套 —— 加第六种图形只要在这里加个常量，
//     再在 does 的样式默认值里补一行。
//  2. **样式随图形存快照**（见 AnnotationStyle 的注释）：模板只是「画的时候抄一份」的来源。
//  3. **后端只把关，不解释**：颜色只校验格式、线宽只夹到范围内、斐波那契只认那四个比例。
//     后端不知道「红色代表看涨」这类语义，界面上怎么画是前端的事。

import (
	"math"
	"strings"
)

// 标注类型。这些字符串直接进 JSON，前端 types/protocol.ts 里有同名的一份。
const (
	AnnTrend = "trend" // 趋势线：两点之间的一段直线
	AnnRay   = "ray"   // 射线：从起点出发，穿过终点一直延伸到屏幕外
	AnnRect  = "rect"  // 矩形：两个对角点
	AnnArrow = "arrow" // 箭头线：趋势线 + 终点的箭头
	AnnFib   = "fib"   // 斐波那契回撤：两点价格区间摊开的四条水平线

	// 0.5.2 及以前的两个类型。读旧存档时会迁移掉（line → trend，text 丢弃），
	// 之后不会再出现在任何地方 —— 留着这两个常量只是为了让迁移代码能读得懂自己。
	legacyAnnLine = "line"
	legacyAnnText = "text"
)

// 斐波那契的四个水平。定义成固定顺序：界面上从上到下永远是 2 / 1 / 0 / -1。
var fibRatios = []float64{-1, 0, 1, 2}

const (
	minLineWidth = 1.0 // 再细看不清（而且 1px 以下在某些屏上是 0 像素）
	maxLineWidth = 6.0
	maxNameRunes = 24

	defaultColor  = "#f59e0b"
	defaultWidth  = 1.6
	defaultFillOp = 0.12
)

// drawKinds 是允许新增的图形类型（迁移用的两个 legacy 不在里面）。
var drawKinds = []string{AnnTrend, AnnRay, AnnRect, AnnArrow, AnnFib}

// kindLabels 只给「模板没写名字」「错误信息」这类地方用，界面上的中文标签在前端。
var kindLabels = map[string]string{
	AnnTrend: "趋势线",
	AnnRay:   "射线",
	AnnRect:  "矩形",
	AnnArrow: "箭头线",
	AnnFib:   "斐波那契回撤",
}

func isDrawKind(kind string) bool {
	for _, k := range drawKinds {
		if k == kind {
			return true
		}
	}
	return false
}

func kindLabel(kind string) string {
	if s, ok := kindLabels[kind]; ok {
		return s
	}
	return kind
}

// defaultLevelColors 是斐波那契四个水平的出厂颜色：四条线一眼能分清，深浅两套配色下都看得见。
var levelColorByRatio = map[float64]string{
	-1: "#38bdf8",
	0:  "#f59e0b",
	1:  "#ec4899",
	2:  "#22c55e",
}

// defaultFibLevels 按固定顺序给出四条水平。
func defaultFibLevels() []FibLevel {
	out := make([]FibLevel, 0, len(fibRatios))
	for _, r := range fibRatios {
		out = append(out, FibLevel{Ratio: r, Color: levelColorByRatio[r]})
	}
	return out
}

// defaultStyle 是一种图形的出厂样式（也是「模板少了字段」时的兜底）。
func defaultStyle(kind string) AnnotationStyle {
	s := AnnotationStyle{Color: defaultColor, Width: defaultWidth, Dash: DashSolid}
	switch kind {
	case AnnRect:
		s.Fill = defaultColor
		s.FillOpacity = defaultFillOp
	case AnnFib:
		s.Width = 1.2
		s.Levels = defaultFibLevels()
	}
	return s
}

// sanitizeStyle 把前端（或手工构造的消息、旧存档）交上来的样式夹进合法范围。
//
// 它**永远返回一个完整可画的样式**：颜色缺失/非法就给出厂色，线宽越界就夹到
// [1, 6]，斐波那契永远返回那四条（顺序、比例都补齐）。所以前端可以放心地把
// style 当「一定有值」的东西用。
func sanitizeStyle(kind string, in AnnotationStyle) AnnotationStyle {
	def := defaultStyle(kind)
	out := AnnotationStyle{
		Color: def.Color,
		Width: def.Width,
		Dash:  DashSolid,
	}
	if validColor(in.Color) {
		out.Color = in.Color
	}
	if in.Width > 0 && !math.IsNaN(in.Width) && !math.IsInf(in.Width, 0) {
		out.Width = math.Min(maxLineWidth, math.Max(minLineWidth, in.Width))
	}
	if in.Dash == DashDashed {
		out.Dash = DashDashed
	}
	if kind == AnnRect {
		// 矩形的填充色默认跟边框色一致（用户只选了边框色，填充跟着走才是直觉行为）。
		out.Fill = out.Color
		if validColor(in.Fill) {
			out.Fill = in.Fill
		}
		out.FillOpacity = def.FillOpacity
		if in.FillOpacity > 0 && in.FillOpacity <= 1 {
			out.FillOpacity = in.FillOpacity
		}
	}
	if kind == AnnFib {
		out.Levels = sanitizeLevels(in.Levels)
	}
	return out
}

// sanitizeLevels 只认 -1 / 0 / 1 / 2 这四个比例，颜色非法的那条回落到出厂色。
// 顺序固定成 -1、0、1、2，前端不用自己排。
func sanitizeLevels(in []FibLevel) []FibLevel {
	byRatio := map[float64]string{}
	for _, lv := range in {
		if validColor(lv.Color) {
			byRatio[lv.Ratio] = lv.Color
		}
	}
	out := make([]FibLevel, 0, len(fibRatios))
	for _, r := range fibRatios {
		color := byRatio[r]
		if color == "" {
			color = levelColorByRatio[r]
		}
		out = append(out, FibLevel{Ratio: r, Color: color})
	}
	return out
}

// validColor 接受 #rgb / #rrggbb / #rrggbbaa。**只认十六进制**：
// 允许 rgb()/关键字的话，一条消息里就能塞进任意长度的字符串。
func validColor(s string) bool {
	if len(s) != 4 && len(s) != 7 && len(s) != 9 {
		return false
	}
	if s[0] != '#' {
		return false
	}
	for i := 1; i < len(s); i++ {
		c := s[i]
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// normalize 是「读旧存档」时的迁移，返回 false 表示这条标注救不回来了（直接丢掉）：
//
//   - line（线段）→ trend（趋势线）：旧版画的线段就是两点直线，语义完全一样；
//   - text（文字）→ 丢弃：新版没有文字工具，也没有承载它的样式，硬转成一条零长度的
//     线只会在图上留个看不见的垃圾；
//   - 样式为空（旧档根本没有样式）→ 用 LegacyColor 或出厂色补齐；
//   - offset 缺失（旧档）→ 0，等价于「端点就在那根 K 线上」，旧图位置一点不变。
func (a *Annotation) normalize() bool {
	if a.Kind == legacyAnnLine {
		a.Kind = AnnTrend
	}
	if a.Kind == legacyAnnText || !isDrawKind(a.Kind) {
		return false
	}
	// 手改过的存档可能把偏移写成一个负数或离谱的大数：读进来先夹一次，
	// 否则前端会拿到一个画不出东西（或画出屏幕外）的端点。
	a.Start.Offset = clampForwardBars(a.Start.Offset)
	a.End.Offset = clampForwardBars(a.End.Offset)
	if a.Style.Color == "" && a.LegacyColor != "" {
		a.Style.Color = a.LegacyColor
	}
	a.Style = sanitizeStyle(a.Kind, a.Style)
	a.LegacyColor = ""
	return true
}

// truncateRunes 按字符（不是字节）截断，中英文混排也不会截出半个字。
func truncateRunes(s string, max int) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) <= max {
		return string(r)
	}
	return string(r[:max])
}
