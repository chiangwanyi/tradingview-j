// Package webreport 把菜单里的「分析已抓取的数据」做成一个本地网页。
//
// 三条刻意的分工：
//
//  1. **统计口径全部来自 store.Scan** —— 和终端菜单 2 用的是同一份代码。
//     网页端只做展示，绝不在别的语言里重算一遍根数/缺口。两份实现一旦漂移，
//     表现就是「终端说这一天 288 根、网页说 287 根」，而用户根本没法裁决谁对。
//
//  2. **只读**：不建目录、不写索引、不改文件。和 store.Scan 的自我约束一致，
//     浏览器上点几次刷新不该对 data/ 产生任何副作用。
//
//  3. **前端资源用 embed 内嵌进 exe**：这个程序的使用方式是双击，
//     多一个必须跟 exe 放在一起的 static 目录，就等于多一种「拷漏了打不开」的失败方式。
//
// 这一层只做「把 store 的结构体翻译成 JSON 字段名」这一件事，不做任何数值加工：
// 原样给出时间戳、根数、字节数，格式化（千分位、"1时5分"）交给前端。
// 这样 JSON 里的数字永远和终端上打印的是同一个数，格式化差异只影响长相。
package webreport

import (
	"fmt"
	"time"

	"tradingview-j/internal/store"
)

// tzLabel 把时区说清楚：光写 loc.String() 在 Windows 上就是一句 "Local"，
// 而「换机器/换时区会影响哪一天的划分」正是这一栏存在的理由（见 store 的注释）。
// 所以名字和实际偏移一起给：Local（中国标准时间 UTC+08:00）。
func tzLabel(loc *time.Location) string {
	name, off := time.Now().In(loc).Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	return fmt.Sprintf("%s（%s UTC%s%02d:%02d）", loc.String(), name, sign, off/3600, off%3600/60)
}

// Report 是喂给网页的一次扫描快照。
type Report struct {
	Meta   Meta        `json:"meta"`
	Totals Totals      `json:"totals"`
	Span   *Span       `json:"span,omitempty"`
	Notes  []string    `json:"notes"` // store.Scan 记下的异常情况，原样透传
	Folders []Folder   `json:"folders"`
	ByDate []DateTotal `json:"byDate"`
}

// Meta 是「这份快照是在什么口径下扫出来的」。
// 网页上要把这些显示出来：同一个数据目录，时区或周期一变，缺口数就会跟着变。
type Meta struct {
	Root        string `json:"root"`        // 数据目录（绝对路径）
	Step        int64  `json:"step"`        // 一根 K 线多少秒（config.intervalSeconds）
	TZ          string `json:"tz"`          // 判「哪一天」用的时区
	Exists      bool   `json:"exists"`      // 数据目录是否存在
	Version     string `json:"version"`     // 程序版本
	GeneratedAt int64  `json:"generatedAt"` // 本次扫描完成时刻（Unix 秒）
	ScanMs      int64  `json:"scanMs"`      // 本次扫描耗时（毫秒）
}

// Totals 是全局合计。
type Totals struct {
	Symbols int   `json:"symbols"` // 有数据的标的目录数
	Days    int   `json:"days"`    // 日文件数
	Bars    int   `json:"bars"`    // K 线根数合计
	Bytes   int64 `json:"bytes"`   // 占用磁盘
	Skipped int   `json:"skipped"` // 解析不了的行数合计
	Dupes   int   `json:"dupes"`   // 时间戳重复的行数合计
}

// Span 是所有标的合起来的时间跨度。
type Span struct {
	FirstT int64 `json:"firstT"`
	LastT  int64 `json:"lastT"`
}

