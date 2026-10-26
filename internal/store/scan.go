package store

// 只读扫描：菜单里的「网页版分析」用它。
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
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Gap 是一段时间断档：本该有 K 线、实际上没有的那段区间。
//
// 两端取「第一根缺失 / 最后一根缺失」**应当出现**的时刻，而不是夹着它的那两根实有
// K 线的时刻 —— 用户要看的是「少了哪一段」，写 `05:00 → 06:05` 比写
// 「05:00 那根之后、06:10 那根之前」直接得多。
//
// EndT 由 StartT 加 (Bars-1) 个周期算出，不独立地从后一根减 —— 间隔不是周期整数倍的
// 脏数据下，两种算法会得出 EndT 早于 StartT 这种自相矛盾的行。
type Gap struct {
	StartT int64 // 缺失的第一根应当出现的时间
	EndT   int64 // 缺失的最后一根应当出现的时间
	Bars   int   // 缺了几根
}

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
	Gaps    []Gap // 时间断档（相邻两根之间大于一个周期的空档），按时间递增
	Expect  []int // 这一天按星期几应当有多少根；日期认不出来时为空
	BarsOff bool  // 根数不在 Expect 里（Expect 为空时不判）
	// Mismatch = 同一天别的标的的根数与它不同。它要等所有标的都扫完才知道，
	// 所以由 Scan 在最后回填，scanDay 里永远是 false。
	Mismatch bool
	Bytes    int64
	Err      string // 非空 = 这份文件没统计到（读不了）
}

