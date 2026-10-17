package store

// 供「复盘」使用的只读读取：与落盘共享同一份 CSV 解析，但**保留时间列的原始文本**。
//
// 复盘的协议里，一根 K 线的唯一标识就是 CSV 里那一列 `yyyy-MM-dd HH:MM` 的字符串
// （用户明确要求「用 time 时间字符串做唯一标识，不用时间戳、不考虑时区」）。
// 已有的 readDayFile 只返回 int64 时间戳，拿不到原文，所以这里补一层：
// 解析还是 parseTimeRow 这一份，时间字符串和时间戳同时给出来，两边不会漂移。
//
// 这一路径同样**只读**：不建目录、不写索引、不改文件。

import (
	"encoding/csv"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// TimeBar 是 CSV 的一行：时间保持明文，同时带上解析出来的时间戳（排序用）。
type TimeBar struct {
	Time      string  // 原始文本，形如 "2026-10-16 12:00"
	T         int64   // 解析后的 Unix 秒（只用于排序/比较，不进协议）
	O, H, L, C float64
	V         float64
	HasVolume bool
}

// CatalogEntry 是「复盘选标的」用的名录项。
//
// 只列目录名和文件名里的信息（天数、首末日），不读文件内容 ——
// 一个标的有 800 多个 CSV，为了列个下拉框把它们全读一遍是不可接受的。
type CatalogEntry struct {
	Symbol    string // _symbols.json 里登记的标的；没登记就用文件夹名
	Folder    string
	Days      int
	FirstDate string // 最早一天（文件名）
	LastDate  string // 最晚一天（文件名）
}

// readDayRows 把一份日文件读成按时间升序的行，保留时间文本。
// 坏行照旧跳过并计数，不因为一行坏行丢掉整份文件。
func readDayRows(path string, loc *time.Location) ([]TimeBar, RowStat, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, RowStat{}, err
	}
	defer f.Close()

	var rows []TimeBar
	var rs RowStat
	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	firstRow := true
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return rows, rs, err
		}
		if firstRow {
			firstRow = false
			if len(rec) > 0 && strings.EqualFold(strings.TrimSpace(rec[0]), "time") {
				continue
			}
		}
		rs.Rows++
		tb, ok := parseTimeRow(rec, loc)
		if !ok {
			rs.Bad++
			continue
		}
		rows = append(rows, tb)
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].T < rows[j].T })
	return rows, rs, nil
}

// ReadDayRows 是 readDayRows 的导出形式，供复盘后端按天加载 K 线。
func ReadDayRows(path string, loc *time.Location) ([]TimeBar, RowStat, error) {
	return readDayRows(path, loc)
}

// DayFiles 返回一个标的目录下的 CSV 文件名（升序 = 日期升序），不含子目录和中间产物。
func DayFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".csv") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	return names, nil
}

// Catalog 列出数据目录里所有带 CSV 的标的。
//
// 标的名只认 _symbols.json（和 Scan 一致）：索引读不到就退回文件夹名，
// 因为目录名本来就是标的 slug 化来的，两者通常长得一样。
func Catalog(root string) ([]CatalogEntry, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	symbolOf := map[string]string{}
	if idx, err := readIndexFile(root); err == nil {
		for sym, rec := range idx.Symbols {
			if rec.Folder != "" {
				symbolOf[rec.Folder] = sym
			}
		}
	}
	out := make([]CatalogEntry, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), "_") {
			continue
		}
		files, err := DayFiles(filepath.Join(root, e.Name()))
		if err != nil || len(files) == 0 {
			continue // 空目录不占版面
		}
		sym, ok := symbolOf[e.Name()]
		if !ok {
			sym = e.Name()
		}
		out = append(out, CatalogEntry{
			Symbol:    sym,
			Folder:    e.Name(),
			Days:      len(files),
			FirstDate: strings.TrimSuffix(files[0], filepath.Ext(files[0])),
			LastDate:  strings.TrimSuffix(files[len(files)-1], filepath.Ext(files[len(files)-1])),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Folder < out[j].Folder })
	return out, nil
}
