package replay

// 实例的本地持久化：服务重启后，标注、挂单、成交、持仓、游标都还在。
//
// 为什么值得做：一个实例的价值在「画到哪儿了、画了什么、做过什么单」。
// 这些东西如果只在内存里，关一次窗口（或机器重启）就没了 —— 而复盘本来就是
// 跨越几天的活，不能要求用户一次看完。
//
// 落盘结构（v2 起）**按实例分文件夹**：
//
//	<stateDir>/replay-instances.json        索引：版本、保存时间、下一个实例编号、实例清单
//	<stateDir>/instances/<实例 id>/meta.json        游标、标的、tick、持仓、累计盈亏、计数器、加载备注
//	<stateDir>/instances/<实例 id>/bars.json        已加载的 K 线（含未揭示的未来部分）
//	<stateDir>/instances/<实例 id>/annotations.json 标注
//	<stateDir>/instances/<实例 id>/orders.json      挂单
//	<stateDir>/instances/<实例 id>/trades.json      成交流水
//	<stateDir>/instances/<实例 id>/events.json      操作记录
//	<stateDir>/instances/<实例 id>/voice.json       语音讲解的元数据（v0.16.0）
//	<stateDir>/instances/<实例 id>/voice/voice-N.wav 语音音频（v0.16.0）
//
// 为什么不再用一个 JSON：v1 把所有实例的 K 线、标注、成交全塞进一个文件，
// 一个实例 20 万字节、几个实例就是几百 KB；每 2 秒整份重写，改一个标注也要重写
// 所有人的行情。拆开之后「谁变了写谁」，文件也能直接打开看（meta.json 就一小段）。
//
// 写入用 tmp + rename（和 CSV 落盘同一套），半截文件不会被当成有效状态读进去。
// 一个实例的多个文件之间没有事务：**meta.json 最后写**，它是这份存档的提交点 ——
// 中途断电时最多丢掉最后两秒的增量，不会出现「游标指向一根不存在的 K 线」。
//
// 三个刻意的决定：
//
//  1. **索引只记 id 和目录名**，实例数据全在各自文件夹里；删实例时连文件夹一起删。
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
	stateVersion  = 2

	instanceDirName = "instances"
	metaFileName    = "meta.json"
	barsFileName    = "bars.json"
	annFileName     = "annotations.json"
	orderFileName   = "orders.json"
	tradeFileName   = "trades.json"
	eventFileName   = "events.json"
	voiceMetaName   = voiceFileName // voice.json（见 voice.go；音频在同目录的 voice/ 下）

	// legacyBackupSuffix 是 v1 单文件存档迁移前的备份后缀。
	// 迁移会重写 replay-instances.json，但原文件里那一大堆数据只在这里留底。
	legacyBackupSuffix = ".v1.bak"

	// persistEventsKept 是每个实例落盘时保留多少条操作记录。
	// 事件里带着 bar_advanced 的整份挂单/成交流水，条数不设上限会让文件涨到几十 MB。
	persistEventsKept = 100
)

// stateFile 是索引文件的形状（v2）。实例数据不在这里，只有 id 和目录名。
type stateFile struct {
	Version int   `json:"version"`
	SavedAt int64 `json:"savedAt"`
	// NextInstance 是实例编号的下一个值。不存它，重启后新实例会和旧的撞号
	// （ins-1 撞 ins-1，前端 join 到错的实例）。
	NextInstance int           `json:"nextInstance"`
	Instances    []instanceRef `json:"instances"`
}

// instanceRef 是索引里的一行。Dir 允许为空（= 用 id），留给以后改目录名的余地。
type instanceRef struct {
	ID  string `json:"id"`
	Dir string `json:"dir,omitempty"`
}

// instanceMeta 是 meta.json：一个实例「除了流水之外」的全部状态。
//
// 字段与 Instance 一一对应（除了那些「属于进程、不属于实例」的东西：操作者、机器人协程）。
// 字段名写全，方便直接打开文件看。
type instanceMeta struct {
	ID        string  `json:"id"`
	Symbol    string  `json:"symbol"`
	Folder    string  `json:"folder"`
	Tick      float64 `json:"tick"`
	TickValue float64 `json:"tickValue,omitempty"`
	Anchor    string  `json:"anchor"`

	Cursor      int      `json:"cursor"`
	Position    Position `json:"position"`
	RealizedPnl float64  `json:"realizedPnl"`

	CreatedBy string `json:"createdBy"`
	CreatedAt int64  `json:"createdAt"`

	Seq    int      `json:"seq"`
	Notes  []string `json:"notes"`
	SeqAnn int      `json:"seqAnnotation"`
	SeqOrd int      `json:"seqOrder"`
	SeqTrd int      `json:"seqTrade"`
}