// Anomaly 是「这一天不对劲」的判定：根数与该星期应有的根数不符，或文件本身有问题
// （有坏行 / 有重复行 / 整份读不了）。
//
// 判定写在 Go 里而不是前端：它依赖下面那张 Expect 表，前端再写一遍就是第二份口径，
// 而「界面上说 276 根正常、后端说异常」这种矛盾用户没法自查。
func (d DayStat) Anomaly() bool {
	return d.Err != "" || d.Skipped > 0 || d.Dupes > 0 || d.BarsOff
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
	// 下面两个是跨标的的聚合，和 Folders 里的逐日标记同源，只是给界面做个总数。
	BarsOff      int      // 根数与该星期应有根数不符的日文件数
	MismatchDays int      // 各标的根数不完全一致的天数
	Notes        []string // 异常情况（读不了之类的），由调用方原样打出来
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

	// 先把「要扫哪些文件」列出来（只有目录读失败才会在这里出错），
	// 再一次性并发扫完 —— 逐文件夹串行扫的时候，几乎全部时间都停在 syscall 上，
	// 一个核在等，另外几个核空转。
	type folderPlan struct {
		dir   string
		name  string
		start int // 在 jobs 里的起点
		n     int // 这份文件个数
		err   error
	}
	plans := make([]folderPlan, 0, len(entries))
	jobs := make([]dayJob, 0, 1024)
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		p := folderPlan{dir: filepath.Join(root, e.Name()), name: e.Name(), start: len(jobs)}
		names, sizes, err := listDayFiles(p.dir)
		if err != nil {
			p.err = err
			plans = append(plans, p)
			continue
		}
		p.n = len(names)
		for i, name := range names {
			jobs = append(jobs, dayJob{path: filepath.Join(p.dir, name), size: sizes[i]})
		}
		plans = append(plans, p)
	}

	stats := scanDays(jobs, loc, step)

	for _, p := range plans {
		fs := FolderStat{Folder: p.name}
		if p.err != nil {
			fs.Days = []DayStat{{Date: "(目录)", Path: p.dir, Err: fmt.Sprintf("读不了目录：%v", p.err)}}
			res.Folders = append(res.Folders, fs)
			continue
		}
		for i := p.start; i < p.start+p.n; i++ {
			d := stats[i]
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
	// 「同一天各标的的根数是不是一样」要等所有标的都扫完才知道，所以放在这里回填，
	// 而不是在 scanFolder / scanDay 里 —— 单份文件自己看不出这件事。
	res.MismatchDays = markMismatch(res.Folders)
	for _, f := range res.Folders {
		for _, d := range f.Days {
			if d.BarsOff {
				res.BarsOff++
			}
		}
	}
	sort.Slice(res.Folders, func(i, j int) bool { return res.Folders[i].Folder < res.Folders[j].Folder })
	return res
}

// markMismatch 给「当天根数与别的标的不一致」的日文件打标记，返回这样**天**有几个。
//
// 只比「当天有数据的标的」：某个标的当天根本没有这份文件（还没抓到、或这个标的当时
// 还没加进来），不能当成它抓了 0 根 —— 那会把「缺抓」误报成「抓多/抓少」，
// 而且用户以后每加一个新标的，它入库之前的每一天都会集体亮黄，日历上全是噪音。
func markMismatch(folders []FolderStat) int {
	byDate := map[string]map[int]bool{}
	for _, f := range folders {
		for _, d := range f.Days {
			if d.Bars == 0 {
				continue
			}
			if byDate[d.Date] == nil {
				byDate[d.Date] = map[int]bool{}
			}
			byDate[d.Date][d.Bars] = true
		}
	}
	days := 0
	for _, counts := range byDate {
		if len(counts) > 1 {
			days++
		}
	}
	for i := range folders {
		for j := range folders[i].Days {
			d := &folders[i].Days[j]
			if d.Bars > 0 && len(byDate[d.Date]) > 1 {
				d.Mismatch = true
			}
		}
	}
	return days
}

// dayJob 是「扫一份日文件」这件事：路径，加上从目录项里顺手捡到的大小。
//
// 大小从目录项来而不是再 os.Stat 一次：os.ReadDir 在 Windows 上本来就把
// WIN32_FIND_DATA 读回来了，Info() 是白拿的 —— 817 天 × 4 标的实测，
// 省掉这 3300 次 stat 就是省掉整次扫描里相当可观的一段 syscall。
type dayJob struct {
	path string
	size int64
}

// listDayFiles 列出一个标的目录下的日文件（文件名 + 大小）。
// 目录读不了时返回错误，由调用方决定怎么如实报出来。
func listDayFiles(dir string) ([]string, []int64, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	type named struct {
		name string
		size int64
	}
	list := make([]named, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(strings.ToLower(e.Name()), ".csv") {
			continue // .tmp 之类的中间产物不算数据
		}
		var size int64
		if fi, err := e.Info(); err == nil {
			size = fi.Size()
		}
		list = append(list, named{name: e.Name(), size: size})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name }) // 文件名就是日期，字符串序 = 时间序
	names := make([]string, len(list))
	sizes := make([]int64, len(list))
	for i, it := range list {
		names[i], sizes[i] = it.name, it.size
	}
	return names, sizes, nil
}

// scanDays 并发把一批日文件扫成 DayStat，结果按 job 的下标落位，所以顺序是稳定的
// （Folders 里的日文件永远是日期升序，跟串行扫出来的完全一样）。
func scanDays(jobs []dayJob, loc *time.Location, step int64) []DayStat {
	out := make([]DayStat, len(jobs))
	if len(jobs) == 0 {
		return out
	}
	workers := scanWorkers(len(jobs))
	var next int64
	var wg sync.WaitGroup
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			for {
				// 原子取号而不是开一条 channel：任务只有几千个，
				// 竞争一把 64 位计数器比 3300 次 channel 收发便宜。
				i := int(atomic.AddInt64(&next, 1)) - 1
				if i >= len(jobs) {
					return
				}
				out[i] = scanDay(jobs[i].path, jobs[i].size, loc, step)
			}
		}()
	}
	wg.Wait()
	return out
}

// scanWorkers 决定开几个 goroutine。
//
// 这一层是 I/O 密集（每份文件一次 open + 若干次 read），单核等 syscall 的时间里
// 别的核能干别的活，所以比核数多一点是划算的；但也不是越多越好 ——
// 冷盘上并发几十个读会让磁头来回跳，反而更慢。16 是实测的折中值。
func scanWorkers(files int) int {
	n := runtime.NumCPU() * 2
	if n < 4 {
		n = 4
	}
	if n > 16 {
		n = 16
	}
	if n > files {
		n = files
	}
	return n
}

