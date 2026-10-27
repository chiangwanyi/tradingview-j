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
//
// 锚点有四种来源：指定时刻（time）、随机（time 留空）、「最新」（OpenLatest）——
// 对应界面上的【新增实例（最新）】，锚点就是数据目录里最新的那一根；以及「开盘」（OpenSession）——
// 对应【新增实例（开盘）】，锚点是**随机一个交易日**的开盘首根（判据见 session.go）。
// 后两种都在这里先算出一个时间，再和指定时刻走同一条加载路径，只有「挑哪一根」不同。

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
			TickValue: e.TickValue,
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

// TickValue 查一个标的「1 tick 值多少美元」。查不到返回 0（调用方按每点 1 美元兜底）。
//
// 恢复旧存档时用：老存档里没有 tickValue，用户在 _symbols.json 里补上之后，
// 重开服务就该按新值结算，而不是沿用「不知道」。
func (ds *DataSource) TickValue(symbol, folder string) float64 {
	list, err := ds.Catalog()
	if err != nil {
		return 0
	}
	for _, e := range list {
		if e.Symbol == symbol || (folder != "" && e.Folder == folder) {
			return e.TickValue
		}
	}
	return 0
}

// Open 按「标的 + 时间锚点」准备一个实例（不含 ID 与创建者，那些由 hub 填）。
//
// anchorTime 为空时随机挑一个时间点：挑日期 -> 挑那天的某一根 -> 以它为止。
// rnd 传 nil 就用当前时间做种子；传固定种子可复现（测试与排查用）。
func (ds *DataSource) Open(symbol, anchorTime string, history, forwardDays int, rnd *rand.Rand) (*Instance, error) {
	return ds.open(symbol, anchorTime, false, history, forwardDays, rnd)
}

// OpenLatest 以「数据目录里最新的那一根 K 线」为锚点准备一个实例。
//
// 它就是界面上的【新增实例（最新）】：用户不必知道那个时刻是什么，也不用管最后一天
// 是不是提前收市（最后一天的最后一根根数是不确定的，只有读文件才知道）。
//
// 代价要说清楚：锚点已经在数据的末端，所以之后**没有可揭示的行情**（hiddenBars = 0、
// hasMore = false，【下一根】会走到 ErrNoMoreBars）。这个实例是用来「看最新这一屏」的，
// 不是用来往前后推演的 —— 要推演就选一个更早的时间点。
func (ds *DataSource) OpenLatest(symbol string, history, forwardDays int) (*Instance, error) {
	return ds.open(symbol, "", true, history, forwardDays, nil)
}

// OpenSession 以「随机一个交易日的开盘首根」为锚点准备一个实例（界面上的【新增实例（开盘）】）。
//
// 为什么是随机的一天：锚在最后一天就等于【新增实例（最新）】—— 之后没有行情可揭示，
// 练一次就白建一个实例；锚在历史里随机一天，左边天然有一屏历史、右边天然有行情能逐根推演，
// 而且每点一次都不一样（同一个日子反复练没有意义）。
//
// rnd 只决定「挑哪一天」，传 nil 就用当前时间做种子。
func (ds *DataSource) OpenSession(symbol string, history, forwardDays int, rnd *rand.Rand) (*Instance, error) {
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
	anchor, err := ds.randomSessionOpen(dir, files, rnd)
	if err != nil {
		return nil, err
	}
	return ds.Open(symbol, anchor, history, forwardDays, nil)
}

// randomSessionOpen 随机挑一天，返回那天的开盘首根。
//
// 首尾各一天排除：首日没有历史可看，尾日之后没有行情（等于【最新】）。
// 只有尾巴、没有开盘的日子（周六文件：00:00~04:55 是上一个交易日的延续）跳过 —— 判据见 session.go。
func (ds *DataSource) randomSessionOpen(dir string, files []string, rnd *rand.Rand) (string, error) {
	lo, hi := 1, len(files)-2
	if hi < lo {
		lo, hi = 0, len(files)-1 // 只有一两天数据：没有可挑的余地
	}
	span := hi - lo + 1
	start := lo + rnd.Intn(span)
	for k := 0; k < span; k++ {
		d := lo + (start-lo+k)%span
		rows, _, err := store.ReadDayRows(filepath.Join(dir, files[d]), ds.Loc)
		if err != nil || len(rows) == 0 {
			continue
		}
		times := make([]string, len(rows))
		for i, r := range rows {
			times[i] = r.Time
		}
		// 缺口可能落在接缝上（周一开盘那根就是这个文件的第一根），要上一份数据的最后一根
		if i := sessionOpenIdx(times, ds.prevLastTime(dir, files, d)); i >= 0 {
			return times[i], nil
		}
	}
	return "", errors.New("数据目录里找不到任何一个交易日的开盘（全是空文件或坏文件）")
}

// prevLastTime 取 d 之前最近一份有数据的日文件的最后一根时间（接缝上的缺口要拿它比）。
func (ds *DataSource) prevLastTime(dir string, files []string, d int) string {
	for p := d - 1; p >= 0; p-- {
		if pr, _, err := store.ReadDayRows(filepath.Join(dir, files[p]), ds.Loc); err == nil && len(pr) > 0 {
			return pr[len(pr)-1].Time
		}
	}
	return ""
}

