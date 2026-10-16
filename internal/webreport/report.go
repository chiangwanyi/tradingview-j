// Package webreport 把菜单里的「分析已抓取的数据」做成一个本地网页。
//
// 三条刻意的分工：
//
//  1. **统计口径全部来自 store.Scan** —— 和终端菜单 2 用的是同一份代码。
//     网页端只做展示，绝不在别的语言里重算一遍根数、断档、异常判定。
//     两份实现一旦漂移，表现就是「终端说这一天 288 根、网页说 287 根」，而用户根本没法裁决谁对。
//
//  2. **只读**：不建目录、不写索引、不改文件。和 store.Scan 的自我约束一致，
//     浏览器上点几次刷新不该对 data/ 产生任何副作用。
//
//  3. **前端资源用 embed 内嵌进 exe**：这个程序的使用方式是双击，
//     多一个必须跟 exe 放在一起的 static 目录，就等于多一种「拷漏了打不开」的失败方式。
//
// 这一层只做「把 store 的结构体翻译成 JSON 字段名」这一件事，不做任何数值加工：
// 原样给出时间戳、根数、字节数，格式化（千分位、KB/MB）交给前端。
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
// 网页上要把这些显示出来：同一个数据目录，时区或周期一变，「哪一天」和应有根数就会跟着变。
type Meta struct {
	Root        string `json:"root"`        // 数据目录（绝对路径）
	Step        int64  `json:"step"`        // 一根 K 线多少秒（config.intervalSeconds）
	TZ          string `json:"tz"`          // 判「哪一天」用的时区
	Exists      bool   `json:"exists"`      // 数据目录是否存在
	Version     string `json:"version"`     // 程序版本
	GeneratedAt int64  `json:"generatedAt"` // 本次扫描完成时刻（Unix 秒）
	ScanMs      int64  `json:"scanMs"`      // 本次扫描耗时（毫秒）
	// ExpectRule 是「这个星期几应当有多少根」的判定表，原样透传。
	// 网页的「统计口径」直接把它印出来 —— 判据只在 store.ExpectedRule 一处，
	// HTML 里再抄一遍数字迟早会漏改，表现成「页面上写着 216、程序按 204 判」。
	ExpectRule []WeekExpect `json:"expectRule"`
}

// WeekExpect 对应 store.WeekExpect：星期几、正常取值是哪些。
type WeekExpect struct {
	Weekday string `json:"weekday"`
	Bars    []int  `json:"bars"`
}

