package replay

// 实例的本地持久化：服务重启后，标注、挂单、成交、持仓、游标都还在。
//
// 为什么值得做：一个实例的价值在「画到哪儿了、画了什么、做过什么单」。
// 这些东西现在只在内存里，关一次窗口（或机器重启）就没了 —— 而复盘本来就是
// 跨越几天的活，不能要求用户一次看完。
//
// 落盘的是**全量 JSON**，不是增量日志：文件最多几百 KB，全量写最简单，
// 也不需要考虑「回放到第几步」这种问题。写入用 tmp + rename（和 CSV 落盘同一套），
// 半截文件不会被当成有效状态读进去。
//
// 三个刻意的决定：
//
//  1. **只存一份文件**（<stateDir>/replay-instances.json），不按实例分文件 ——
//     实例数量本来就是个位数，分文件反而要处理「孤儿文件」「删了实例但文件还在」。
//  2. **不存日文件下标**。日文件可能被增删（今天又抓了一天数据），下标会错位；
//     恢复时按「最后一根 K 线的日期之后」重新定位，是唯一稳的做法。
//  3. **操作权不恢复**。名字是连接时分配的（human / human2），重启后没人叫这个名字，
//     硬恢复会让实例永远卡在「由 human 操作」而没人能接管。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"tradingview-j/internal/store"
)

const (
	stateFileName = "replay-instances.json"
	stateVersion  = 1

	// persistEventsKept 是每个实例落盘时保留多少条操作记录。
	// 事件里带着 bar_advanced 的整份挂单/成交流水，条数不设上限会让文件涨到几十 MB。
	persistEventsKept = 100
)

// stateFile 是状态文件的整体形状。
type stateFile struct {
	Version int   `json:"version"`
	SavedAt int64 `json:"savedAt"`
	// NextInstance 是实例编号的下一个值。不存它，重启后新实例会和旧的撞号
	// （ins-1 撞 ins-1，前端 join 到错的实例）。
	NextInstance int                 `json:"nextInstance"`
	Instances    []persistedInstance `json:"instances"`
}

// persistedInstance 是一个实例的全部可变状态。
//
// 字段与 Instance 一一对应（除了那些「属于进程、不属于实例」的东西：操作者、
// 机器人协程、日文件缓存）。字段名写全，方便直接打开文件看。
type persistedInstance struct {
	ID     string  `json:"id"`
	Symbol string  `json:"symbol"`
	Folder string  `json:"folder"`
	Tick   float64 `json:"tick"`
	Anchor string  `json:"anchor"`

	Cursor int   `json:"cursor"`
	Bars   []Bar `json:"bars"`

	Annotations []Annotation `json:"annotations"`
	Orders      []Order      `json:"orders"`
	Trades      []Trade      `json:"trades"`
	Position    Position     `json:"position"`
	RealizedPnl float64      `json:"realizedPnl"`

	CreatedBy string `json:"createdBy"`
	CreatedAt int64  `json:"createdAt"`

	Seq    int      `json:"seq"`
	Events []Event  `json:"events"`
	Notes  []string `json:"notes"`

	SeqAnnotation int `json:"seqAnnotation"`
	SeqOrder      int `json:"seqOrder"`
	SeqTrade      int `json:"seqTrade"`
}

func (inst *Instance) toPersisted() persistedInstance {
	events := inst.Events
	if len(events) > persistEventsKept {
		events = events[len(events)-persistEventsKept:]
	}
	return persistedInstance{
		ID:            inst.ID,
		Symbol:        inst.Symbol,
		Folder:        inst.Folder,
		Tick:          inst.Tick,
		Anchor:        inst.Anchor,
		Cursor:        inst.Cursor,
		Bars:          inst.Bars,
		Annotations:   inst.Annotations,
		Orders:        inst.Orders,
		Trades:        inst.Trades,
		Position:      inst.Position,
		RealizedPnl:   inst.RealizedPnl,
		CreatedBy:     inst.CreatedBy,
		CreatedAt:     inst.CreatedAt,
		Seq:           inst.Seq,
		Events:        events,
		Notes:         inst.notes,
		SeqAnnotation: inst.seqAnn,
		SeqOrder:      inst.seqOrder,
		SeqTrade:      inst.seqTrade,
	}
}

