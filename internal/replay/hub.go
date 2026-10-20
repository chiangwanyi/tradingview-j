package replay

// Hub 是「谁连着我、有哪些实例、这一条消息该广播给谁」的唯一权威。
//
// 两个刻意的设计：
//
//  1. **一把锁罩住「改状态 + 逐个连接写」**。同一连接的并发写会把帧撕开，
//     广播顺序也必须等于处理顺序 —— 多用户下两个人同时点「下一根」，谁先谁后
//     必须所有人看到的都一样。写失败只标记、不在这里关连接（关连接会反过来
//     触发读循环回调 Hub，持锁关连接就是死锁）。
//  2. **操作权只有一个**。实例上记一个 Operator 名字：谁先动谁接管，其他人只能观看。
//     这样「多人演示」天然是有序的，也顺便解决了机器人测试时和真人抢操作的问题。

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"
)

// Client 是一条已连接的 WebSocket。write/close 由 ws 层注入，Hub 不碰网络细节。
type Client struct {
	ID         string
	Name       string
	InstanceID string
	write      func([]byte) error
	close      func()
}

// Hub 管理所有连接与实例。
type Hub struct {
	mu        sync.Mutex
	clients   map[string]*Client
	instances map[string]*Instance
	order     []string // 实例创建顺序，列表按它排
	ds        *DataSource
	version   string
	log       func(format string, args ...any)
	rnd       *rand.Rand
	garbage   []*Client // 写失败、等解锁后再关的连接

	clientSeq   int
	instanceSeq int
	usedNames   map[string]bool
	closed      bool

	// 持久化（persist.go）：statePath 为空 = 不落盘（单元测试就是这么跑的）。
	// dirtyIndex 管索引（新建/删除实例），dirtyInst 管单个实例的数据 ——
	// 分开记，步进一次就只重写那一个文件夹。
	statePath     string
	instanceDir   string
	stateDone     chan struct{}
	dirtyIndex    bool
	dirtyInst     map[string]bool
	pendingRemove []string // 已删除实例的目录名，等索引写成功后再回收

	// 标注模板（templates.go）：和实例存档同一个目录，但另一个文件
	templates []Template
	tplSeq    int
	tplPath   string

	// 录制（recording.go）。records 永远非 nil：没配目录时它是一个「不落盘」的
	// 空壳，录制照样能在内存里跑完（测试就是这么用的）。
	records *RecordingStore
	// pendingSaves 是「已经录完、等锁外落盘」的录像。写盘是 IO，
	// 不能拿着 h.mu 做 —— 那会把所有人的步进和广播一起卡住。
	pendingSaves []pendingSave
}

// pendingSave 是一条等待写盘的录像（含音频字节）。
type pendingSave struct {
	rec    *Recording
	audios []pendingAudio
}

func NewHub(ds *DataSource, version string, logf func(format string, args ...any)) *Hub {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Hub{
		clients:   map[string]*Client{},
		instances: map[string]*Instance{},
		ds:        ds,
		version:   version,
		log:       logf,
		rnd:       rand.New(rand.NewSource(time.Now().UnixNano())),
		usedNames: map[string]bool{},
		stateDone: make(chan struct{}),
		dirtyInst: map[string]bool{},
		// 出厂模板先放在内存里：没配状态目录（测试）或第一次运行都有得用。
		// EnablePersistence 会拿磁盘上的文件覆盖它。
		templates: defaultTemplates(),
		tplSeq:    len(defaultTemplates()),
		// 没启用录像目录时它是个空壳：录制能跑、只是不落盘
		records: &RecordingStore{},
	}
}

