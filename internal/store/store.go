// Package store 把 K 线落成 CSV：<outDir>/<标的>/<yyyy-mm-dd>.csv
//
// 三个刻意的设计：
//
//  1. **一个标的一个文件夹，一天一个文件。** 文件名就是日期，翻数据不用查库，
//     也不用先把一个几十万行的大文件读进来再过滤。
//
//  2. **时间是明文（yyyy-MM-dd HH:MM），不是 Unix 秒。** 用文本编辑器/Excel 打开
//     就能看懂，代价是解析时要按时区还原成时间戳 —— 所以时区是这里最敏感的一个参数。
//
//  3. **写盘 = 用内存里的当日全量重写整个文件（tmp + rename），不做「是否存在」判断。**
//     同一根 K 线会被反复推更新（正在走的那根 du 增量），逐行 append 会堆出一串重复，
//     逐行改又要先扫全文件定位。既然一天只有 288 行（5 分钟级别），
//     直接按「时间戳 -> 最后一版」聚合后整体覆盖，重复问题从结构上就不存在了。
//     这也是需求里说的「写入直接覆盖，不考虑是否已存在相同时间的数据」。
package store

import (
	"bytes"
	"crypto/sha1"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	dateLayout = "2006-01-02"
	timeLayout = "2006-01-02 15:04"
	indexFile  = "_symbols.json"
)

// Bar 是落盘用的最小 K 线（不带 series/method，那些在 CSV 里没有位置也没有读者）
type Bar struct {
	T          int64
	O, H, L, C float64
	V          float64
	HasVolume  bool
}

type Options struct {
	Root     string         // outDir
	Header   bool           // 是否写表头
	Location *time.Location // 决定「哪一天」和明文时间；空则用本机时区
	Log      func(format string, args ...any)
}

type Store struct {
	opts Options
	loc  *time.Location

	mu       sync.Mutex
	days     map[string]*day
	folders  map[string]string // symbol -> folder
	taken    map[string]string // lower(folder) -> symbol
	accepted map[string]int64  // folder -> Add 调用次数（含覆盖）
	idxDirty bool
}

type day struct {
	path  string
	dir   string
	bars  map[int64]Bar
	dirty bool
	skip  int // 载入时跳过的坏行数
}

func Open(opts Options) (*Store, error) {
	if opts.Root == "" {
		return nil, fmt.Errorf("store: 未指定数据目录")
	}
	loc := opts.Location
	if loc == nil {
		loc = time.Local
	}
	s := &Store{
		opts:     opts,
		loc:      loc,
		days:     map[string]*day{},
		folders:  map[string]string{},
		taken:    map[string]string{},
		accepted: map[string]int64{},
	}
	if err := os.MkdirAll(opts.Root, 0o755); err != nil {
		return nil, fmt.Errorf("创建数据目录 %s 失败: %w", opts.Root, err)
	}
	s.loadIndex()
	return s, nil
}

func (s *Store) logf(format string, args ...any) {
	if s.opts.Log != nil {
		s.opts.Log(format, args...)
	}
}

// ---------------- 标的 -> 文件夹 ----------------

type symRec struct {
	Folder  string `json:"folder"`
	Created int64  `json:"created,omitempty"`
}

type symbolIndex struct {
	Version int               `json:"version"`
	Symbols map[string]symRec `json:"symbols"`
}

func (s *Store) indexPath() string { return filepath.Join(s.opts.Root, indexFile) }

func (s *Store) loadIndex() {
	raw, err := os.ReadFile(s.indexPath())
	if err == nil {
		var idx symbolIndex
		if json.Unmarshal(raw, &idx) == nil && idx.Symbols != nil {
			for sym, rec := range idx.Symbols {
				if rec.Folder == "" {
					continue
				}
				s.folders[sym] = rec.Folder
				s.taken[strings.ToLower(rec.Folder)] = sym
			}
			return
		}
		s.logf("[store] %s 解析失败，按空索引继续（不会覆盖已有文件夹，只会新分配）", indexFile)
	}
	// 没有索引时，把磁盘上已有的目录先认领成「符号名 = 目录名」，
	// 否则一个手工拷进来的目录可能被当成新标的、再分配一个名字相近的目录。
	entries, err := os.ReadDir(s.opts.Root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		s.folders[e.Name()] = e.Name()
		s.taken[strings.ToLower(e.Name())] = e.Name()
	}
}

