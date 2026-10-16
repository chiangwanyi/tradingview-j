package store

// 只读扫描：菜单里的「分析已抓取的数据」用它。
//
// 为什么放在 store 里而不是新建一个包：它必须和写入路径共享同一份
// 「目录结构 + CSV 行格式 + _symbols.json」的规则（readDayFile / readIndexFile）。
// 统计口径和落盘口径一旦分开维护，就会出现「文件里明明写着 288 行，分析却说 287 根」
// 这类对不上的情况 —— 而用户看到两个数字打架时，根本没法判断该信哪个。
//
// 这一路径**只读**：不建目录、不写索引、不改文件。统计动作永远不该碰数据本身。

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DayStat 是一个日文件的摘要。
type DayStat struct {
	Date    string // 文件名里的日期
	Path    string
	Bars    int   // 解析出来的 K 线根数（按时间戳去重后）
	Rows    int   // 文件里的数据行数（不含表头）
	Skipped int   // 无法解析、被跳过的行数
	Dupes   int   // 同一时间戳重复出现的行数（正常写入下应当恒为 0）
	FirstT  int64 // 首根的时间戳
	LastT   int64 // 尾根的时间戳
	Span    int   // 首尾之间按 step 推算「本应有」多少根
	Missing int   // Span - Bars
	MaxGap  int64 // 相邻两根之间的最大间隔（秒）；只有一根时为 0
	Bytes   int64
	Err     string // 非空 = 这份文件没统计到（读不了）
}

// FolderStat 是一个标的目录的摘要。
type FolderStat struct {
	Folder  string
	Symbol  string // _symbols.json 里登记的标的；没登记就用文件夹名
	Known   bool   // 该文件夹在索引里有登记
	Days    []DayStat
	Bars    int
	Bytes   int64
	Skipped int // 坏行合计
	Dupes   int // 重复行合计
	FirstT  int64
	LastT   int64
	HasTime bool
}

// ScanResult 是整个数据目录的摘要。
type ScanResult struct {
	Root    string
	Step    int64 // 一根 K 线多少秒（来自 config.intervalSeconds）
	Exists  bool  // 数据目录是否存在
	Folders []FolderStat
	Days    int
	Bars    int
	Bytes   int64
	Skipped int
	Dupes   int
	Notes   []string // 异常情况（读不了之类的），由调用方原样打出来
}

// Scan 只读地扫一遍数据目录。任何问题都不返回 error，而是记进 Notes ——
// 「有一份文件读不了」不该让整次分析失败，用户更想看的是剩下那些能读的。
func Scan(root string, loc *time.Location, step int64) *ScanResult {
	if loc == nil {
		loc = time.Local
	}
	if step <= 0 {
		step = 300
	}
	res := &ScanResult{Root: root, Step: step}

	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			res.Notes = append(res.Notes, "数据目录还不存在（还没抓过数据？）")
		} else {
			res.Notes = append(res.Notes, fmt.Sprintf("读不了数据目录：%v", err))
		}
		return res
	}
	res.Exists = true

	// 文件夹 -> 标的。读不到索引不是错误：那时直接把文件夹名当标的显示，
	// 反正目录名就是标的 slug 化来的，两者通常长得一样。
	symbolOf := map[string]string{}
	if idx, err := readIndexFile(root); err == nil {
		for sym, rec := range idx.Symbols {
			if rec.Folder != "" {
				symbolOf[rec.Folder] = sym
			}
		}
	} else if !os.IsNotExist(err) {
		res.Notes = append(res.Notes, fmt.Sprintf("读 %s 失败（%v），下面标的显示为文件夹名", indexFile, err))
	}

	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		fs := scanFolder(filepath.Join(root, e.Name()), e.Name(), loc, step)
		if sym, ok := symbolOf[fs.Folder]; ok {
			fs.Symbol, fs.Known = sym, true
		} else {
			fs.Symbol = fs.Folder
		}
		if len(fs.Days) == 0 {
			continue // 空目录（没有 csv）不占版面
		}
		res.Folders = append(res.Folders, fs)
		res.Days += len(fs.Days)
		res.Bars += fs.Bars
		res.Bytes += fs.Bytes
		res.Skipped += fs.Skipped
		res.Dupes += fs.Dupes
	}
	sort.Slice(res.Folders, func(i, j int) bool { return res.Folders[i].Folder < res.Folders[j].Folder })
	return res
}