// Folder 是一个标的目录。
type Folder struct {
	Folder  string `json:"folder"`  // 目录名
	Symbol  string `json:"symbol"`  // _symbols.json 里登记的标的；没登记就是目录名
	Known   bool   `json:"known"`   // 是否在索引里登记过
	Days    int    `json:"days"`    // 日文件数
	Bars    int    `json:"bars"`    // 根数合计
	Bytes   int64  `json:"bytes"`   // 占用
	Skipped int    `json:"skipped"` // 坏行合计
	Dupes   int    `json:"dupes"`   // 重复行合计
	HasTime bool   `json:"hasTime"`
	FirstT  int64  `json:"firstT"`
	LastT   int64  `json:"lastT"`
	Files   []Day  `json:"files"`
}

// Day 是一个日文件（文件名就是日期）。
type Day struct {
	Date    string `json:"date"`
	Bars    int    `json:"bars"`    // 解析出来的根数（同一时间戳只算一根）
	Rows    int    `json:"rows"`    // 文件里的数据行数（不含表头）
	Skipped int    `json:"skipped"` // 坏行
	Dupes   int    `json:"dupes"`   // 重复时间戳的行
	FirstT  int64  `json:"firstT"`
	LastT   int64  `json:"lastT"`
	Span    int    `json:"span"`    // 首尾之间按 step 推算「本应有」多少根
	Missing int    `json:"missing"` // Span - Bars
	MaxGap  int64  `json:"maxGap"`  // 相邻两根的最大间隔（秒）
	Bytes   int64  `json:"bytes"`
	Err     string `json:"err,omitempty"` // 非空 = 这份文件没统计到
}

// DateTotal 是「这一天所有标的合计」。
type DateTotal struct {
	Date    string `json:"date"`
	Symbols int    `json:"symbols"`
	Bars    int    `json:"bars"`
}

// Build 把一次 store.Scan 的结果翻译成网页用的快照。
//
// 纯函数：只读入参、不碰磁盘、不看时钟（GeneratedAt / ScanMs 由调用方补），
// 所以可以用一份手工构造的 ScanResult 直接断言，不用先造出一个数据目录。
func Build(res *store.ScanResult, loc *time.Location, version string) *Report {
	if loc == nil {
		loc = time.Local
	}
	rep := &Report{
		Meta: Meta{
			Root:    res.Root,
			Step:    res.Step,
			TZ:      tzLabel(loc),
			Exists:  res.Exists,
			Version: version,
		},
		Notes:   append([]string{}, res.Notes...),
		Folders: make([]Folder, 0, len(res.Folders)),
		ByDate:  []DateTotal{},
	}
	if rep.Notes == nil {
		rep.Notes = []string{}
	}

	rep.Totals = Totals{
		Symbols: len(res.Folders),
		Days:    res.Days,
		Bars:    res.Bars,
		Bytes:   res.Bytes,
		Skipped: res.Skipped,
		Dupes:   res.Dupes,
	}
	if first, last, ok := res.GlobalRange(); ok {
		rep.Span = &Span{FirstT: first, LastT: last}
	}

	for _, f := range res.Folders {
		out := Folder{
			Folder:  f.Folder,
			Symbol:  f.Symbol,
			Known:   f.Known,
			Days:    len(f.Days),
			Bars:    f.Bars,
			Bytes:   f.Bytes,
			Skipped: f.Skipped,
			Dupes:   f.Dupes,
			HasTime: f.HasTime,
			FirstT:  f.FirstT,
			LastT:   f.LastT,
			Files:   make([]Day, 0, len(f.Days)),
		}
		for _, d := range f.Days {
			out.Files = append(out.Files, Day{
				Date:    d.Date,
				Bars:    d.Bars,
				Rows:    d.Rows,
				Skipped: d.Skipped,
				Dupes:   d.Dupes,
				FirstT:  d.FirstT,
				LastT:   d.LastT,
				Span:    d.Span,
				Missing: d.Missing,
				MaxGap:  d.MaxGap,
				Bytes:   d.Bytes,
				Err:     d.Err,
			})
		}
		rep.Folders = append(rep.Folders, out)
	}

	for _, r := range res.ByDate() {
		rep.ByDate = append(rep.ByDate, DateTotal{Date: r.Date, Symbols: r.Symbols, Bars: r.Bars})
	}
	return rep
}