// Register 接纳一条新连接，分配 id 与显示名（human / human2 / ...）。
//
// 名字要唯一且稳定：广播里用名字标「谁干的」，重名会让演示回看彻底没法读。
func (h *Hub) Register(write func([]byte) error, closeFn func()) *Client {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clientSeq++
	c := &Client{
		ID:    fmt.Sprintf("c%d", h.clientSeq),
		Name:  h.uniqueNameLocked(),
		write: write,
		close: closeFn,
	}
	h.clients[c.ID] = c
	h.reply(c, WelcomeMsg{Type: MsgWelcome, ClientID: c.ID, Name: c.Name, Version: h.version})
	h.log("[replay] %s（%s）已连接，当前 %d 人", c.Name, c.ID, len(h.clients))
	return c
}

func (h *Hub) uniqueNameLocked() string {
	if !h.usedNames["human"] {
		h.usedNames["human"] = true
		return "human"
	}
	for i := 2; ; i++ {
		name := fmt.Sprintf("human%d", i)
		if !h.usedNames[name] {
			h.usedNames[name] = true
			return name
		}
	}
}

// Unregister 断开一条连接：退出所在实例、必要时交还操作权、可能停掉机器人。
func (h *Hub) Unregister(c *Client) {
	if c == nil {
		return
	}
	h.mu.Lock()
	defer func() {
		// 断开时可能刚好收尾了一段录制（见 leaveLocked）：写盘同样放到锁外。
		saves := h.pendingSaves
		h.pendingSaves = nil
		h.mu.Unlock()
		for _, s := range saves {
			h.saveRecording(s)
		}
	}()
	if _, ok := h.clients[c.ID]; !ok {
		return
	}
	h.leaveLocked(c, true)
	delete(h.clients, c.ID)
	// 名字只在「同时在线」时才要求唯一，人走了就还回池子。不还的话，一个人刷新
	// 两百次页面（或者掉线重连两百次）会看到 human201，而列表里全是幽灵名字。
	// 已经发生的广播里记的仍是当时的名字，回看照旧能读。
	delete(h.usedNames, c.Name)
	h.broadcastInstancesLocked()
	h.log("[replay] %s 已断开，当前 %d 人", c.Name, len(h.clients))
}

// Handle 处理一条上行 JSON。所有状态变更都在这里（及其调用的 do* 方法）发生。
func (h *Hub) Handle(c *Client, raw []byte) {
	h.mu.Lock()
	defer func() {
		garbage := h.garbage
		h.garbage = nil
		saves := h.pendingSaves
		h.pendingSaves = nil
		h.mu.Unlock()
		for _, s := range saves {
			h.saveRecording(s)
		}
		for _, d := range garbage {
			if d.close != nil {
				d.close()
			}
		}
	}()

	var req Request
	if err := json.Unmarshal(raw, &req); err != nil {
		h.reply(c, ErrorMsg{Type: MsgError, Message: "消息不是合法 JSON：" + err.Error()})
		return
	}
	if h.closed {
		return
	}

	switch req.Type {
	case ReqHello:
		h.handleHello(c, req)
	case ReqListSymbols:
		h.handleListSymbols(c)
	case ReqListInstances:
		h.reply(c, InstancesMsg{Type: MsgInstances, Instances: h.summariesLocked()})
	case ReqCreate:
		h.handleCreate(c, req)
	case ReqJoin:
		h.handleJoin(c, req)
	case ReqLeave:
		h.handleLeave(c, req)
	case ReqDelete:
		h.handleDelete(c, req)
	case ReqTakeControl:
		h.handleTakeControl(c, req)
	case ReqRelease:
		h.handleRelease(c, req)
	case ReqStep:
		h.handleStep(c, req)
	case ReqAnnAdd:
		h.handleAnnotationAdd(c, req)
	case ReqAnnMove:
		h.handleAnnotationMove(c, req)
	case ReqAnnDelete:
		h.handleAnnotationDelete(c, req)
	case ReqAnnClear:
		h.handleAnnotationClear(c, req)
	case ReqListTemplates:
		h.handleListTemplates(c)
	case ReqSaveTemplate:
		h.handleSaveTemplate(c, req)
	case ReqDelTemplate:
		h.handleDeleteTemplate(c, req)
	case ReqOrderPlace:
		h.handleOrderPlace(c, req)
	case ReqOrderMarket:
		h.handleOrderMarket(c, req)
	case ReqOrderCancel:
		h.handleOrderCancel(c, req)
	case ReqOrderModify:
		h.handleOrderModify(c, req)
	case ReqBotStart:
		h.handleBotStart(c, req)
	case ReqBotStop:
		h.handleBotStop(c, req)
	case ReqPing:
		h.reply(c, PongMsg{Type: MsgPong})
	case ReqRecordStart:
		h.handleRecordStart(c, req)
	case ReqRecordStop:
		h.handleRecordStop(c, req)
	case ReqRecordAudio:
		h.handleRecordAudio(c, req)
	case ReqListRecordings:
		h.handleListRecordings(c)
	case ReqGetRecording:
		h.handleGetRecording(c, req)
	case ReqDeleteRecording:
		h.handleDeleteRecording(c, req)
	case "":
		h.fail(c, req, "消息缺少 type 字段")
	default:
		h.fail(c, req, fmt.Sprintf("不认识的消息类型 %q", req.Type))
	}
}