func (p persistedInstance) toInstance() *Instance {
	return &Instance{
		ID:          p.ID,
		Symbol:      p.Symbol,
		Folder:      p.Folder,
		Tick:        p.Tick,
		Anchor:      p.Anchor,
		Bars:        p.Bars,
		Cursor:      p.Cursor,
		Annotations: p.Annotations,
		Orders:      p.Orders,
		Trades:      p.Trades,
		Position:    p.Position,
		RealizedPnl: p.RealizedPnl,
		CreatedBy:   p.CreatedBy,
		CreatedAt:   p.CreatedAt,
		Seq:         p.Seq,
		Events:      p.Events,
		notes:       p.Notes,
		seqAnn:      p.SeqAnnotation,
		seqOrder:    p.SeqOrder,
		seqTrade:    p.SeqTrade,
	}
}

// ---------------- Hub 侧 ----------------

// EnablePersistence 让 Hub 从 dir 里恢复实例，并在之后定时把变化写回去。
//
// dir 为空 = 不持久化（单元测试默认走这条路，免得每次跑测试都在磁盘上留文件）。
func (h *Hub) EnablePersistence(dir string) error {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("创建复盘状态目录 %s 失败：%w", dir, err)
	}

	h.mu.Lock()
	h.statePath = filepath.Join(dir, stateFileName)
	path := h.statePath
	err := h.loadStateLocked(path)
	// 模板和实例存档同目录（另一个文件）：模板跟实例无关，读不到也不影响服务起来。
	// 放在 statePath 之后是因为它要靠这个目录；放在 err 判断之前是因为存档读坏了
	// 也不该把模板一起丢掉。
	h.loadTemplatesLocked(filepath.Join(dir, templatesFileName))
	h.mu.Unlock()
	if err != nil {
		// 读坏了不是致命错误：实例从空的开始，把原因说清楚就行，
		// 但**不能**直接覆盖掉那个文件 —— 用户可能还想手工抢救它。
		h.log("[replay] 状态文件 %s 读不了（%v），本次从空列表开始；原文件保留不动", path, err)
		return nil
	}

	go h.stateLoop()
	return nil
}

// stateLoop 每两秒把脏状态写一次盘。
//
// 不「每次操作都写」是因为机器人演示时一秒好几个动作，每根 K 线都重写几百 KB
// 没有必要；两秒的窗口在崩溃时最多丢最后几个动作，而标注/成交都是即时可见的。
func (h *Hub) stateLoop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-h.stateDone:
			return
		case <-t.C:
			h.flushState()
		}
	}
}

// markDirtyLocked 标记「有变化待落盘」。调用方必须持有 h.mu。
func (h *Hub) markDirtyLocked() {
	if h.statePath == "" {
		return
	}
	h.dirty = true
}

// flushState 把当前所有实例写进状态文件（没有变化就什么也不做）。
func (h *Hub) flushState() {
	h.mu.Lock()
	if !h.dirty || h.statePath == "" {
		h.mu.Unlock()
		return
	}
	path := h.statePath
	sf := stateFile{
		Version:      stateVersion,
		SavedAt:      time.Now().Unix(),
		NextInstance: h.instanceSeq,
		Instances:    make([]persistedInstance, 0, len(h.order)),
	}
	for _, id := range h.order {
		if inst, ok := h.instances[id]; ok {
			sf.Instances = append(sf.Instances, inst.toPersisted())
		}
	}
	h.dirty = false
	h.mu.Unlock()

	// 序列化与写盘都在锁外：几百 KB 的编码不该挡住步进和广播。
	if err := writeStateFile(path, sf); err != nil {
		h.log("[replay] 存档没写成：%v（下次改动时再试）", err)
		// 写失败就把脏标记放回去：否则这一次的改动要等下一次操作才有机会落盘，
		// 而用户可能正好在这之后关掉程序。
		h.mu.Lock()
		h.dirty = true
		h.mu.Unlock()
	}
}

