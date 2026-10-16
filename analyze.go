package main

// 菜单里的「2  分析已抓取的数据」。
//
// 这一条路径**只读**：走的是 store.Scan（纯扫描），而不是 store.Store。
// Store 是给写入用的 —— 它一 Open 就会建数据目录、会在 _symbols.json 里分配
// 标的目录名。统计动作不该对数据产生任何副作用，所以这里刻意绕开它。
//
// 统计的口径（根数 / 缺口 / 断档）全部来自 store 层，和落盘用的是同一份
// 解析规则，不在这里重新实现一遍 CSV 解析。

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"tradingview-j/internal/logx"
	"tradingview-j/internal/store"
)

func runAnalyze(lg *logx.Logger, baseDir string) int {
	cfg, ok := loadConfig(lg, baseDir)
	if !ok {
		return 1
	}
	loc := time.Local
	root := cfg.OutPath()

	lg.Rule(72)
	lg.Plain("  数据分析 · %s", root)
	lg.Plain("  一根 K 线 %d 秒（config.json 的 intervalSeconds）· 时区 %s", cfg.IntervalSeconds, loc)
	lg.Rule(72)

	res := store.Scan(root, loc, cfg.IntervalSeconds)
	for _, n := range res.Notes {
		lg.Warn("%s", n)
	}
	if len(res.Folders) == 0 {
		if res.Exists {
			lg.Plain("  数据目录里还没有任何 K 线。")
			lg.Plain("  先选 1 启动抓取，然后在 TradingView 图表上拖一拖，数据就会落到这里。")
		}
		lg.Rule(72)
		return 0
	}

	printOverview(lg, res, loc)
	for _, f := range res.Folders {
		printFolder(lg, f, loc)
	}
	if len(res.Folders) > 1 {
		printByDate(lg, res)
	}
	lg.Rule(72)
	return 0
}

func printOverview(lg *logx.Logger, res *store.ScanResult, loc *time.Location) {
	lg.Plain("  标的 %d 个 · 日文件 %d 个 · 合计 %s 根 · 磁盘 %s",
		len(res.Folders), res.Days, comma(res.Bars), humanBytes(res.Bytes))
	if f, l, ok := globalRange(res, loc); ok {
		lg.Plain("  时间跨度  %s ~ %s", f, l)
	}
	if res.Skipped > 0 || res.Dupes > 0 {
		lg.Warn("有 %d 行解析不了、%d 行时间戳重复（见下面备注列标了「坏N行」「重复N行」的日文件）",
			res.Skipped, res.Dupes)
	}
	lg.Plain("")
	lg.Plain("  口径 根数 = 该日文件里的 K 线行数（同一时间戳只算一根）")
	lg.Plain("       缺口 = 首根与尾根之间按 %d 秒推算应当有多少根，减去实有根数", res.Step)
	lg.Plain("       最大断档 = 相邻两根之间最大的间隔（拖图时跳过了范围，或交易所休市）")
	lg.Plain("")
}

// 表格列宽（显示宽度）。用固定列宽 + 按宽度补齐，而不是 %-12s：
// 中文一个字占两列却只有一个 rune，用 %-Ns 排出来的表会串行。
const (
	wDate    = 11
	wBars    = 6
	wClock   = 6
	wMissing = 5
	wGap     = 8
	wSize    = 9
)