// ---------------- 各类型请求 ----------------

func (h *Hub) handleHello(c *Client, req Request) {
	name := strings.TrimSpace(req.Name)
	if name != "" && name != c.Name {
		if n := len([]rune(name)); n > 20 {
			name = string([]rune(name)[:20])
		}
		if h.usedNames[name] {
			h.fail(c, req, fmt.Sprintf("名字 %q 已经有人用了，换一个（断开重连也能重新分配）", name))
		} else {
			delete(h.usedNames, c.Name)
			h.usedNames[name] = true
			c.Name = name
			h.broadcastInstancesLocked()
		}
	}
	h.reply(c, WelcomeMsg{Type: MsgWelcome, ClientID: c.ID, Name: c.Name, Version: h.version})
}

func (h *Hub) handleListSymbols(c *Client) {
	list, err := h.ds.Catalog()
	if err != nil {
		h.reply(c, ErrorMsg{Type: MsgError, Message: err.Error(), Request: ReqListSymbols})
		return
	}
	h.reply(c, SymbolsMsg{Type: MsgSymbols, Symbols: list})
}

func (h *Hub) handleCreate(c *Client, req Request) {
	rnd := h.rnd
	if req.Seed != 0 {
		// 显式种子：只影响这一次随机锚点的选择，不动全局随机源
		rnd = rand.New(rand.NewSource(req.Seed))
	}
	inst, err := h.ds.Open(req.Symbol, strings.TrimSpace(req.Time), req.Bars, req.ForwardDays, rnd)
	if err != nil {
		h.fail(c, req, err.Error())
		return
	}
	h.instanceSeq++
	inst.ID = fmt.Sprintf("ins-%d", h.instanceSeq)
	inst.CreatedBy = c.Name
	inst.CreatedAt = time.Now().Unix()
	// 创建者自动进实例并拿到操作权：自己刚开的盘，第一下就应该是自己能点。
	if c.InstanceID != "" && c.InstanceID != inst.ID {
		h.leaveLocked(c, false)
	}
	c.InstanceID = inst.ID
	inst.Operator = c.Name

	h.instances[inst.ID] = inst
	h.order = append(h.order, inst.ID)
	// 新实例既要进索引，也要自己那份数据落盘
	h.markDirtyLocked()
	h.markDirtyLocked(inst.ID)
	h.log("[replay] %s 新建实例 %s（%s @ %s，%d 根已加载）", c.Name, inst.ID, inst.Symbol, inst.Anchor, len(inst.Bars))

	h.sendStateLocked(c, inst)
	h.broadcastInstancesLocked()
}

func (h *Hub) handleJoin(c *Client, req Request) {
	inst, ok := h.findInstanceLocked(c, req)
	if !ok {
		return
	}
	if c.InstanceID != "" && c.InstanceID != inst.ID {
		h.leaveLocked(c, false)
	}
	c.InstanceID = inst.ID
	if inst.Operator == "" {
		inst.Operator = c.Name
		h.broadcastInstanceLocked(inst, ActControlChanged, ControlChangedPayload{Operator: c.Name}, c.Name)
	}
	h.sendStateLocked(c, inst)
	h.broadcastMembersLocked(inst)
	h.broadcastInstancesLocked()
}