// instanceFiles 是一个实例落盘后的全部内容，读写共用。
// 拆成字段而不是一个大结构体，是为了每类数据一个文件、只读需要的那个。
type instanceFiles struct {
	Meta        instanceMeta
	Bars        []Bar
	Annotations []Annotation
	Orders      []Order
	Trades      []Trade
	Events      []Event
	// Voice 是语音讲解的元数据（v0.16.0）。音频不在这里 —— 它是 voice/ 下的 wav，
	// 和录像里的音频同一个理由：JSON 只记「哪一段、多长、哪个文件」。
	Voice []AudioClip
}

// legacyInstance 是 v1 单文件存档里的实例（字段与旧 persistedInstance 完全一致）。
// 只用于迁移：读到它就把数据摊进新的文件夹结构。
type legacyInstance struct {
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

// legacyStateFile 是 v1 的 replay-instances.json：所有实例都在一个文件里。
type legacyStateFile struct {
	Version      int              `json:"version"`
	SavedAt      int64            `json:"savedAt"`
	NextInstance int              `json:"nextInstance"`
	Instances    []legacyInstance `json:"instances"`
}

func (inst *Instance) toFiles() instanceFiles {
	events := inst.Events
	if len(events) > persistEventsKept {
		events = events[len(events)-persistEventsKept:]
	}
	return instanceFiles{
		Meta: instanceMeta{
			ID:          inst.ID,
			Symbol:      inst.Symbol,
			Folder:      inst.Folder,
			Tick:        inst.Tick,
			TickValue:   inst.TickValue,
			Anchor:      inst.Anchor,
			Cursor:      inst.Cursor,
			Position:    inst.Position,
			RealizedPnl: inst.RealizedPnl,
			CreatedBy:   inst.CreatedBy,
			CreatedAt:   inst.CreatedAt,
			Seq:         inst.Seq,
			Notes:       copies(inst.notes),
			SeqAnn:      inst.seqAnn,
			SeqOrd:      inst.seqOrder,
			SeqTrd:      inst.seqTrade,
		},
		Bars:        inst.Bars,
		Annotations: copies(inst.Annotations),
		Orders:      copies(inst.Orders),
		Trades:      copies(inst.Trades),
		Events:      copies(events),
		Voice:       inst.voiceNotes().all(),
	}
}

func (f instanceFiles) toInstance() *Instance {
	m := f.Meta
	return &Instance{
		ID:          m.ID,
		Symbol:      m.Symbol,
		Folder:      m.Folder,
		Tick:        m.Tick,
		TickValue:   m.TickValue,
		Anchor:      m.Anchor,
		Bars:        f.Bars,
		Cursor:      m.Cursor,
		Annotations: f.Annotations,
		Orders:      f.Orders,
		Trades:      f.Trades,
		Position:    m.Position,
		RealizedPnl: m.RealizedPnl,
		CreatedBy:   m.CreatedBy,
		CreatedAt:   m.CreatedAt,
		Seq:         m.Seq,
		Events:      f.Events,
		notes:       m.Notes,
		seqAnn:      m.SeqAnn,
		seqOrder:    m.SeqOrd,
		seqTrade:    m.SeqTrd,
		// 先只带上清单；落盘目录由 restoreInstanceLocked 里的 attachVoiceLocked 补
		// （目录要知道 statePath，而这一层不该知道状态目录在哪）。
		voice: newVoiceStore("", f.Voice, nil),
	}
}

func (p legacyInstance) toFiles() instanceFiles {
	events := p.Events
	if len(events) > persistEventsKept {
		events = events[len(events)-persistEventsKept:]
	}
	return instanceFiles{
		Meta: instanceMeta{
			ID:          p.ID,
			Symbol:      p.Symbol,
			Folder:      p.Folder,
			Tick:        p.Tick,
			Anchor:      p.Anchor,
			Cursor:      p.Cursor,
			Position:    p.Position,
			RealizedPnl: p.RealizedPnl,
			CreatedBy:   p.CreatedBy,
			CreatedAt:   p.CreatedAt,
			Seq:         p.Seq,
			Notes:       p.Notes,
			SeqAnn:      p.SeqAnnotation,
			SeqOrd:      p.SeqOrder,
			SeqTrd:      p.SeqTrade,
		},
		Bars:        p.Bars,
		Annotations: p.Annotations,
		Orders:      p.Orders,
		Trades:      p.Trades,
		Events:      events,
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
	h.instanceDir = filepath.Join(dir, instanceDirName)
	path := h.statePath
	err := h.loadStateLocked(path)
	// 标注模板不在这里读：它是「本机偏好」而不是实例数据，走 EnablePresets
	// （v0.18.0 起和实例存档分目录存放，见 templates.go）。
	// 读到 v1 单文件（或者补过缺口）时已经标了脏：趁服务还没开始广播，立刻落一次，
	// 把「一个文件」换成「一个文件夹」。失败也只是下次改动时再试，不影响本次运行。
	needsFlush := h.dirtyIndex || len(h.dirtyInst) > 0
	h.mu.Unlock()
	if err != nil {
		// 读坏了不是致命错误：实例从空的开始，把原因说清楚就行，
		// 但**不能**直接覆盖掉那个文件 —— 用户可能还想手工抢救它。
		h.log("[replay] 状态文件 %s 读不了（%v），本次从空列表开始；原文件保留不动", path, err)
		return nil
	}
	if needsFlush {
		h.flushState()
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
//
// 不传 id = 索引变了（新建/删除实例、实例顺序变了）；传 id = 那个实例的数据变了。
// 分开记是为了只重写真正变了的那个文件夹 —— 步进一次不该把别人的成交也重写一遍。
func (h *Hub) markDirtyLocked(ids ...string) {
	if h.statePath == "" {
		return
	}
	if len(ids) == 0 {
		h.dirtyIndex = true
		return
	}
	for _, id := range ids {
		if id != "" {
			h.dirtyInst[id] = true
		}
	}
}

// flushState 把当前脏的实例和索引写进状态目录（没有变化就什么也不做）。
func (h *Hub) flushState() {
	h.mu.Lock()
	if h.statePath == "" || (!h.dirtyIndex && len(h.dirtyInst) == 0) {
		h.mu.Unlock()
		return
	}
	path, instRoot := h.statePath, h.instanceDir

	sf := stateFile{
		Version:      stateVersion,
		SavedAt:      time.Now().Unix(),
		NextInstance: h.instanceSeq,
		Instances:    make([]instanceRef, 0, len(h.order)),
	}
	var pending []instanceFiles
	var pendingIDs []string
	for _, id := range h.order {
		inst, ok := h.instances[id]
		if !ok {
			continue // 刚删掉的实例：索引里不该再有它
		}
		sf.Instances = append(sf.Instances, instanceRef{ID: id, Dir: instanceFolder(id)})
		if h.dirtyInst[id] {
			pending = append(pending, inst.toFiles())
			pendingIDs = append(pendingIDs, id)
		}
	}
	writeIndex := h.dirtyIndex
	remove := h.pendingRemove
	h.dirtyIndex = false
	h.pendingRemove = nil
	h.dirtyInst = map[string]bool{}
	h.mu.Unlock()

	// 序列化与写盘都在锁外：几百 KB 的编码不该挡住步进和广播。
	indexOK := true
	if writeIndex {
		if err := writeJSONAtomic(path, sf); err != nil {
			indexOK = false
			h.log("[replay] 存档索引没写成：%v（下次改动时再试）", err)
			h.mu.Lock()
			h.dirtyIndex = true
			h.mu.Unlock()
		}
	}

	for i, f := range pending {
		if err := writeInstanceFiles(instRoot, pendingIDs[i], f); err != nil {
			h.log("[replay] 实例 %s 的存档没写成：%v（下次改动时再试）", pendingIDs[i], err)
			// 写失败就把脏标记放回去：否则这一次的改动要等下一次操作才有机会落盘，
			// 而用户可能正好在这之后关掉程序。
			h.mu.Lock()
			h.dirtyInst[pendingIDs[i]] = true
			h.mu.Unlock()
		}
	}

	// 索引写成功才删「已删除实例」的文件夹：索引没更新时删文件夹会让数据凭空消失。
	if writeIndex && indexOK {
		for _, dir := range remove {
			_ = os.RemoveAll(filepath.Join(instRoot, dir))
		}
	} else if len(remove) > 0 {
		// 索引没写成：删除动作留到下一轮，先别动任何目录
		h.mu.Lock()
		h.pendingRemove = append(h.pendingRemove, remove...)
		h.mu.Unlock()
	}
}

// ---------------- 读写单个实例 ----------------

// instanceFolder 把实例 id 变成安全的目录名（ins-1 -> ins-1）。
func instanceFolder(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		return "instance"
	}
	return out
}

func instanceDirPath(root, id string) string {
	return filepath.Join(root, instanceFolder(id))
}

// writeInstanceFiles 把一个实例写进 <instRoot>/<id>/。
//
// 顺序有意义：**meta.json 最后写**。bar 先进盘、meta 的 cursor 后落，
// 中途断电时读到的是「上一份 cursor + 可能多出来的 K 线」，加载时按 cursor 截取，
// 不会出现游标越界。
func writeInstanceFiles(instRoot, id string, f instanceFiles) error {
	dir := instanceDirPath(instRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := []struct {
		name string
		v    any
	}{
		{barsFileName, f.Bars},
		{annFileName, f.Annotations},
		{orderFileName, f.Orders},
		{tradeFileName, f.Trades},
		{eventFileName, f.Events},
		{voiceMetaName, f.Voice},
		{metaFileName, f.Meta},
	}
	for _, item := range files {
		if err := writeJSONAtomic(filepath.Join(dir, item.name), item.v); err != nil {
			return fmt.Errorf("%s：%w", item.name, err)
		}
	}
	return nil
}

// readInstanceFiles 读一个实例文件夹。
//
// meta.json 和 bars.json 缺一不可（没有它们这个实例没法用）；标注/挂单/成交/事件
// 缺了或坏了只当空的 —— 总比因为一个文件损坏就丢掉整段复盘强。
func readInstanceFiles(dir string, logf func(format string, args ...any)) (instanceFiles, error) {
	var f instanceFiles
	if err := readJSONFile(filepath.Join(dir, metaFileName), &f.Meta); err != nil {
		return f, fmt.Errorf("meta.json 读不了：%w", err)
	}
	if err := readJSONFile(filepath.Join(dir, barsFileName), &f.Bars); err != nil {
		return f, fmt.Errorf("bars.json 读不了：%w", err)
	}
	optional := []struct {
		name string
		dst  any
	}{
		{annFileName, &f.Annotations},
		{orderFileName, &f.Orders},
		{tradeFileName, &f.Trades},
		{eventFileName, &f.Events},
		// 语音清单缺了就当没有（旧存档、或者刚被清空）：音频文件还在也别去猜，
		// 猜错了会出现「清单里没有、文件夹里有」的幽灵录音。
		{voiceMetaName, &f.Voice},
	}
	for _, item := range optional {
		if err := readJSONFile(filepath.Join(dir, item.name), item.dst); err != nil && !os.IsNotExist(err) {
			if logf != nil {
				logf("[replay] 实例 %s 的 %s 读不了（%v），按空处理", f.Meta.ID, item.name, err)
			}
		}
	}
	return f, nil
}

// writeJSONAtomic 序列化后整份写入：先写 .tmp 再 rename，写一半断电也不会毁掉上一份存档。
func writeJSONAtomic(path string, v any) error {
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化失败：%w", err)
	}
	buf = append(buf, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func readJSONFile(path string, v any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("不是合法 JSON：%w", err)
	}
	return nil
}

// ---------------- 恢复 ----------------

// loadStateLocked 读状态文件并恢复实例。调用方持有 h.mu。
func (h *Hub) loadStateLocked(path string) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // 第一次运行，没有状态文件是正常的
		}
		return err
	}
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		return fmt.Errorf("不是合法 JSON：%w", err)
	}
	switch head.Version {
	case stateVersion:
		return h.loadIndexLocked(raw)
	case 1:
		return h.loadLegacyLocked(path, raw)
	default:
		return fmt.Errorf("版本是 %d，本程序只认 %d（或更旧的 1）", head.Version, stateVersion)
	}
}