// Folder 返回（必要时分配）某个标的应用的文件夹名。
//
// 撞名只会发生在大小写归一后相同、或 slug 化后相同的情况下（Windows 文件名不区分大小写）。
// 一旦撞上就追加短哈希 —— 绝不「猜一个相近的名字」，那会让两个标的的数据混进同一个目录。
func (s *Store) Folder(symbol string) string {
	sym := strings.TrimSpace(symbol)
	if sym == "" {
		sym = "(unknown)"
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if f, ok := s.folders[sym]; ok {
		return f
	}
	base := slug(sym)
	folder := base
	if owner, used := s.taken[strings.ToLower(folder)]; used && owner != sym {
		folder = base + "~" + shortHash(sym)
		for n := 2; ; n++ {
			if owner, used := s.taken[strings.ToLower(folder)]; !used || owner == sym {
				break
			}
			folder = fmt.Sprintf("%s~%s-%d", base, shortHash(sym), n)
		}
	}
	s.folders[sym] = folder
	s.taken[strings.ToLower(folder)] = sym
	s.idxDirty = true
	s.logf("[store] 新标的 %s -> %s/", sym, folder)
	return folder
}

// slug 只做文件系统需要的最小替换，不做拼音/转写：
// CME_MINI:MNQ1! -> CME_MINI_MNQ1!
func slug(symbol string) string {
	var b strings.Builder
	for _, r := range symbol {
		switch {
		case r < 0x20 || r == 0x7f:
			b.WriteRune('_')
		case strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.TrimRight(b.String(), ". ")
	out = strings.TrimSpace(out)
	if out == "" {
		return "unknown"
	}
	return out
}

func shortHash(s string) string {
	sum := sha1.Sum([]byte(s))
	return hex.EncodeToString(sum[:])[:6]
}

func (s *Store) saveIndexLocked() error {
	idx := symbolIndex{Version: 1, Symbols: map[string]symRec{}}
	for sym, folder := range s.folders {
		idx.Symbols[sym] = symRec{Folder: folder}
	}
	buf, err := json.MarshalIndent(idx, "", "  ")
	if err != nil {
		return err
	}
	path := s.indexPath()
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(buf, '\n'), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	s.idxDirty = false
	return nil
}

// ---------------- 写入 ----------------

// Add 记下一根 K 线。同一时间戳再写就是覆盖 —— 正在走的那根会被反复更新，
// 这是正常现象而不是异常，所以这里不做任何「已存在」判断。
func (s *Store) Add(symbol string, b Bar) {
	folder := s.Folder(symbol)
	s.mu.Lock()
	defer s.mu.Unlock()

	t := time.Unix(b.T, 0).In(s.loc)
	date := t.Format(dateLayout)
	key := folder + "/" + date
	d, ok := s.days[key]
	if !ok {
		d = s.loadDayLocked(folder, date)
		s.days[key] = d
	}
	d.bars[b.T] = b
	d.dirty = true
	s.accepted[folder]++
}

func (s *Store) dayPath(folder, date string) string {
	return filepath.Join(s.opts.Root, folder, date+".csv")
}

// loadDayLocked 把磁盘上已有的当日文件读进内存。
// 必须读：程序重启后用户接着拖图表，同一个日文件会被重新碰，
// 不先载入就等于把之前抓的几天数据整体覆盖掉。
func (s *Store) loadDayLocked(folder, date string) *day {
	d := &day{path: s.dayPath(folder, date), dir: filepath.Dir(s.dayPath(folder, date)), bars: map[int64]Bar{}}
	f, err := os.Open(d.path)
	if err != nil {
		if !os.IsNotExist(err) {
			s.logf("[store] 读不了 %s: %v", d.path, err)
		}
		return d
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1 // 容忍列数不一致的坏行，逐行跳过而不是整文件失败
	r.ReuseRecord = false
	firstRow := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			s.logf("[store] %s 读取中断: %v", d.path, err)
			break
		}
		if firstRow {
			firstRow = false
			if len(rec) > 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "time") {
				continue
			}
		}
		if len(rec) < 5 {
			d.skip++
			continue
		}
		ts, err := time.ParseInLocation(timeLayout, strings.TrimSpace(rec[0]), s.loc)
		if err != nil {
			d.skip++
			continue
		}
		// 前 4 列（OHLC）必须都能解析；第 5 列成交量允许为空 ——
		// 有些品种的帧里没有成交量，写出来就是一列空值，读回来不能因此把整行丢掉。
		vals := make([]float64, 0, 5)
		bad := false
		for i := 1; i <= 5 && i < len(rec); i++ {
			raw := strings.TrimSpace(rec[i])
			if raw == "" {
				if i == 5 {
					break
				}
				bad = true
				break
			}
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				bad = true
				break
			}
			vals = append(vals, f)
		}
		if bad || len(vals) < 4 {
			d.skip++
			continue
		}
		b := Bar{T: ts.Unix(), O: vals[0], H: vals[1], L: vals[2], C: vals[3]}
		if len(vals) == 5 {
			b.V, b.HasVolume = vals[4], true
		}
		d.bars[b.T] = b
	}
	if d.skip > 0 {
		s.logf("[store] %s 有 %d 行无法解析（已跳过，不影响其它行）", filepath.Base(d.path), d.skip)
	}
	return d
}