func (h *Hub) handleLeave(c *Client, req Request) {
	if req.InstanceID != "" && req.InstanceID != c.InstanceID {
		h.fail(c, req, "你不在这个实例里")
		return
	}
	if c.InstanceID == "" {
		h.fail(c, req, "你现在不在任何实例里")
		return
	}
	id := c.InstanceID
	h.leaveLocked(c, false)
	h.reply(c, LeftMsg{Type: MsgLeft, InstanceID: id})
	h.broadcastInstancesLocked()
}

func (h *Hub) handleDelete(c *Client, req Request) {
	inst, ok := h.findInstanceLocked(c, req)
	if !ok {
		return
	}
	// 只有创建者或当前操作者能删。其他人只能退出 —— 免得观众一失手把大家的复盘删了。
	if c.Name != inst.CreatedBy && c.Name != inst.Operator {
		h.fail(c, req, "只有实例的创建者或当前操作者能删除它")
		return
	}
	h.deleteLocked(inst)
}

func (h *Hub) handleTakeControl(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if inst.Operator == c.Name {
		h.reply(c, ErrorMsg{Type: MsgError, Request: req.Type, Instance: inst.ID, Message: "操作权已经在你手上"})
		return
	}
	if inst.Operator != "" {
		h.fail(c, req, fmt.Sprintf("现在由 %s 操作，等他交还或退出后才能接管", inst.Operator))
		return
	}
	inst.Operator = c.Name
	h.broadcastInstanceLocked(inst, ActControlChanged, ControlChangedPayload{Operator: c.Name}, c.Name)
	h.broadcastInstancesLocked()
}

func (h *Hub) handleRelease(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if inst.Operator != c.Name {
		h.fail(c, req, "操作权不在你手上")
		return
	}
	inst.Operator = ""
	h.broadcastInstanceLocked(inst, ActControlChanged, ControlChangedPayload{Operator: ""}, c.Name)
	h.broadcastInstancesLocked()
}

func (h *Hub) handleStep(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if err := h.doStepLocked(inst, c.Name); err != nil {
		h.fail(c, req, err.Error())
	}
}

func (h *Hub) handleAnnotationAdd(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if req.Start == nil {
		h.fail(c, req, "缺少 start（标注端点必须指向一根 K 线）")
		return
	}
	if req.End == nil {
		h.fail(c, req, "缺少 end（五种图形都用两个端点：线的两端 / 矩形对角 / 斐波那契的价格区间）")
		return
	}
	style := AnnotationStyle{}
	if req.Style != nil {
		style = *req.Style
	}
	if _, err := h.doAddAnnotationLocked(inst, c.Name, req.Kind, *req.Start, *req.End, style, req.Snap); err != nil {
		h.fail(c, req, err.Error())
	}
}

func (h *Hub) handleAnnotationMove(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if req.Start == nil {
		h.fail(c, req, "缺少 start")
		return
	}
	if req.End == nil {
		h.fail(c, req, "缺少 end（移动整体和只移动一个端点走同一条消息，两端都要发）")
		return
	}
	if _, err := h.doMoveAnnotationLocked(inst, c.Name, req.AnnotationID, *req.Start, *req.End, req.Snap); err != nil {
		h.fail(c, req, err.Error())
	}
}

func (h *Hub) handleAnnotationDelete(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if err := h.doDeleteAnnotationLocked(inst, c.Name, req.AnnotationID); err != nil {
		h.fail(c, req, err.Error())
	}
}