// open 是 Open / OpenLatest 共用的那一条路径。
//
// latest = true 时忽略 anchorTime 与 rnd：锚点不是「挑」出来的，而是读到的
// （见 pickAnchor）。
func (ds *DataSource) open(symbol, anchorTime string, latest bool, history, forwardDays int, rnd *rand.Rand) (*Instance, error) {
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
		Symbol:    info.Symbol,
		Folder:    info.Folder,
		Tick:      info.Tick,
		TickValue: info.TickValue,
		dayFiles:  files,
	}
	var notes []string

	dayIdx, rowIdx, anchorRow, err := ds.pickAnchor(dir, files, anchorTime, latest, rnd, &notes)
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
	computeIndicators(bars)

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
//
// 三种模式，优先级从高到低：latest（数据里最新的那一根）> anchorTime（指定时刻）> 随机。
func (ds *DataSource) pickAnchor(dir string, files []string, anchorTime string, latest bool, rnd *rand.Rand, notes *[]string) (dayIdx, rowIdx int, rows []store.TimeBar, err error) {
	if latest {
		return ds.pickLatestAnchor(dir, files, notes)
	}
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

// pickLatestAnchor 找「目录里最新的那一根」：从最后一个日文件往前翻，
// 取第一份有 K 线的那一天的最后一根。
//
// 为什么不是「直接读最后一个文件」：周日的日文件是空的（休市），节假日、临时停市
// 也会留下空文件甚至坏文件。直接取最后一个文件会得到「这一天没有数据」，
// 而用户点【新增实例（最新）】想要的是**有数据的**最后那一根。
//
// 往前翻不设上限，但一旦找到就立刻返回；数据目录里空文件最多几十个，
// 这个循环的成本和「随机模式」里那 30 次尝试是同一个量级。
func (ds *DataSource) pickLatestAnchor(dir string, files []string, notes *[]string) (dayIdx, rowIdx int, rows []store.TimeBar, err error) {
	for d := len(files) - 1; d >= 0; d-- {
		dayRows, stat, readErr := store.ReadDayRows(filepath.Join(dir, files[d]), ds.Loc)
		if readErr != nil {
			*notes = append(*notes, fmt.Sprintf("%s 读不了（%v）", files[d], readErr))
			continue
		}
		if stat.Bad > 0 {
			*notes = append(*notes, fmt.Sprintf("%s 有 %d 行解析不了（已跳过）", files[d], stat.Bad))
		}
		if len(dayRows) == 0 {
			continue
		}
		return d, len(dayRows) - 1, dayRows, nil
	}
	return 0, 0, nil, errors.New("数据目录里一根 K 线都没有（全是空文件或坏文件）")
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
		computeIndicators(inst.Bars)
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

// BarsAtTimes 按 times 的顺序把 K 线从数据目录取回来，同时报告有几根找不到。
//
// 交易记录的 recording.json 只存时间、不存 OHLC（见 recording.go 的 recordingForDisk）：
// 那几百根 K 线本来就在日文件里，打开记录时从这里补齐。按日期分组、一天只读一次，
// 700 多根通常只落在三四个日文件里，代价和打开一个实例差不多。
//
// 找不到的根数要如实返回：数据目录被清理过时，调用方得知道盘面是残缺的。
func (ds *DataSource) BarsAtTimes(folder string, times []string) ([]Bar, int) {
	out := make([]Bar, 0, len(times))
	if ds == nil || len(times) == 0 {
		return out, len(times)
	}
	info, err := ds.resolveFolder(folder)
	if err != nil {
		return out, len(times)
	}
	dir := filepath.Join(ds.Root, info.Folder)
	days := map[string]map[string]Bar{}
	missing := 0
	for _, t := range times {
		date, ok := anchorDate(t)
		if !ok {
			missing++
			continue
		}
		day, seen := days[date]
		if !seen {
			day = map[string]Bar{}
			// 日文件不存在 / 读不了都不报错：查不到就是「这根缺失」，由下面的计数说话
			if rows, _, rerr := store.ReadDayRows(filepath.Join(dir, date+".csv"), ds.Loc); rerr == nil {
				for _, r := range rows {
					day[r.Time] = toBar(r)
				}
			}
			days[date] = day
		}
		b, ok := day[t]
		if !ok {
			missing++
			continue
		}
		out = append(out, b)
	}
	// EMA20 重算一遍就够了：记录里的 base.bars 一直是「已揭示的前缀」，序列一模一样，
	// 而 EMA 只依赖前面的收盘价，所以算出来的值和当时存下来的逐根相同。
	if len(out) > 0 {
		computeIndicators(out)
	}
	return out, missing
}

func rowsToBars(rows []store.TimeBar) []Bar {
	out := make([]Bar, 0, len(rows))
	for _, r := range rows {
		out = append(out, toBar(r))
	}
	return out
}

// computeIndicators 把「盘面事实上算出来的指标」一次算全。
//
// 之所以收成一个入口：这些字段都是**派生数据**，加载 / 续读 / 从存档补缺口都要重算一遍，
// 分散成几行调用迟早会漏一处 —— 漏掉的那一处在画布上就是「某段历史没有指标线」。
//
// 现在只剩 EMA20 一个。ATR 在 v0.23.0 挪到了前端（web/src/components/chart/atr.ts）：
// 它的 Length / Smoothing 由用户现调，参数组合无穷，后端没法预先算好几套下发 ——
// 详见那个文件开头「为什么从后端搬到前端」。
func computeIndicators(bars []Bar) {
	computeEMA(bars)
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