// Totals 是全局合计。
type Totals struct {
	Symbols int   `json:"symbols"` // 有数据的标的目录数
	Days    int   `json:"days"`    // 日文件数
	Bars    int   `json:"bars"`    // K 线根数合计
	Bytes   int64 `json:"bytes"`   // 占用磁盘
	Skipped int   `json:"skipped"` // 解析不了的行数合计
	Dupes   int   `json:"dupes"`   // 时间戳重复的行数合计
	BarsOff int   `json:"barsOff"` // 根数与应有根数不符的日文件数
	// MismatchDays = 各标的根数不完全一致的天数。注意它是「天」，上面两个是「份文件」，
	// 因为它们各自回答的是不同的问题（一天有 3 份文件不符，也只是 1 天的事）。
	MismatchDays int `json:"mismatchDays"`
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
//
// 这里**没有**「缺口」「最大断档」两个字段：它们把「缺在哪一刻到哪一刻」压成一个数字，
// 而那个数字（12 根）在期货每日结算休市这种正常形态下天天出现，看不出任何东西。
// 换成 Gaps —— 缺失区间的起止时刻，正常的一天就是「05:00→06:05」，
// 什么时候反常一眼就能认出来。
type Day struct {
	Date     string `json:"date"`
	Bars     int    `json:"bars"`     // 解析出来的根数（同一时间戳只算一根）
	Rows     int    `json:"rows"`     // 文件里的数据行数（不含表头）
	Skipped  int    `json:"skipped"`  // 坏行
	Dupes    int    `json:"dupes"`    // 重复时间戳的行
	FirstT   int64  `json:"firstT"`
	LastT    int64  `json:"lastT"`
	Bytes    int64  `json:"bytes"`
	Gaps     []Gap  `json:"gaps"`     // 时间断档（缺失区间的起止时刻），按时间递增
	Expect   []int  `json:"expect"`   // 这一天按星期几应当有多少根
	BarsOff  bool   `json:"barsOff"`  // 根数不在 expect 里
	Mismatch bool   `json:"mismatch"` // 同一天各标的的根数不完全相同
	Anomaly  bool   `json:"anomaly"`  // 异常（根数不符 / 坏行 / 重复行 / 读不了），判定在 store 里
	Err      string `json:"err,omitempty"` // 非空 = 这份文件没统计到
}

// Gap 是一段时间断档：缺失区间的起止时刻 + 缺了几根。
type Gap struct {
	StartT int64 `json:"startT"`
	EndT   int64 `json:"endT"`
	Bars   int   `json:"bars"`
}

// DateTotal 是「这一天所有标的合计」。
type DateTotal struct {
	Date     string     `json:"date"`
	Symbols  int        `json:"symbols"`
	Bars     int        `json:"bars"`
	Issues   int        `json:"issues"`   // 这一天有几个标的被判为异常（日历要标红）
	Mismatch bool       `json:"mismatch"` // 这一天的根数在各标的之间不完全一致（日历要标黄）
	Majority int        `json:"majority"` // 出现最多的那个根数：明细表用它挑出「少数派」
	Counts   []BarCount `json:"counts"`   // 各种根数各有几个标的
}

// BarCount 对应 store.BarCount。
type BarCount struct {
	Bars    int `json:"bars"`
	Symbols int `json:"symbols"`
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
	for _, r := range store.ExpectedRule() {
		rep.Meta.ExpectRule = append(rep.Meta.ExpectRule, WeekExpect{Weekday: r.Weekday, Bars: r.Bars})
	}
	if rep.Meta.ExpectRule == nil {
		rep.Meta.ExpectRule = []WeekExpect{}
	}

	rep.Totals = Totals{
		Symbols:      len(res.Folders),
		Days:         res.Days,
		Bars:         res.Bars,
		Bytes:        res.Bytes,
		Skipped:      res.Skipped,
		Dupes:        res.Dupes,
		BarsOff:      res.BarsOff,
		MismatchDays: res.MismatchDays,
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
			// 空切片要建成 []，不能留 nil —— 前端直接 .length，null 会让模板当场报错
			gaps := make([]Gap, 0, len(d.Gaps))
			for _, g := range d.Gaps {
				gaps = append(gaps, Gap{StartT: g.StartT, EndT: g.EndT, Bars: g.Bars})
			}
			expect := append([]int{}, d.Expect...)
			if expect == nil {
				expect = []int{}
			}
			out.Files = append(out.Files, Day{
				Date:     d.Date,
				Bars:     d.Bars,
				Rows:     d.Rows,
				Skipped:  d.Skipped,
				Dupes:    d.Dupes,
				FirstT:   d.FirstT,
				LastT:    d.LastT,
				Bytes:    d.Bytes,
				Gaps:     gaps,
				Expect:   expect,
				BarsOff:  d.BarsOff,
				Mismatch: d.Mismatch,
				Anomaly:  d.Anomaly(),
				Err:      d.Err,
			})
		}
		rep.Folders = append(rep.Folders, out)
	}

	for _, r := range res.ByDate() {
		counts := make([]BarCount, 0, len(r.Counts))
		for _, c := range r.Counts {
			counts = append(counts, BarCount{Bars: c.Bars, Symbols: c.Symbols})
		}
		rep.ByDate = append(rep.ByDate, DateTotal{
			Date:     r.Date,
			Symbols:  r.Symbols,
			Bars:     r.Bars,
			Issues:   r.Issues,
			Mismatch: r.Mismatch,
			Majority: r.Majority,
			Counts:   counts,
		})
	}
	return rep
}