// handleAnnotationClear 处理「移除全部绘图」（画布右键菜单）。
//
// 它删的是**这个实例**的绘图，不是某一个人的：画布上的图形属于实例，任何人画的都在同一层。
// 所以要和单条删除一样要操作权 —— 观众没有资格一把清空别人画的图。
func (h *Hub) handleAnnotationClear(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if _, err := h.doClearAnnotationsLocked(inst, c.Name); err != nil {
		h.fail(c, req, err.Error())
	}
}

func (h *Hub) handleOrderPlace(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if _, err := h.doPlaceOrderLocked(inst, c.Name, req.Side, req.Kind, req.Price, req.Quantity); err != nil {
		h.fail(c, req, err.Error())
	}
}

func (h *Hub) handleOrderMarket(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if _, _, err := h.doMarketOrderLocked(inst, c.Name, req.Side, req.Quantity); err != nil {
		h.fail(c, req, err.Error())
	}
}

func (h *Hub) handleOrderCancel(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if err := h.doCancelOrderLocked(inst, c.Name, req.OrderID); err != nil {
		h.fail(c, req, err.Error())
	}
}

// handleOrderModify 处理「拖动挂单线改价」。
//
// 和撤单一样要操作权：改价会改变这张单接下来会不会成交，属于交易动作。
func (h *Hub) handleOrderModify(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if !h.requireOperatorLocked(c, req, inst) {
		return
	}
	if _, err := h.doMoveOrderLocked(inst, c.Name, req.OrderID, req.Price); err != nil {
		h.fail(c, req, err.Error())
	}
}

// ---------------- 操作实现（真人 / 机器人共用） ----------------

func (h *Hub) doStepLocked(inst *Instance, by string) error {
	bar, fills, err := inst.advance(h.ds)
	if err != nil {
		return err
	}
	if fills == nil {
		fills = []Trade{}
	}
	h.broadcastInstanceLocked(inst, ActBarAdvanced, BarAdvancedPayload{
		Bar:           bar,
		Cursor:        inst.Cursor,
		HiddenBars:    inst.hiddenBars(),
		HasMore:       inst.hasMore(),
		Orders:        copies(inst.Orders),
		Trades:        copies(inst.Trades),
		ClosedLots:    inst.closedLots(),
		Position:      inst.position(),
		RealizedPnl:   inst.RealizedPnl,
		UnrealizedPnl: inst.unrealizedPnl(),
		Fills:         fills,
	}, by)
	return nil
}

func (h *Hub) doAddAnnotationLocked(inst *Instance, by, kind string, start, end Point, style AnnotationStyle, snap bool) (Annotation, error) {
	a, err := inst.addAnnotation(by, kind, start, end, style, snap)
	if err != nil {
		return Annotation{}, err
	}
	h.broadcastInstanceLocked(inst, ActAnnotationAdded, AnnotationPayload{Annotation: a}, by)
	return a, nil
}

func (h *Hub) doMoveAnnotationLocked(inst *Instance, by, id string, start, end Point, snap bool) (Annotation, error) {
	a, err := inst.moveAnnotation(by, id, start, end, snap)
	if err != nil {
		return Annotation{}, err
	}
	h.broadcastInstanceLocked(inst, ActAnnotationMoved, AnnotationPayload{Annotation: a}, by)
	return a, nil
}

func (h *Hub) doDeleteAnnotationLocked(inst *Instance, by, id string) error {
	if err := inst.deleteAnnotation(id); err != nil {
		return err
	}
	h.broadcastInstanceLocked(inst, ActAnnotationDelete, AnnotationDeletedPayload{AnnotationID: id}, by)
	return nil
}

// doClearAnnotationsLocked 清空全部绘图并广播一条 annotation_cleared。
//
// 空画布直接报错而不是静默成功：前端菜单在 0 个绘图时本来就是灰的，
// 真走到这里只可能是「别人刚清完」的竞态 —— 给用户一句准话，比什么都不发生好。
func (h *Hub) doClearAnnotationsLocked(inst *Instance, by string) (int, error) {
	ids := inst.clearAnnotations()
	if len(ids) == 0 {
		return 0, fmt.Errorf("当前画布上没有绘图可移除")
	}
	h.broadcastInstanceLocked(inst, ActAnnotationClear, AnnotationClearedPayload{
		Count:         len(ids),
		AnnotationIDs: ids,
	}, by)
	return len(ids), nil
}