func printFolder(lg *logx.Logger, f store.FolderStat, loc *time.Location) {
	title := f.Symbol
	switch {
	case f.Known && !strings.EqualFold(f.Symbol, f.Folder):
		title = fmt.Sprintf("%s    （文件夹 %s/）", f.Symbol, f.Folder)
	case !f.Known:
		title = fmt.Sprintf("%s    （文件夹同名；_symbols.json 里没有登记）", f.Folder)
	}
	lg.Plain("  ▸ %s", title)

	line := fmt.Sprintf("      %d 天 · %s 根 · %s", len(f.Days), comma(f.Bars), humanBytes(f.Bytes))
	if f.HasTime {
		line += fmt.Sprintf(" · %s ~ %s", store.Stamp(f.FirstT, loc), store.Stamp(f.LastT, loc))
	}
	lg.Plain("%s", line)
	lg.Plain("%s", strings.TrimRight(
		"      "+cell("日期", wDate, false)+" "+cell("根数", wBars, true)+" "+
			cell("首根", wClock, true)+" "+cell("尾根", wClock, true)+" "+
			cell("缺口", wMissing, true)+" "+cell("最大断档", wGap, true)+" "+cell("文件", wSize, true), " "))

	for _, d := range f.Days {
		if d.Err != "" {
			lg.Plain("      %-10s  读不了：%s", d.Date, d.Err)
			continue
		}
		note := ""
		if d.Skipped > 0 {
			note += fmt.Sprintf("  坏%d行", d.Skipped)
		}
		if d.Dupes > 0 {
			note += fmt.Sprintf("  重复%d行", d.Dupes)
		}
		gap := "—"
		if d.Bars > 1 {
			gap = gapText(d.MaxGap)
		}
		row := "      " + cell(d.Date, wDate, false) + " " + cell(comma(d.Bars), wBars, true) + " " +
			cell(store.Clock(d.FirstT, loc), wClock, true) + " " + cell(store.Clock(d.LastT, loc), wClock, true) + " " +
			cell(strconv.Itoa(d.Missing), wMissing, true) + " " + cell(gap, wGap, true) + " " +
			cell(humanBytes(d.Bytes), wSize, true) + note
		lg.Plain("%s", strings.TrimRight(row, " "))
	}
	lg.Plain("")
}

// printByDate 把所有标的合并成「这一天一共多少根」。
// 只有一个标的时这一节没有信息量，所以调用方会跳过它。
func printByDate(lg *logx.Logger, res *store.ScanResult) {
	rows := res.ByDate()
	if len(rows) == 0 {
		return
	}
	const wSyms, wTotal = 12, 8
	lg.Plain("  全部标的按日期合计")
	lg.Plain("%s", strings.TrimRight(
		"      "+cell("日期", wDate, false)+" "+cell("有数据的标的", wSyms, true)+" "+cell("根数", wTotal, true), " "))
	for _, r := range rows {
		lg.Plain("%s", strings.TrimRight(
			"      "+cell(r.Date, wDate, false)+" "+cell(strconv.Itoa(r.Symbols), wSyms, true)+" "+
				cell(comma(r.Bars), wTotal, true), " "))
	}
	lg.Plain("")
}

func globalRange(res *store.ScanResult, loc *time.Location) (string, string, bool) {
	first, last, ok := res.GlobalRange()
	if !ok {
		return "", "", false
	}
	return store.Stamp(first, loc), store.Stamp(last, loc), true
}

// ---------------- 排版小工具 ----------------

// cell 把一段文本补齐到 w 个显示列。
func cell(s string, w int, right bool) string {
	n := w - logx.Width(s)
	if n <= 0 {
		return s
	}
	if right {
		return strings.Repeat(" ", n) + s
	}
	return s + strings.Repeat(" ", n)
}

// comma 加千分位：9132 -> 9,132。数到五位数以上时肉眼容易数错位。
func comma(n int) string {
	s := strconv.Itoa(n)
	sign := ""
	if strings.HasPrefix(s, "-") {
		sign, s = "-", s[1:]
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(s[i])
	}
	return sign + b.String()
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}

// gapText 把「最大断档」的秒数说成人话。
func gapText(sec int64) string {
	switch {
	case sec < 60:
		return fmt.Sprintf("%d秒", sec)
	case sec < 3600:
		return fmt.Sprintf("%d分", sec/60)
	default:
		h, m := sec/3600, sec%3600/60
		if m == 0 {
			return fmt.Sprintf("%d时", h)
		}
		return fmt.Sprintf("%d时%d分", h, m)
	}
}