// loadIndexLocked 按索引逐个文件夹恢复实例。
func (h *Hub) loadIndexLocked(raw []byte) error {
	var sf stateFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return fmt.Errorf("不是合法 JSON：%w", err)
	}
	restored := 0
	for _, ref := range sf.Instances {
		dir := ref.Dir
		if dir == "" {
			dir = instanceFolder(ref.ID)
		}
		f, err := readInstanceFiles(filepath.Join(h.instanceDir, dir), h.log)
		if err != nil {
			// 读不了也不从索引里删：文件夹原样留着，用户可能只想手工修一下
			h.log("[replay] 跳过实例 %s：%v", ref.ID, err)
			continue
		}
		inst := f.toInstance()
		if !h.restoreInstanceLocked(inst) {
			continue
		}
		restored++
	}
	if sf.NextInstance > h.instanceSeq {
		h.instanceSeq = sf.NextInstance
	}
	if restored > 0 {
		h.log("[replay] 已从 %s 恢复 %d 个实例（游标、标注、挂单、成交都在）", h.statePath, restored)
	}
	return nil
}

// loadLegacyLocked 读 v1 的单文件存档，并在内存里换成新结构。
//
// 旧文件在落新格式之前先原样备份一份（replay-instances.json.v1.bak）：
// 迁移一旦有 bug，用户至少还能把这份拷回去用旧版本打开。
// 真正的新格式落盘由 EnablePersistence 在迁移完旧文件后立刻触发。
func (h *Hub) loadLegacyLocked(path string, raw []byte) error {
	var sf legacyStateFile
	if err := json.Unmarshal(raw, &sf); err != nil {
		return fmt.Errorf("不是合法 JSON：%w", err)
	}
	backup := path + legacyBackupSuffix
	if _, err := os.Stat(backup); os.IsNotExist(err) {
		if werr := os.WriteFile(backup, raw, 0o644); werr != nil {
			h.log("[replay] 迁移前的备份没写成（%v），仍继续迁移", werr)
		} else {
			h.log("[replay] 旧版单文件存档已备份到 %s", backup)
		}
	}

	restored := 0
	for _, p := range sf.Instances {
		f := p.toFiles()
		inst := f.toInstance()
		if !h.restoreInstanceLocked(inst) {
			continue
		}
		// v1 存档里的盈亏是「点数 × 手数」，没有乘每点价值（那时候还没有 tickValue）。
		// 迁移时按现在的配置换成美元，否则同一个实例里旧成交是点数、新成交是美元，
		// 累计盈亏会和流水对不上。
		if k := inst.pointValue(); k != 1 {
			for i := range inst.Trades {
				inst.Trades[i].RealizedPnl *= k
			}
			inst.RealizedPnl *= k
		}
		restored++
	}
	if sf.NextInstance > h.instanceSeq {
		h.instanceSeq = sf.NextInstance
	}

	// 整个目录都要按新格式重写一遍：索引 + 每个实例的文件夹。
	h.markDirtyLocked()
	for id := range h.instances {
		h.markDirtyLocked(id)
	}
	h.log("[replay] 存档已从 v1 单文件迁移到按实例分文件夹（恢复 %d 个实例）", restored)
	return nil
}