func (h *Hub) doPlaceOrderLocked(inst *Instance, by, side, kind string, price, qty float64) (Order, error) {
	o, err := inst.placeOrder(by, side, kind, price, qty)
	if err != nil {
		return Order{}, err
	}
	h.broadcastInstanceLocked(inst, ActOrderPlaced, OrdersPayload{Orders: copies(inst.Orders)}, by)
	return o, nil
}

func (h *Hub) doMarketOrderLocked(inst *Instance, by, side string, qty float64) (Order, Trade, error) {
	o, tr, err := inst.marketOrder(by, side, qty)
	if err != nil {
		return Order{}, Trade{}, err
	}
	h.broadcastInstanceLocked(inst, ActOrderFilled, OrderFilledPayload{
		Order:         o,
		Trade:         tr,
		Orders:        copies(inst.Orders),
		Trades:        copies(inst.Trades),
		ClosedLots:    inst.closedLots(),
		Position:      inst.position(),
		RealizedPnl:   inst.RealizedPnl,
		UnrealizedPnl: inst.unrealizedPnl(),
	}, by)
	return o, tr, nil
}

// doMoveOrderLocked 改价并广播。列表每次都发全量（挂单本来就只有几张），
// 顺带把改完的那一张单独发出去 —— 操作记录靠它写清「改的是哪张、改成了多少」。
func (h *Hub) doMoveOrderLocked(inst *Instance, by, id string, price float64) (Order, error) {
	o, err := inst.moveOrder(id, price)
	if err != nil {
		return Order{}, err
	}
	h.broadcastInstanceLocked(inst, ActOrderMoved, OrderMovedPayload{Order: o, Orders: copies(inst.Orders)}, by)
	return o, nil
}

func (h *Hub) doCancelOrderLocked(inst *Instance, by, id string) error {
	if _, err := inst.cancelOrder(id); err != nil {
		return err
	}
	h.broadcastInstanceLocked(inst, ActOrderCancelled, OrdersPayload{Orders: copies(inst.Orders)}, by)
	return nil
}

// ---------------- 操作权 ----------------

// requireOperatorLocked 返回 true = 这次可以操作；false 时已经给请求方回了原因。
//
// 没人接管时「谁先动谁接管」：比要求先点一个「接管」按钮少一步，也不会出现
// 两个人同时以为自己是操作者（第二个人的请求会被这里挡掉）。
func (h *Hub) requireOperatorLocked(c *Client, req Request, inst *Instance) bool {
	if inst.Operator == c.Name {
		return true
	}
	if inst.Operator == "" {
		inst.Operator = c.Name
		h.broadcastInstanceLocked(inst, ActControlChanged, ControlChangedPayload{Operator: c.Name}, c.Name)
		h.broadcastInstancesLocked()
		return true
	}
	h.fail(c, req, fmt.Sprintf("现在由 %s 操作，你只能观看（等他交还操作权或退出）", inst.Operator))
	return false
}

// ---------------- 实例生命周期 ----------------

// findInstanceLocked 只按 id 找实例，不检查「你加入没加入」。
// 大厅里点「进入」和「删除」时都还没加入，所以不能走 instanceLocked。
func (h *Hub) findInstanceLocked(c *Client, req Request) (*Instance, bool) {
	id := req.InstanceID
	if id == "" {
		id = c.InstanceID
	}
	if id == "" {
		h.fail(c, req, "没有指定实例")
		return nil, false
	}
	inst, ok := h.instances[id]
	if !ok {
		h.fail(c, req, fmt.Sprintf("实例 %s 不存在（可能已被删除）", id))
		return nil, false
	}
	return inst, true
}