func scanFolder(dir, folder string, loc *time.Location, step int64) FolderStat {
	fs := FolderStat{Folder: folder}
	entries, err := os.ReadDir(dir)
	if err != nil {
		fs.Days = []DayStat{{Date: "(目录)", Path: dir, Err: fmt.Sprintf("读不了目录：%v", err)}}
		return fs
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".csv") {
			continue // .tmp 之类的中间产物不算数据
		}
		names = append(names, e.Name())
	}
	sort.Strings(names) // 文件名就是日期，字符串序 = 时间序

	for _, name := range names {
		d := scanDay(filepath.Join(dir, name), loc, step)
		fs.Days = append(fs.Days, d)
		fs.Bars += d.Bars
		fs.Bytes += d.Bytes
		fs.Skipped += d.Skipped
		fs.Dupes += d.Dupes
		if d.Bars > 0 {
			if !fs.HasTime || d.FirstT < fs.FirstT {
				fs.FirstT = d.FirstT
			}
			if !fs.HasTime || d.LastT > fs.LastT {
				fs.LastT = d.LastT
			}
			fs.HasTime = true
		}
	}
	return fs
}

func scanDay(path string, loc *time.Location, step int64) DayStat {
	d := DayStat{Date: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Path: path}
	if fi, err := os.Stat(path); err == nil {
		d.Bytes = fi.Size()
	}
	bars, rs, err := readDayFile(path, loc)
	d.Rows, d.Skipped = rs.Rows, rs.Bad
	d.Bars = len(bars)
	d.Dupes = rs.Good() - len(bars)
	if err != nil {
		d.Err = err.Error()
	}
	if d.Bars == 0 {
		return d
	}

	times := make([]int64, 0, len(bars))
	for t := range bars {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	d.FirstT, d.LastT = times[0], times[len(times)-1]

	if n := len(times); n > 1 {
		d.Span = int((times[n-1]-times[0])/step) + 1
		if d.Span < n {
			// step 比实际周期大（比如 config 里的 intervalSeconds 被改过）时，
			// 算出来的「应有根数」会小于实有。宁可显示 0 缺口，也不显示负数。
			d.Span = n
		}
		d.Missing = d.Span - n
		for i := 1; i < n; i++ {
			if gap := times[i] - times[i-1]; gap > d.MaxGap {
				d.MaxGap = gap
			}
		}
	} else {
		d.Span = 1
	}
	return d
}

// GlobalRange 返回所有标的合起来的时间跨度（最早的 FirstT、最晚的 LastT）。
//
// 放在这里而不是让调用方各算一遍：终端的「分析」和网页版都要这一行，
// 两处各写一份聚合逻辑，迟早会有一处忘了跳过没有时间信息的日文件
// （比如整份文件读不了、Bars=0 的那种），于是同一个数据目录在两个界面上
// 显示出不同的跨度 —— 这种不一致用户没法自查。
func (r *ScanResult) GlobalRange() (first, last int64, ok bool) {
	for _, f := range r.Folders {
		if !f.HasTime {
			continue
		}
		if !ok || f.FirstT < first {
			first = f.FirstT
		}
		if !ok || f.LastT > last {
			last = f.LastT
		}
		ok = true
	}
	return first, last, ok
}

// ByDate 把所有标的的数据按日期合并，得到「这一天一共多少根」。
func (r *ScanResult) ByDate() []DateTotal {
	type acc struct {
		syms   int
		bars   int
		issues int
	}
	m := map[string]*acc{}
	for _, f := range r.Folders {
		for _, d := range f.Days {
			if d.Bars == 0 {
				continue
			}
			a := m[d.Date]
			if a == nil {
				a = &acc{}
				m[d.Date] = a
			}
			a.syms++
			a.bars += d.Bars
			if d.Err != "" || d.Skipped > 0 || d.Dupes > 0 {
				a.issues++
			}
		}
	}
	out := make([]DateTotal, 0, len(m))
	for date, a := range m {
		out = append(out, DateTotal{Date: date, Symbols: a.syms, Bars: a.bars, Issues: a.issues})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

type DateTotal struct {
	Date    string
	Symbols int
	Bars    int
	Issues  int // 这一天有几个标的的文件带异常（坏行 / 重复行 / 读不了）
}

// Clock / Stamp 把时间戳按给定时区格式化成明文，供上层直接打印。
// 放这里是为了让「分析」和控制台看到的时间格式跟 CSV 里那一列完全一致。
func Clock(t int64, loc *time.Location) string {
	return time.Unix(t, 0).In(loc).Format("15:04")
}

func Stamp(t int64, loc *time.Location) string {
	return time.Unix(t, 0).In(loc).Format(timeLayout)
}
