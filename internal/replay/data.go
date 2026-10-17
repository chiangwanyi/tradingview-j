package replay

// 把数据目录里的 CSV 变成「一个可步进的实例」。
//
// 加载策略（为什么不一次读全）：一个标的有 800 多个日文件、90 多万根 K 线，
// 全部读进内存要几十 MB，而一次复盘只会看锚点附近那几百根。所以：
//
//   1. 锚点那天**整日**收进来，游标停在锚点上（锚点之后的部分是「已加载但没揭示」）；
//   2. 以锚点为中心**向前**收集，直到已揭示的根数够 bars 根（默认 400）作为可见历史；
//   3. **向后**预加载 forwardDays 天（默认 5）作为「已加载但没揭示」的部分；
//   4. 游标走到预加载末尾时，再按日文件顺序续读（ensureFuture），一次最多补几天。
//
// 第 1 条是 0.5.1 修的 bug：以前锚点那天只收到锚点为止（rows[:rowIdx+1]），
// 于是「下一根」会直接跳到第二天 00:00 —— 锚点当天 12:00 之后的几百根全被跳过。
//
// 随机锚点会避开首尾各一天（首日没有历史、尾日没有未来），这样「随机加载某个时间点
// 及之前的 K 线」才真的能步进起来。

import (
	"errors"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"tradingview-j/internal/store"
)

const (
	DefaultHistory     = 400
	DefaultForwardDays = 5
	MaxHistory         = 5000
	MaxForwardDays     = 60
)

// DataSource 只读地访问数据目录。
type DataSource struct {
	Root string
	Loc  *time.Location
}

func NewDataSource(root string, loc *time.Location) *DataSource {
	if loc == nil {
		loc = time.Local
	}
	return &DataSource{Root: root, Loc: loc}
}

// Catalog 列出可选的标的。
func (ds *DataSource) Catalog() ([]SymbolInfo, error) {
	entries, err := store.Catalog(ds.Root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("数据目录不存在：%s（先用菜单 1 抓点数据）", ds.Root)
		}
		return nil, fmt.Errorf("读不了数据目录 %s：%w", ds.Root, err)
	}
	out := make([]SymbolInfo, 0, len(entries))
	for _, e := range entries {
		out = append(out, SymbolInfo{
			Symbol:    e.Symbol,
			Folder:    e.Folder,
			Days:      e.Days,
			FirstDate: e.FirstDate,
			LastDate:  e.LastDate,
			Tick:      e.Tick,
		})
	}
	return out, nil
}

// resolveFolder 把「标的」或「文件夹名」解析成实际的目录名。
func (ds *DataSource) resolveFolder(symbol string) (SymbolInfo, error) {
	symbol = strings.TrimSpace(symbol)
	if symbol == "" {
		return SymbolInfo{}, errors.New("没有选择标的")
	}
	list, err := ds.Catalog()
	if err != nil {
		return SymbolInfo{}, err
	}
	for _, e := range list {
		if e.Symbol == symbol || e.Folder == symbol || strings.EqualFold(e.Folder, symbol) {
			return e, nil
		}
	}
	return SymbolInfo{}, fmt.Errorf("数据目录里没有标的 %q（先用菜单 1 抓过它）", symbol)
}