// Flush 把所有脏的日文件重写一遍。
// 序列化在锁内做（拿到一致快照），写盘在锁外做（不阻塞收帧）。
func (s *Store) Flush() error {
	type pending struct {
		path string
		data []byte
	}
	var list []pending
	var keys []string

	s.mu.Lock()
	if s.idxDirty {
		if err := s.saveIndexLocked(); err != nil {
			s.logf("[store] 写 %s 失败: %v", indexFile, err)
		}
	}
	for key, d := range s.days {
		if !d.dirty {
			continue
		}
		data, err := s.encodeDayLocked(d)
		if err != nil {
			s.logf("[store] 序列化 %s 失败: %v", d.path, err)
			continue
		}
		list = append(list, pending{path: d.path, data: data})
		keys = append(keys, key)
		d.dirty = false
	}
	s.mu.Unlock()

	var lastErr error
	for i, p := range list {
		if err := writeFileAtomic(p.path, p.data); err != nil {
			lastErr = err
			s.logf("[store] 写 %s 失败: %v（下个周期重试）", p.path, err)
			s.mu.Lock()
			if d, ok := s.days[keys[i]]; ok {
				d.dirty = true
			}
			s.mu.Unlock()
		}
	}
	return lastErr
}

func (s *Store) encodeDayLocked(d *day) ([]byte, error) {
	times := make([]int64, 0, len(d.bars))
	for t := range d.bars {
		times = append(times, t)
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.UseCRLF = false
	if s.opts.Header {
		if err := w.Write([]string{"time", "open", "high", "low", "close", "volume"}); err != nil {
			return nil, err
		}
	}
	rec := make([]string, 6)
	for _, t := range times {
		b := d.bars[t]
		rec[0] = time.Unix(t, 0).In(s.loc).Format(timeLayout)
		rec[1] = num(b.O)
		rec[2] = num(b.H)
		rec[3] = num(b.L)
		rec[4] = num(b.C)
		if b.HasVolume {
			rec[5] = num(b.V)
		} else {
			rec[5] = ""
		}
		if err := w.Write(rec); err != nil {
			return nil, err
		}
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// num 用最短表示：30067.25 -> "30067.25"，843.0 -> "843"。
// 期货价格是 0.25 的整数倍，这样写出来和交易软件里看到的一致。
func num(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	// Windows 上 os.Rename 会覆盖已存在的目标文件；若目标被 Excel 打开则会失败，
	// 这时保持「脏」状态，下个周期重试。
	return os.Rename(tmp, path)
}

// ---------------- 统计 ----------------

type SymbolStat struct {
	Symbol    string
	Folder    string
	Days      int
	Bars      int64 // 内存里当前持有的不同时间戳数（= 重写后文件里的行数）
	Accepted  int64 // Add 被调用的次数（含对正在走的那根的重复覆盖）
	FirstTime string
	LastTime  string
}

// Stats 返回本次会话碰过的每个标的的当前状态。
// Bars 是「内存里这一份 = 磁盘上那一份」的条数，不是累计写入次数 ——
// 累计次数会把正在走的那根 K 线的反复更新算成很多根，是虚高的。
func (s *Store) Stats() []SymbolStat {
	s.mu.Lock()
	defer s.mu.Unlock()

	byFolder := map[string]*SymbolStat{}
	first := map[string]int64{}
	last := map[string]int64{}
	for _, d := range s.days {
		folder := filepath.Base(d.dir)
		st := byFolder[folder]
		if st == nil {
			st = &SymbolStat{Folder: folder}
			byFolder[folder] = st
		}
		st.Days++
		st.Bars += int64(len(d.bars))
		for t := range d.bars {
			if cur, ok := first[folder]; !ok || t < cur {
				first[folder] = t
			}
			if cur, ok := last[folder]; !ok || t > cur {
				last[folder] = t
			}
		}
	}
	for sym, folder := range s.folders {
		st := byFolder[folder]
		if st == nil {
			byFolder[folder] = &SymbolStat{Symbol: sym, Folder: folder}
			continue
		}
		st.Symbol = sym
	}
	out := make([]SymbolStat, 0, len(byFolder))
	for _, st := range byFolder {
		if st.Symbol == "" {
			st.Symbol = st.Folder
		}
		st.Accepted = s.accepted[st.Folder]
		if t, ok := first[st.Folder]; ok {
			st.FirstTime = time.Unix(t, 0).In(s.loc).Format(timeLayout)
		}
		if t, ok := last[st.Folder]; ok {
			st.LastTime = time.Unix(t, 0).In(s.loc).Format(timeLayout)
		}
		if st.Bars == 0 {
			continue // 只分配过文件夹、没写进任何数据的不报
		}
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Folder < out[j].Folder })
	return out
}

func (s *Store) Close() error {
	return s.Flush()
}