// instanceLocked 找实例并确认这条连接已经进入它。
// 所有「改状态」的请求都走这里：没加入的旁观者不能凭一个 id 就去改别人的复盘。
func (h *Hub) instanceLocked(c *Client, req Request) (*Instance, bool) {
	inst, ok := h.findInstanceLocked(c, req)
	if !ok {
		return nil, false
	}
	if c.InstanceID != inst.ID {
		h.fail(c, req, "你还没进入这个实例（先 join_instance）")
		return nil, false
	}
	return inst, true
}

// leaveLocked 让连接退出所在实例。quiet=true 用于断开连接（那边不关心回执）。
func (h *Hub) leaveLocked(c *Client, quiet bool) {
	id := c.InstanceID
	if id == "" {
		return
	}
	c.InstanceID = ""
	inst, ok := h.instances[id]
	if !ok {
		return
	}
	if inst.Operator == c.Name {
		inst.Operator = ""
		h.broadcastInstanceLocked(inst, ActControlChanged, ControlChangedPayload{}, c.Name)
	}
	// 录制属于「这个人这一段操作」：他走了就结束并保存。
	// 不这么做的话，这个实例会永远停在「已经在录制了」，下一个人再也开不了新的录像。
	if inst.rec != nil && inst.rec.rec.CreatedBy == c.Name {
		if payload, ok := h.finishRecordingLocked(inst); ok {
			h.broadcastInstanceLocked(inst, ActRecordingStopped, payload, "system")
		}
	}
	h.broadcastMembersLocked(inst)
	// 没人看了就把机器人停掉：否则它会一直替一个空实例改数据。
	if len(h.membersLocked(inst)) == 0 && inst.botStop != nil {
		h.stopBotLocked(inst, "")
	}
	_ = quiet
}