func scanDay(path string, size int64, loc *time.Location, step int64) DayStat {
	d := DayStat{Date: strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)), Path: path}
	d.Bytes = size
	if d.Bytes == 0 {
		// 目录项里没拿到大小（极少见）才退回去 stat 一次
		if fi, err := os.Stat(path); err == nil {
			d.Bytes = fi.Size()
		}
	}
	bars, rs, err := readDayTimes(path, loc)
	d.Rows, d.Skipped = rs.Rows, rs.Bad
	d.Bars = len(bars)
	d.Dupes = rs.Good() - len(bars)
	if err != nil {
		d.Err = err.Error()
	}
	// 「应当有多少根」只跟这个日期是星期几有关，和文件读到几根无关，
	// 所以空文件 / 读不了的文件也照样判一次 —— 判据必须一致，否则「0 根的文件」会成为例外。
	d.Expect = ExpectedBars(d.Date, loc)
	if len(d.Expect) > 0 {
		d.BarsOff = !containsInt(d.Expect, d.Bars)
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
	d.Gaps = gapsBetween(times, step)

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

// gapsBetween 找出相邻两根之间大于一个周期的空档。
//
// 正常形态下一天只有一处（CME 每日结算休市 1 小时 = 12 根 5 分钟）；拖图时跳过了
// 可视范围会多出几处 —— 实测本机这份数据 817 天里最多 2 处。所以直接全列出来，
// 不做「只留最大的那处」这种取舍：报告要说清楚的是「缺在哪一刻到哪一刻」。
func gapsBetween(times []int64, step int64) []Gap {
	var out []Gap
	for i := 1; i < len(times); i++ {
		diff := times[i] - times[i-1]
		if diff <= step {
			continue
		}
		n := int(diff/step) - 1
		if n <= 0 {
			// 间隔不是周期的整数倍（脏数据）：报出来，但至少要有一根 ——
			// 否则会出现「断档 0 根」这种自相矛盾的行。
			n = 1
		}
		out = append(out, Gap{
			StartT: times[i-1] + step,
			EndT:   times[i-1] + int64(n)*step,
			Bars:   n,
		})
	}
	return out
}

func containsInt(xs []int, v int) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}

// WeekExpect 是判定表里的一行：星期几、以及「正常应当是几根」的全部取值。
type WeekExpect struct {
	Weekday string // 周一…周日
	Bars    []int  // 夏令时/冬令时可能给出两个取值
}

// expectRule 是「这一天应当有多少根 K 线」的判定表，索引就是 time.Weekday。
//
// 依据是 CME Globex 的作息换算到本地时区（本程序按 time.Local 划分「哪一天」，
// 见 store 顶部的说明）：
//
//	每日 16:00 CT 结算休市 1 小时；周五 16:00 CT 收市、周日 17:00 CT 开市。
//
// 美国夏令时/冬令时会让这些时刻在本地整体挪 1 小时，**于是同一个星期几会有两个正常值**：
//
//	周一        18 小时 = 216 根（夏令时） / 17 小时 = 204 根（冬令时）
//	周二~周五   5+18 或 6+17 = 都是 23 小时 = 276 根（两种时令同值）
//	周六        5 小时 = 60 根（夏令时） / 6 小时 = 72 根（冬令时）
//	周日        休市，正常一根都没有
//
// 半年切一次时令，所以固定一个值会在切换后的那半年里天天误报。实测本机这份数据
// （2024-03 起 817 天）：周一的 204 根全部落在冬令时、216 根全部落在夏令时；
// 周六的 60/72 同理 —— 这正是「周一要接受两个值」的实测依据。
//
// 交易所的临时休市（美国节假日提前收市、某位要人葬礼休市之类）会给出第三个值，
// 那些**就是要报出来**的：用户正是想看到「这一天和平时不一样」。
var expectRule = [7]WeekExpect{
	time.Monday:    {Weekday: "周一", Bars: []int{216, 204}},
	time.Tuesday:   {Weekday: "周二", Bars: []int{276}},
	time.Wednesday: {Weekday: "周三", Bars: []int{276}},
	time.Thursday:  {Weekday: "周四", Bars: []int{276}},
	time.Friday:    {Weekday: "周五", Bars: []int{276}},
	time.Saturday:  {Weekday: "周六", Bars: []int{72, 60}},
	time.Sunday:    {Weekday: "周日", Bars: []int{0}},
}