// writeStateFile 序列化后整份写入：先写 .tmp 再 rename，写一半断电也不会毁掉上一份存档。
func writeStateFile(path string, sf stateFile) error {
	buf, err := json.Marshal(sf)
	if err != nil {
		return fmt.Errorf("序列化失败：%w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	return nil
}

// loadStateLocked 读状态文件并恢复实例。调用方持有 h.mu。
func (h *Hub) loadStateLocked(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 第一次运行，没有状态文件是正常的
		}
		return err
	}
	var sf stateFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return fmt.Errorf("不是合法 JSON：%w", err)
	}
	if sf.Version != stateVersion {
		return fmt.Errorf("版本是 %d，本程序只认 %d", sf.Version, stateVersion)
	}

	restored := 0
	for _, p := range sf.Instances {
		if len(p.Bars) == 0 || p.Cursor < 0 || p.Cursor >= len(p.Bars) {
			h.log("[replay] 跳过实例 %s：持久化的 K 线不完整（%d 根，游标 %d）", p.ID, len(p.Bars), p.Cursor)
			continue
		}
		inst := p.toInstance()
		// 旧版本（0.5.2 及以前）的标注类型和样式要迁到新模型上，见 migrateAnnotations。
		if dropped := migrateAnnotations(inst); dropped > 0 {
			h.log("[replay] 实例 %s：%d 条旧版文字标注已丢弃（新版只有五种线/面工具）", inst.ID, dropped)
			h.markDirtyLocked()
		}
		// 操作权是「这次进程里谁在操作」，不跟着状态文件走
		inst.Operator = ""
		inst.botStop = nil
		inst.botInterval = 0
		inst.deleted = false
		h.reindexLocked(inst)
		// 0.5.0 建出来的实例，存档里锚点日之后缺一整段（见 repairDayGapsLocked）。
		// 补齐后最后一根的日期可能变了，所以要再定位一次「下一份要读的日文件」。
		if h.repairDayGapsLocked(inst) {
			h.reindexLocked(inst)
			h.markDirtyLocked()
			h.log("[replay] 实例 %s 的存档缺口已按日文件补齐（锚点 %s）", inst.ID, inst.Anchor)
		}
		h.instances[inst.ID] = inst
		h.order = append(h.order, inst.ID)
		restored++
	}
	if sf.NextInstance > h.instanceSeq {
		h.instanceSeq = sf.NextInstance
	}
	if restored > 0 {
		h.log("[replay] 已从 %s 恢复 %d 个实例（游标、标注、挂单、成交都在）", path, restored)
	}
	return nil
}

// migrateAnnotations 把 0.5.2 及以前的标注迁到新模型：
// line（线段）→ trend（趋势线，本来就是同一个东西），样式按类型补齐；
// text（文字）没有对应物（新版只有五种线/面工具），丢掉并返回丢掉了几条。
func migrateAnnotations(inst *Instance) int {
	kept := inst.Annotations[:0]
	dropped := 0
	for i := range inst.Annotations {
		a := inst.Annotations[i]
		if a.normalize() {
			kept = append(kept, a)
		} else {
			dropped++
		}
	}
	inst.Annotations = kept
	return dropped
}

// reindexLocked 重新拿到日文件列表，并把「下一个要读的日文件」定位到最后一根 K 线之后。
//
// 不存下标的原因见文件头：停机期间可能又抓了新的日文件，也可能删掉了旧的。
func (h *Hub) reindexLocked(inst *Instance) {
	dir := filepath.Join(h.ds.Root, inst.Folder)
	files, err := store.DayFiles(dir)
	if err != nil {
		inst.dayFiles = nil
		inst.nextDay = 0
		inst.notes = append(inst.notes, fmt.Sprintf("恢复时读不了 %s 的目录（%v），暂时不能继续步进", inst.Folder, err))
		return
	}
	inst.dayFiles = files
	lastDate := ""
	if n := len(inst.Bars); n > 0 {
		lastDate = barDate(inst.Bars[n-1].Time)
	}
	inst.nextDay = len(files)
	for i, name := range files {
		if barDate(strings.TrimSuffix(name, filepath.Ext(name))) > lastDate {
			inst.nextDay = i
			break
		}
	}
}