// Open 按「标的 + 时间锚点」准备一个实例（不含 ID 与创建者，那些由 hub 填）。
//
// anchorTime 为空时随机挑一个时间点：挑日期 -> 挑那天的某一根 -> 以它为止。
// rnd 传 nil 就用当前时间做种子；传固定种子可复现（测试与排查用）。
func (ds *DataSource) Open(symbol, anchorTime string, history, forwardDays int, rnd *rand.Rand) (*Instance, error) {
	if history <= 0 {
		history = DefaultHistory
	}
	if history > MaxHistory {
		history = MaxHistory
	}
	if forwardDays <= 0 {
		forwardDays = DefaultForwardDays
	}
	if forwardDays > MaxForwardDays {
		forwardDays = MaxForwardDays
	}
	if rnd == nil {
		rnd = rand.New(rand.NewSource(time.Now().UnixNano()))
	}

	info, err := ds.resolveFolder(symbol)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(ds.Root, info.Folder)
	files, err := store.DayFiles(dir)
	if err != nil {
		return nil, fmt.Errorf("读不了 %s 的目录：%w", info.Folder, err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s 目录里没有 CSV", info.Folder)
	}

	inst := &Instance{
		Symbol:   info.Symbol,
		Folder:   info.Folder,
		Tick:     info.Tick,
		dayFiles: files,
	}
	var notes []string

	dayIdx, rowIdx, anchorRow, err := ds.pickAnchor(dir, files, anchorTime, rnd, &notes)
	if err != nil {
		return nil, err
	}

	// 从前向后攒历史：锚点日**整日**先放进来（锚点之后的那些根留在内存里、不进快照），
	// 再往前面加整日，直到**已揭示**的根数够 bars 根。
	//
	// 这里的「整日」不能省：以前只放到锚点为止，步进时 Bars[Cursor+1] 就变成了第二天日文件的
	// 第一根（00:00），锚点当天剩下的几十上百根全被跳过 —— 用户点「下一根」看到的是隔夜的
	// 00:00，而不是真正的下一根。所以判「够不够 history」也要按已揭示根数（revealed）算，
	// 不能按 len(bars) 算：锚点靠凌晨时整日那两百多根大多还没揭示，按 len 算会提前停止往前补。
	rows := anchorRow
	bars := make([]Bar, 0, history+8+len(rows))
	bars = append(bars, rowsToBars(rows)...)
	revealed := rowIdx + 1
	for d := dayIdx - 1; d >= 0 && revealed < history; d-- {
		prev, rs, err := store.ReadDayRows(filepath.Join(dir, files[d]), ds.Loc)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s 读不了（%v）", files[d], err))
			continue
		}
		if rs.Bad > 0 {
			notes = append(notes, fmt.Sprintf("%s 有 %d 行解析不了（已跳过）", files[d], rs.Bad))
		}
		if len(prev) == 0 {
			continue
		}
		bars = append(rowsToBars(prev), bars...)
		revealed += len(prev)
	}
	cursorTime := rows[rowIdx].Time

	// 向后预加载几天：这些根进内存但不进快照，直到游标走过去。
	for d := dayIdx + 1; d < len(files) && d <= dayIdx+forwardDays; d++ {
		next, rs, err := store.ReadDayRows(filepath.Join(dir, files[d]), ds.Loc)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s 读不了（%v）", files[d], err))
			continue
		}
		if rs.Bad > 0 {
			notes = append(notes, fmt.Sprintf("%s 有 %d 行解析不了（已跳过）", files[d], rs.Bad))
		}
		bars = append(bars, rowsToBars(next)...)
	}
	inst.nextDay = minInt(dayIdx+forwardDays+1, len(files))

	// 同一根 K 线可能出现两次（日文件边界被手工改过）：按时间去掉后来的那份，
	// 保证 time 在实例里是**唯一**的 —— 协议就用它当标识。
	bars = dedupeBars(bars)
	computeEMA(bars)

	cursor := 0
	if idx, ok := indexOfTimeIn(bars, cursorTime); ok {
		cursor = idx
	} else {
		// 去重/边界处理把它弄丢了（理论上不会发生）。退回最后一根，并如实说一声。
		cursor = len(bars) - 1
		notes = append(notes, "锚点那根 K 线在合并日文件时丢了，已退回最早可用位置之后的最后一根")
	}

	inst.Bars = bars
	inst.Cursor = cursor
	inst.Anchor = bars[cursor].Time
	inst.notes = notes
	return inst, nil
}

// pickAnchor 决定锚点落在哪一天、那一天的哪一根。
func (ds *DataSource) pickAnchor(dir string, files []string, anchorTime string, rnd *rand.Rand, notes *[]string) (dayIdx, rowIdx int, rows []store.TimeBar, err error) {
	if anchorTime != "" {
		date, ok := anchorDate(anchorTime)
		if !ok {
			return 0, 0, nil, fmt.Errorf("时间点 %q 格式不对，要形如 2026-10-16 12:00", anchorTime)
		}
		dayIdx = indexOfFile(files, date+".csv")
		if dayIdx < 0 {
			return 0, 0, nil, fmt.Errorf("%s 那天没有 %s 的数据", date, filepath.Base(dir))
		}
		rows, rs, err := store.ReadDayRows(filepath.Join(dir, files[dayIdx]), ds.Loc)
		if err != nil {
			return 0, 0, nil, fmt.Errorf("读不了 %s：%w", files[dayIdx], err)
		}
		if rs.Bad > 0 {
			*notes = append(*notes, fmt.Sprintf("%s 有 %d 行解析不了（已跳过）", files[dayIdx], rs.Bad))
		}
		for i := range rows {
			if rows[i].Time == anchorTime {
				return dayIdx, i, rows, nil
			}
		}
		return 0, 0, nil, fmt.Errorf("时间点 %s 不在 %s 里（可能那根还没抓到，或分钟数不是 5 的倍数）", anchorTime, files[dayIdx])
	}

	// 随机：避开首尾各一天。数据只有一两天时退化成任意一天。
	lo, hi := 1, len(files)-2
	if hi < lo {
		lo, hi = 0, len(files)-1
	}
	dayIdx = lo + rnd.Intn(hi-lo+1)
	for attempt := 0; attempt < 30; attempt++ {
		rows, rs, readErr := store.ReadDayRows(filepath.Join(dir, files[dayIdx]), ds.Loc)
		if readErr != nil {
			*notes = append(*notes, fmt.Sprintf("%s 读不了（%v）", files[dayIdx], readErr))
		} else if len(rows) > 0 {
			if rs.Bad > 0 {
				*notes = append(*notes, fmt.Sprintf("%s 有 %d 行解析不了（已跳过）", files[dayIdx], rs.Bad))
			}
			return dayIdx, rnd.Intn(len(rows)), rows, nil
		}
		// 空文件（例如周日）或不存在的日子：往后找，不往前 —— 往前会让「随机」偏早。
		dayIdx++
		if dayIdx >= len(files) {
			dayIdx = 0
		}
	}
	return 0, 0, nil, errors.New("随机挑时间点失败：连续 30 个日文件都是空的")
}