// ExpectedBars 给出某个日期按上表应当有多少根。日期解析不出来时返回 nil（不判）。
func ExpectedBars(date string, loc *time.Location) []int {
	if loc == nil {
		loc = time.Local
	}
	t, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return nil
	}
	return expectRule[t.Weekday()].Bars
}

// ExpectedRule 把判定表原样交给界面印出来，**按中文习惯从周一开始排**
// （内部那张表是按 time.Weekday 索引的，周日是 0）。
//
// 存在的理由：这张表要在网页的「统计口径」里列一遍。让 HTML 再抄一份数字，
// 改了表就会漏改那边 —— 而「页面上写着周一 216、程序按 204 判」这种错没人能发现。
func ExpectedRule() []WeekExpect {
	out := make([]WeekExpect, 0, len(expectRule))
	for i := 1; i <= 7; i++ {
		r := expectRule[i%7]
		out = append(out, WeekExpect{Weekday: r.Weekday, Bars: append([]int{}, r.Bars...)})
	}
	return out
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
		counts map[int]int // 根数 -> 有几个标的
	}
	m := map[string]*acc{}
	for _, f := range r.Folders {
		for _, d := range f.Days {
			if d.Bars == 0 {
				continue
			}
			a := m[d.Date]
			if a == nil {
				a = &acc{counts: map[int]int{}}
				m[d.Date] = a
			}
			a.syms++
			a.bars += d.Bars
			if d.Anomaly() {
				a.issues++
			}
			a.counts[d.Bars]++
		}
	}
	out := make([]DateTotal, 0, len(m))
	for date, a := range m {
		t := DateTotal{Date: date, Symbols: a.syms, Bars: a.bars, Issues: a.issues}
		for bars, n := range a.counts {
			t.Counts = append(t.Counts, BarCount{Bars: bars, Symbols: n})
		}
		// 个数多的排前面，并列时根数大的排前面 —— 首项就是「多数派」，
		// 明细表用它把少数派那几行标出来（这个判断不该留给前端重算）。
		sort.Slice(t.Counts, func(i, j int) bool {
			if t.Counts[i].Symbols != t.Counts[j].Symbols {
				return t.Counts[i].Symbols > t.Counts[j].Symbols
			}
			return t.Counts[i].Bars > t.Counts[j].Bars
		})
		t.Majority = t.Counts[0].Bars
		t.Mismatch = len(t.Counts) > 1
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date < out[j].Date })
	return out
}

type DateTotal struct {
	Date     string
	Symbols  int
	Bars     int
	Issues   int        // 这一天有几个标的的文件带异常（根数不符 / 坏行 / 重复行 / 读不了）
	Mismatch bool       // 这一天的根数在各标的之间不完全一致
	Majority int        // 出现最多的那个根数（不一致时用它挑出「少数派」）
	Counts   []BarCount // 各种根数各有几个标的（Mismatch 时明细表要显示）
}

// BarCount 是「这一天有 N 个标的各抓了 Bars 根」。
type BarCount struct {
	Bars    int
	Symbols int
}

// Clock / Stamp 把时间戳按给定时区格式化成明文，供上层直接打印。
// 放这里是为了让「分析」和控制台看到的时间格式跟 CSV 里那一列完全一致。
func Clock(t int64, loc *time.Location) string {
	return time.Unix(t, 0).In(loc).Format("15:04")
}

func Stamp(t int64, loc *time.Location) string {
	return time.Unix(t, 0).In(loc).Format(timeLayout)
}