// barDate 取 "2026-10-16 12:00" / "2026-10-16" 的日期部分。
func barDate(t string) string {
	if len(t) > 10 {
		return t[:10]
	}
	return t
}

// repairDayGapsLocked 修 0.5.0 存档里的「锚点日尾巴丢了」。
//
// 0.5.0 建实例时锚点那天只收到锚点为止，所以存档里的 Bars 在「锚点日最后一根 →
// 第二天第一根」之间缺了一整段：点【下一根】会从 12:00 直接跳到第二天 00:00。
// 这跟本次修的是同一个 bug —— 光改加载逻辑救不了**已经存下来的**实例，得在这里补。
//
// 判据很硬：相邻两根跨日、而前一根在自己那天的日文件里**还有后续行**，就是缺口。
// 补法是按日文件把存档覆盖的日期范围整段重读（几百根、十来个文件，恢复时才走一次）。
//
// 重建之后**下标全变了**，所以游标和挂单一律按时间重新定位 —— 时间字符串才是这套协议里
// 唯一稳定的标识（PlacedAt / FilledAt 本来就存着，等于白送两个锚点）。
// 标注只用时间当横坐标、成交只存时间，都不用动。
func (h *Hub) repairDayGapsLocked(inst *Instance) bool {
	if len(inst.Bars) < 2 || len(inst.dayFiles) == 0 {
		return false
	}
	dir := filepath.Join(h.ds.Root, inst.Folder)

	missing := false
	for i := 1; i < len(inst.Bars) && !missing; i++ {
		prev := inst.Bars[i-1].Time
		if barDate(prev) == barDate(inst.Bars[i].Time) {
			continue // 同一天里不连续是正常的（CME 每天 05:00 那个结算断档）
		}
		name := barDate(prev) + ".csv"
		if indexOfFile(inst.dayFiles, name) < 0 {
			continue
		}
		rows, _, err := store.ReadDayRows(filepath.Join(dir, name), h.ds.Loc)
		if err != nil {
			continue
		}
		for j := range rows {
			if rows[j].Time == prev && j+1 < len(rows) {
				missing = true
				break
			}
		}
	}
	if !missing {
		return false
	}

	first, last := barDate(inst.Bars[0].Time), barDate(inst.Bars[len(inst.Bars)-1].Time)
	var bars []Bar
	for _, name := range inst.dayFiles {
		d := strings.TrimSuffix(name, filepath.Ext(name))
		if d < first || d > last {
			continue
		}
		rows, _, err := store.ReadDayRows(filepath.Join(dir, name), h.ds.Loc)
		if err != nil {
			continue
		}
		bars = append(bars, rowsToBars(rows)...)
	}
	bars = dedupeBars(bars)
	if len(bars) == 0 {
		return false
	}
	cursor, ok := indexOfTimeIn(bars, inst.Bars[inst.Cursor].Time)
	if !ok {
		// 连游标那根都对不上：宁可不动，也不要悄悄把用户挪到别的位置
		return false
	}
	computeEMA(bars)
	for i := range inst.Orders {
		o := &inst.Orders[i]
		if j, ok := indexOfTimeIn(bars, o.PlacedAt); ok {
			o.PlacedIndex = j
		}
		if o.FilledAt == "" {
			o.FilledIndex = -1
		} else if j, ok := indexOfTimeIn(bars, o.FilledAt); ok {
			o.FilledIndex = j
		}
	}
	inst.Bars = bars
	inst.Cursor = cursor
	inst.notes = append(inst.notes, "从存档恢复时补上了旧版本留下的缺口：锚点当天剩下的 K 线已按日文件补齐")
	return true
}