func (h *Hub) deleteLocked(inst *Instance) {
	if inst.botStop != nil {
		h.stopBotLocked(inst, "")
	}
	// 删实例时正在录的那一段也要收尾：录制文件不属于实例存档，它是独立的一份录像，
	// 留着比丢掉有用（用户删的是「这个盘面」，不是「刚录的那段」）。
	if inst.rec != nil {
		h.finishRecordingLocked(inst)
	}
	inst.deleted = true
	delete(h.instances, inst.ID)
	for i, id := range h.order {
		if id == inst.ID {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
	for _, c := range h.clients {
		if c.InstanceID == inst.ID {
			c.InstanceID = ""
			h.reply(c, DeletedMsg{Type: MsgDeleted, InstanceID: inst.ID})
		}
	}
	// 目录要等索引写成功之后再删，见 persist.go 的 flushState
	if h.statePath != "" {
		h.pendingRemove = append(h.pendingRemove, instanceFolder(inst.ID))
	}
	h.markDirtyLocked()
	h.broadcastInstancesLocked()
	h.log("[replay] 实例 %s 已删除", inst.ID)
}

// Close 停掉所有机器人与连接（服务退出时调用），最后把状态写一次盘。
func (h *Hub) Close() {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return
	}
	h.closed = true
	for _, inst := range h.instances {
		if inst.botStop != nil {
			h.stopBotLocked(inst, "")
		}
		// 正在录的那一段要收尾并保存：Ctrl+C 不该把已经录完的十几步一起丢掉。
		if inst.rec != nil {
			h.finishRecordingLocked(inst)
		}
	}
	for _, c := range h.clients {
		if c.close != nil {
			c.close()
		}
	}
	h.clients = map[string]*Client{}
	done := h.stateDone
	saves := h.pendingSaves
	h.pendingSaves = nil
	h.mu.Unlock()

	// 先让定时器退出，再同步写一次：Ctrl+C 之后不该丢最后几秒的动作。
	if done != nil {
		close(done)
	}
	for _, s := range saves {
		h.saveRecording(s)
	}
	h.flushState()
}

// ---------------- 广播 ----------------

func (h *Hub) broadcastInstanceLocked(inst *Instance, action string, payload any, by string) {
	ev := inst.appendEvent(by, action, payload)
	// 录制挂在这里，而不是散在每个 do* 方法里：这条路径是所有状态变化的唯一出口
	// （真人、机器人、多人广播都走它），所以「机器人操作也能被录」不是另写一套，
	// 而是**挂对位置**的自然结果 —— 录下来的和操作记录 Tab 里看到的是同一批事件。
	if inst.rec != nil {
		inst.rec.observe(action, by, payload, ev.Seq)
	}
	// 广播 = 这个实例的状态变了（步进、标注、下单、操作权、成员）。落盘就打在这里，
	// 免得每个 do* 方法各记一次、漏一个就静默丢数据。
	h.markDirtyLocked(inst.ID)
	data, err := json.Marshal(EventMsg{
		Type:       MsgEvent,
		InstanceID: inst.ID,
		Seq:        ev.Seq,
		At:         ev.At,
		By:         by,
		Action:     action,
		Payload:    payload,
	})
	if err != nil {
		h.log("[replay] 序列化广播失败：%v", err)
		return
	}
	for _, c := range h.clients {
		if c.InstanceID == inst.ID {
			h.writeLocked(c, data)
		}
	}
}

func (h *Hub) broadcastMembersLocked(inst *Instance) {
	h.broadcastInstanceLocked(inst, ActMembersChanged, MembersChangedPayload{Members: h.membersLocked(inst)}, "system")
}

func (h *Hub) broadcastInstancesLocked() {
	data, err := json.Marshal(InstancesMsg{Type: MsgInstances, Instances: h.summariesLocked()})
	if err != nil {
		return
	}
	for _, c := range h.clients {
		h.writeLocked(c, data)
	}
}

func (h *Hub) sendStateLocked(c *Client, inst *Instance) {
	h.reply(c, InstanceStateMsg{
		Type:     MsgInstanceState,
		You:      You{ClientID: c.ID, Name: c.Name, Operator: inst.Operator == c.Name},
		Instance: inst.snapshot(h.membersLocked(inst)),
	})
}

func (h *Hub) reply(c *Client, msg any) {
	data, err := json.Marshal(msg)
	if err != nil {
		h.log("[replay] 序列化失败：%v", err)
		return
	}
	h.writeLocked(c, data)
}

func (h *Hub) writeLocked(c *Client, data []byte) {
	if c.write == nil {
		return
	}
	if err := c.write(data); err != nil {
		// 连接已经坏掉：标记后由 Handle/Unregister 收尾。这里**不能**直接关连接 ——
		// 关连接会让读循环回调 Hub，而 Hub 的锁现在正被自己拿着。
		h.garbage = append(h.garbage, c)
	}
}

func (h *Hub) fail(c *Client, req Request, msg string) {
	h.reply(c, ErrorMsg{Type: MsgError, Request: req.Type, Instance: req.InstanceID, Message: msg})
}

// ---------------- 摘要 / 成员 ----------------

func (h *Hub) summariesLocked() []InstanceSummary {
	out := make([]InstanceSummary, 0, len(h.instances))
	for _, id := range h.order {
		inst, ok := h.instances[id]
		if !ok {
			continue
		}
		out = append(out, inst.summary(len(h.membersLocked(inst))))
	}
	return out
}

func (h *Hub) membersLocked(inst *Instance) []Member {
	out := make([]Member, 0, 4)
	for _, c := range h.clients {
		if c.InstanceID != inst.ID {
			continue
		}
		out = append(out, Member{
			ClientID:  c.ID,
			Name:      c.Name,
			Operator:  inst.Operator == c.Name,
			Connected: true,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ClientID < out[j].ClientID })
	return out
}

// copies 复制一份切片，避免把内部状态以指针的形式交给编码器时被并发改动。
// 空切片也要返回 `[]` 而不是 null：前端拿 null 去 forEach 会炸。
func copies[T any](in []T) []T {
	out := make([]T, 0, len(in))
	return append(out, in...)
}

// Logf 供 ws 层打印（保持同一份日志出口）。
func (h *Hub) Logf(format string, args ...any) { h.log(format, args...) }