// restoreInstanceLocked 做「读出来之后、放进 Hub 之前」的那一串修整。
// 返回 false = 这条实例不完整，跳过。
func (h *Hub) restoreInstanceLocked(inst *Instance) bool {
	if len(inst.Bars) == 0 || inst.Cursor < 0 || inst.Cursor >= len(inst.Bars) {
		h.log("[replay] 跳过实例 %s：持久化的 K 线不完整（%d 根，游标 %d）", inst.ID, len(inst.Bars), inst.Cursor)
		return false
	}
	if inst.Anchor == "" {
		inst.Anchor = inst.Bars[inst.Cursor].Time
	}
	// 旧存档里没有 tickValue：按 _symbols.json（或内置兜底表）补一次。
	// 每次恢复都重新取，用户改了配置里的「一跳值多少」之后重开就生效。
	if v := h.ds.TickValue(inst.Symbol, inst.Folder); v > 0 {
		inst.TickValue = v
	}
	// 旧版本（0.5.2 及以前）的标注类型和样式要迁到新模型上，见 migrateAnnotations。
	if dropped := migrateAnnotations(inst); dropped > 0 {
		h.log("[replay] 实例 %s：%d 条旧版文字标注已丢弃（新版只有五种线/面工具）", inst.ID, dropped)
		h.markDirtyLocked(inst.ID)
	}
	// 操作权是「这次进程里谁在操作」，不跟着状态文件走
	inst.Operator = ""
	inst.botStop = nil
	inst.botInterval = 0
	inst.botStrategy = ""
	inst.botQty = 0
	inst.run = nil
	inst.deleted = false
	h.reindexLocked(inst)
	// 0.5.0 建出来的实例，存档里锚点日之后缺一整段（见 repairDayGapsLocked）。
	// 补齐后最后一根的日期可能变了，所以要再定位一次「下一份要读的日文件」。
	if h.repairDayGapsLocked(inst) {
		h.reindexLocked(inst)
		h.markDirtyLocked(inst.ID)
		h.log("[replay] 实例 %s 的存档缺口已按日文件补齐（锚点 %s）", inst.ID, inst.Anchor)
	}
	// 语音讲解的目录（instances/<id>/voice）在恢复时才定得下来：它靠 statePath 展开。
	// 放在最后是因为 attachVoiceLocked 会拿「读回来的清单」重建 store（含编号计数器）。
	h.attachVoiceLocked(inst)
	h.instances[inst.ID] = inst
	h.order = append(h.order, inst.ID)
	return true
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