// ensureFuture 在游标走到已加载数据末尾时，顺序续读后面的日文件。
//
// 空文件（周日、临时休市）会被跳过，最多连续看 40 个文件 —— 不能因为一个长假
// 就把「没有更多数据」误报出来，也不能无上限地在一个步骤里读完几百个文件。
func (inst *Instance) ensureFuture(ds *DataSource) error {
	if inst.Cursor+1 < len(inst.Bars) {
		return nil
	}
	added := 0
	for guard := 0; guard < 40 && inst.nextDay < len(inst.dayFiles) && added == 0; guard++ {
		name := inst.dayFiles[inst.nextDay]
		inst.nextDay++
		rows, rs, err := store.ReadDayRows(filepath.Join(ds.Root, inst.Folder, name), ds.Loc)
		if err != nil {
			inst.notes = append(inst.notes, fmt.Sprintf("%s 读不了（%v）", name, err))
			continue
		}
		if rs.Bad > 0 {
			inst.notes = append(inst.notes, fmt.Sprintf("%s 有 %d 行解析不了（已跳过）", name, rs.Bad))
		}
		if len(rows) == 0 {
			continue
		}
		next := make([]Bar, 0, len(rows))
		for _, r := range rows {
			next = append(next, toBar(r))
		}
		// 日文件之间理论上不会重叠；真重叠了就去重（以后来的为准，与落盘的覆盖语义一致）。
		for _, b := range next {
			if idx, ok := indexOfTimeIn(inst.Bars, b.Time); ok {
				inst.Bars[idx] = b
				continue
			}
			inst.Bars = append(inst.Bars, b)
		}
		computeEMA(inst.Bars)
		added = len(next)
	}
	if inst.Cursor+1 >= len(inst.Bars) {
		return ErrNoMoreBars
	}
	return nil
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

// toBar 把 CSV 的一行变成协议里的一根 K 线。
//
// **价格在这里四舍五入到两位小数**：CSV 里会出现 89.97000000000001 这种浮点噪声，
// 原样下发会让价格轴、十字光标提示、下单框里冒出一串尾巴。成交量不处理（它本来就是整数）。
func toBar(r store.TimeBar) Bar {
	return Bar{
		Time:   r.Time,
		Open:   roundPrice(r.O),
		High:   roundPrice(r.H),
		Low:    roundPrice(r.L),
		Close:  roundPrice(r.C),
		Volume: r.V,
	}
}

func rowsToBars(rows []store.TimeBar) []Bar {
	out := make([]Bar, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBar(r))
	}
	return out
}

// computeEMA 算 EMA20：前 20 根用简单平均做种子，之后按 2/(20+1) 递推。
// 前 19 根留 nil（JSON 里是 null），前端据此从第 20 根开始连线。
//
// 放在后端算：指标口径只有一份，Terminal / 网页 / 以后别的界面不会各算一套。
func computeEMA(bars []Bar) {
	const period = 20
	k := 2.0 / (period + 1)
	var sum float64
	prev := 0.0
	for i := range bars {
		bars[i].Ema20 = nil
	}
	for i := range bars {
		c := bars[i].Close
		if i < period {
			sum += c
			if i == period-1 {
				prev = sum / period
				v := prev
				bars[i].Ema20 = &v
			}
			continue
		}
		prev = c*k + prev*(1-k)
		v := prev
		bars[i].Ema20 = &v
	}
}

// anchorDate 从 "2026-10-16 12:00" 里取日期部分。
func anchorDate(t string) (string, bool) {
	t = strings.TrimSpace(t)
	if len(t) < 16 {
		return "", false
	}
	if _, err := time.ParseInLocation("2006-01-02 15:04", t, time.Local); err != nil {
		return "", false
	}
	return t[:10], true
}

func indexOfFile(files []string, name string) int {
	for i, f := range files {
		if f == name {
			return i
		}
	}
	return -1
}

func indexOfTimeIn(bars []Bar, t string) (int, bool) {
	i := sort.Search(len(bars), func(i int) bool { return bars[i].Time >= t })
	if i < len(bars) && bars[i].Time == t {
		return i, true
	}
	return 0, false
}

// dedupeBars 按时间去掉重复的 K 线，保留**后面**出现的那一份（与落盘"后写覆盖"一致）。
func dedupeBars(bars []Bar) []Bar {
	if len(bars) < 2 {
		return bars
	}
	out := bars[:0]
	for _, b := range bars {
		if n := len(out); n > 0 && out[n-1].Time == b.Time {
			out[n-1] = b
			continue
		}
		out = append(out, b)
	}
	return out
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// roundPrice 把价格修整齐到两位小数（保留给下单、标注、加载三处共用）。
// 它也是「CSV 里 89.97000000000001 到了界面上变成 89.97」的唯一实现。
func roundPrice(p float64) float64 {
	if p == 0 || math.IsNaN(p) {
		return p
	}
	return math.Round(p*100) / 100
}
