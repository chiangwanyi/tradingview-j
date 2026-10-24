package replay

// 交易记录的生命周期（v0.18.0）：**自动开始、自动结束**。
//
// 一句话：仓位从空仓变成有仓的那一刻开始记，回到空仓的那一刻收尾。用户不需要
// 记得「点一下录制」—— 忘了开就不会有记录，忘了关就会录进一堆与本笔无关的操作，
// 这两个问题在自动触发之后都不存在了。
//
// 三条刻意的设计：
//
//  1. **Base 取「开仓成交之前」那一屏**。开仓是一笔成交，它本身也是一条广播；
//     如果等成交之后才开始记，Base 里就已经带着仓位，回放的第一帧就是「已经进场了」，
//     入场那一下永远看不到。所以 hub 在可能开仓的操作**之前**抓一份盘面快照
//     （captureTradeBaseLocked），成交之后再用它当第 1 步。
//  2. **结束条件只看仓位**。不看到底是谁平的、是不是同一根 K 线：仓位归零就收尾，
//     平仓那一笔**先记进去再收尾**（顺序见 trackTradeLocked），所以最后一笔成交
//     不会掉在记录外面。同一根 K 线里开了又平（往返）也会留下一条完整记录。
//  3. **记录挂在实例上、数据存在实例之外**。一笔交易属于「这个盘面上的这个仓位」，
//     但记录本身是独立的文件（<recordingsDir>/<id>/），和实例存档（<stateDir>/）
//     互不引用。删实例不删记录，删记录不影响实例 —— 两边各自完整。
//
// 边界情况（都有明确行为，不是"尽力而为"）：
//
//	实例被删除        当前这笔立即收尾并保存（Closed 按当时仓位如实记）
//	程序退出（Ctrl+C）同上，不留半截记录
//	进程崩溃          每两秒的检查点（checkpointTrades）保住到最后一刻；
//	                  重启后它作为一条「持仓中」的记录留在列表里
//	仓位跨实例         不存在：每个实例有自己的账本（Position / Trades），
//	                  同一个标的在两个实例上是两笔独立的交易、两条独立的记录
//	重启后仍有持仓     不续录（那笔交易的开头已经在崩溃前那次检查点里了），
//	                  下一次「空仓 → 有仓」才开新的一条

import (
	"encoding/json"
	"math"
	"time"
)

// statsTracker 边走边算一笔交易的统计（盈亏、最大不利/有利波动）。
//
// 为什么在录的时候算而不是收尾时重放：广播出去的那一刻 payload 还是**类型化的
// 结构体**（BarAdvancedPayload / OrderFilledPayload），成交价、持仓均价、最高最低价
// 都是现成的；等落盘之后再去 JSON 里翻，等于把已经知道的事实重新猜一遍。
//
// 口径见 protocol.go 的 TradeStats 注释。这里只强调一条：**Mfe/Mae 只统计
// 「有持仓且这一根没有成交」的整根 K 线**。一根 K 线只有 OHLC，成交发生在开盘、
// 盘中还是收盘看不出来，把有成交的那两根的高低点算进来等于替交易者挑了一个
// 最有利/最不利的时刻。
//
// 怎么做到「这一根没有成交」：`bar()` 先把每根持仓 K 线的两个极值记进 `bars`，
// 之后**如果在同一根 K 线上出现了成交**（比如在这根的收盘价市价平掉），`fill()`
// 就把刚记下的那一根撤掉 —— 那一根其实是有成交的，不能拿去算波动。
// 最终数字在 `snapshot()` 里现算（一笔交易几百根 K 线，重算一遍是微不足道的）。
type statsTracker struct {
	s       TradeStats
	pv      float64 // 每点价值（价格每走 1.0，一手值多少美元）
	lastQty float64 // 最近一次看到的持仓手数（判断是否平完）

	// bars 是「持仓期间完整走过、目前还没有成交」的 K 线各自的（有利, 不利）波动。
	// 只在 fill 时可能被弹掉最后一条（见那里），所以它天然按时间有序。
	bars []barExtreme
}

// barExtreme 是一根 K 线给这笔交易带来的有利/不利波动（美元，hi >= 0 >= lo 不保证，
// 两个数都可能同号 —— 比如全程浮亏时 hi 也是负的，最终取 max/min 时被 0 挡掉）。
type barExtreme struct {
	time string
	hi   float64
	lo   float64
}

func newStatsTracker(pv float64) statsTracker {
	if !(pv > 0) {
		pv = 1
	}
	return statsTracker{pv: pv}
}

// note 吃一条广播。只有会动仓位的两种动作有意义，其余直接忽略。
func (t *statsTracker) note(action string, payload any) {
	switch v := payload.(type) {
	case OrderFilledPayload:
		t.fill(v.Trade, v.Position)
	case BarAdvancedPayload:
		if len(v.Fills) > 0 {
			// 这一根里有成交：只记成交，不拿这根的高低点算波动（见类型注释）
			for _, f := range v.Fills {
				t.fill(f, v.Position)
			}
			return
		}
		t.bar(v.Bar, v.Position)
	}
}

// fill 记一笔成交。
//
// 第一笔成交定义「这一笔交易」的方向、手数和开仓价 —— 之后再怎么加减仓，
// 这四个字段只反映**进场那一下**（加减仓的手数看 MaxQuantity）。
func (t *statsTracker) fill(tr Trade, pos Position) {
	if tr.ID == "" {
		return
	}
	// 这一根里出现了成交 → 它不再是「没有成交的整根 K 线」，把刚记下的那一条撤掉。
	// 典型场景：在这根 K 线的收盘价市价平掉 —— 那根 K 线的高低点不该算进 MFE/MAE。
	if n := len(t.bars); n > 0 && tr.Time != "" && t.bars[n-1].time == tr.Time {
		t.bars = t.bars[:n-1]
	}
	if t.s.Fills == 0 {
		t.s.EntryTime = tr.Time
		t.s.EntryPrice = tr.Price
		t.s.Quantity = tr.Quantity
		switch tr.Side {
		case "buy":
			t.s.Side = "long"
		case "sell":
			t.s.Side = "short"
		}
	}
	t.s.ExitTime = tr.Time
	t.s.ExitPrice = tr.Price
	t.s.RealizedPnl += tr.RealizedPnl
	t.s.Fills++
	if q := math.Abs(pos.Quantity); q > t.s.MaxQuantity {
		t.s.MaxQuantity = q
	}
	t.lastQty = pos.Quantity
}

// bar 记一根「全程持仓、没有成交」的 K 线，把它的有利/不利波动先存着。
//
// 多头：high 侧是浮盈、low 侧是浮亏；空头反过来 —— 用带符号的持仓手数乘一下
// 就统一了，不用分支。
func (t *statsTracker) bar(b Bar, pos Position) {
	if pos.Quantity == 0 {
		return
	}
	t.lastQty = pos.Quantity
	up := (b.High - pos.AveragePrice) * pos.Quantity * t.pv
	dn := (b.Low - pos.AveragePrice) * pos.Quantity * t.pv
	t.bars = append(t.bars, barExtreme{time: b.Time, hi: math.Max(up, dn), lo: math.Min(up, dn)})
}

// snapshot 是「此刻的统计」。Mfe/Mae/BarsHeld 从记下的 K 线现算：
// 这样 fill 弹掉最后一根之后不需要做任何回滚算术，数字天然一致。
func (t *statsTracker) snapshot() TradeStats {
	s := t.s
	s.BarsHeld = len(t.bars)
	s.Mfe, s.Mae = 0, 0
	for _, b := range t.bars {
		if b.hi > s.Mfe {
			s.Mfe = b.hi
		}
		if b.lo < s.Mae {
			s.Mae = b.lo
		}
	}
	return s
}

// pointValueOf 是「价格每走 1.0，一手值多少美元」。
//
// 由 _symbols.json 里的 tickValue / tick 算出来，而不是另存一个乘数：
// MNQ 0.50/0.25 = 2 美元/点，MES 1.25/0.25 = 5，MGC 1.00/0.1 = 10。
// 没配置（或配置不成对）时退化成 1 美元/点 —— 至少数字还能互相比较。
func pointValueOf(tick, tickValue float64) float64 {
	if tick > 0 && tickValue > 0 {
		return tickValue / tick
	}
	return 1
}

// deriveStats 从**已经落盘的事件流**反推统计。
//
// 只给 v0.18.0 之前的记录用：那时候落盘的文件里没有 stats 字段，列表上直接显示
// 「+0.00」会让人以为算错了。反推用的是和实时路径同一套 statsTracker，
// 所以两种来源的口径天然一致。
func deriveStats(rec *Recording) TradeStats {
	t := newStatsTracker(pointValueOf(rec.Tick, rec.TickValue))
	for _, st := range rec.Steps {
		for _, ev := range st.Events {
			switch ev.Action {
			case ActOrderFilled:
				var p OrderFilledPayload
				if err := json.Unmarshal(ev.Payload, &p); err == nil {
					t.note(ev.Action, p)
				}
			case ActBarAdvanced:
				var p BarAdvancedPayload
				if err := json.Unmarshal(ev.Payload, &p); err == nil {
					t.note(ev.Action, p)
				}
			}
		}
	}
	t.s.Steps = len(rec.Steps)
	out := t.snapshot()
	out.Steps = len(rec.Steps)
	out.Closed = out.Fills > 0 && t.lastQty == 0
	return out
}

// ---------------------------------------------------------------------------
// 触发：开始 / 结束
// ---------------------------------------------------------------------------

// tradeBase 从实例上取「现在这一屏」的快照，作为交易记录的第 1 步。
//
// 只含**已揭示**的 K 线（和快照口径一致）：记录里不该出现当时还看不到的未来行情。
func tradeBase(inst *Instance) RecordingBase {
	cursor := inst.Cursor
	if cursor >= len(inst.Bars) {
		cursor = len(inst.Bars) - 1
	}
	if cursor < 0 {
		cursor = 0
	}
	bars := []Bar{}
	if len(inst.Bars) > 0 {
		bars = make([]Bar, cursor+1)
		copy(bars, inst.Bars[:cursor+1])
	}
	return RecordingBase{
		Cursor:        cursor,
		Bars:          bars,
		Annotations:   copies(inst.Annotations),
		Orders:        copies(inst.Orders),
		Trades:        copies(inst.Trades),
		ClosedLots:    inst.closedLots(),
		Position:      inst.position(),
		RealizedPnl:   inst.RealizedPnl,
		UnrealizedPnl: inst.unrealizedPnl(),
	}
}

// captureTradeBaseLocked 在「可能开仓」的操作**之前**抓一份快照。
//
// 三个前置条件，缺一不可：
//
//	还没在记（inst.trade == nil）—— 在记的时候 Base 早就定了
//	当前是空仓                  —— 有仓的时候不可能「开始一笔新交易」
//	有 K 线                      —— 空实例连第 1 步的时间都填不出来
//
// 抓到的快照挂在 inst.pendingBase 上，由紧随其后的那条广播消费掉
// （见 trackTradeLocked）。它是**每次可能开仓的操作都重抓一遍**的：
//
//	操作失败（比如已经到最后一根 K 线）—— do* 里会把它清掉
//	这次没成交、下一次才成交     —— 重抓，否则第 1 步会停留在几根之前的那一屏，
//	                              回放从更早的游标开始演，步数和盘面对不上
func (h *Hub) captureTradeBaseLocked(inst *Instance) {
	if inst == nil || inst.deleted || inst.trade != nil {
		return
	}
	if inst.Position.Quantity != 0 || len(inst.Bars) == 0 {
		return
	}
	base := tradeBase(inst)
	inst.pendingBase = &base
}

// tradeOpened 判断这一条广播是不是「把空仓变成有仓」的那一步。
//
// 只有两条路会改持仓：市价单成交（order_filled）和推进 K 线时撮合挂单
// （bar_advanced 里的 Fills）。其余广播一律不可能开仓，不必白抓快照。
func tradeOpened(action string, payload any) bool {
	switch action {
	case ActOrderFilled:
		return true
	case ActBarAdvanced:
		if p, ok := payload.(BarAdvancedPayload); ok {
			return len(p.Fills) > 0
		}
	}
	return false
}

// startTradeLocked 用 pendingBase 开一段新记录。
func (h *Hub) startTradeLocked(inst *Instance, base RecordingBase, by string) {
	if h.records == nil {
		return
	}
	id := h.records.NewID(time.Now())
	// 先把目录建出来：记录中每录一条语音就写进去，语音浮窗的预览播放靠它。
	folder, err := h.records.MarkLive(id)
	if err != nil {
		h.log("[replay] 交易记录目录不可用，这条记录的语音只会留在内存里：%v", err)
	}
	inst.trade = newTradeRecorder(inst, base, by, id, folder, h.log)
	h.log("[replay] 实例 %s 开仓，开始记交易 %s（第 1 步 %s）", inst.ID, id, base.LastTime())
}

// LastTime 是快照最后一根 K 线的时间（空快照给空串）。
func (b RecordingBase) LastTime() string {
	if len(b.Bars) == 0 {
		return ""
	}
	return b.Bars[len(b.Bars)-1].Time
}

// trackTradeLocked 在**每条广播之后**决定这段记录该怎么走：开始 / 继续 / 收尾。
//
// 顺序是刻意的：先记这一步，再判断要不要收尾。反过来的话，平仓那一笔成交会
// 掉在记录外面 —— 而那恰恰是这笔交易最该被记住的一步。
//
// 调用点在 hub.broadcastInstanceLocked 的末尾（广播已经发出去之后）：这条路径是
// 所有状态变化的唯一出口，真人、机器人、多人广播全走它，所以「机器人交易也会被记」
// 是挂对位置的副产品，不是另写的一套。
func (h *Hub) trackTradeLocked(inst *Instance, action string, payload any, by string) {
	if inst == nil || inst.deleted {
		return
	}
	flat := inst.Position.Quantity == 0
	if inst.trade == nil {
		base := inst.pendingBase
		inst.pendingBase = nil
		if base == nil || base.Position.Quantity != 0 || !tradeOpened(action, payload) {
			return
		}
		h.startTradeLocked(inst, *base, by)
	}
	inst.trade.observe(action, by, payload, inst.stepContext())
	if flat {
		h.finishTradeLocked(inst, by)
	}
}

// finishTradeLocked 给当前这笔收尾，并排进「锁外写盘」的队列。
//
// 返回 false = 现在没有在记。写盘不在锁内做（那是 IO，会把所有人的步进一起卡住），
// 所以这里只负责把记录封好、塞进 h.pendingSaves；Handle / Unregister / Close
// 的 defer 会在解锁之后真正落盘，并在成功后通知前端刷新列表。
func (h *Hub) finishTradeLocked(inst *Instance, by string) (RecordingSummary, bool) {
	if inst == nil || inst.trade == nil {
		return RecordingSummary{}, false
	}
	r := inst.trade
	inst.trade = nil
	inst.pendingBase = nil
	closed := inst.Position.Quantity == 0

	rec := r.finish(closed)
	rec.InstanceID = inst.ID
	audios := append([]pendingAudio(nil), r.audios...)
	h.pendingSaves = append(h.pendingSaves, pendingSave{
		rec:      rec,
		audios:   audios,
		instance: inst.ID,
	})
	h.log("[replay] 实例 %s 的交易记录 %s 收尾（%s，%s %.0f 手，%d 步 / %+.2f）",
		inst.ID, rec.ID, by, rec.Stats.Side, rec.Stats.Quantity, rec.StepCount, rec.Stats.RealizedPnl)
	return rec.RecordingSummary, true
}

// pendingSave 是一条等待写盘的交易记录（含语音字节）。
type pendingSave struct {
	rec      *Recording
	audios   []pendingAudio
	instance string // 通知发给这个实例里的连接（空 = 不发）
}

// saveTrade 在锁外把一条记录写进磁盘，然后通知前端。
//
// 「保存成没成」必须说清楚：没配目录、磁盘满都要让用户知道，但不能因为写不进去
// 就把已经记完的内容一起丢掉（数据还在内存里，下一次收尾/检查点还会再试）。
func (h *Hub) saveTrade(s pendingSave) {
	if s.rec == nil {
		return
	}
	if h.records == nil || !h.records.Enabled() {
		h.notifyTradeRecorded(s.instance, s.rec.RecordingSummary,
			"没有配置交易记录目录（config.json 的 replayRecordingDir），这条记录没有落盘")
		return
	}
	sum, err := h.records.Save(s.rec, s.audios)
	if err != nil {
		h.log("[replay] 交易记录 %s 没保存成：%v", s.rec.ID, err)
		// 没有 recording.json 的目录在列表里看不见，留着谁也认不出来
		h.records.Drop(s.rec.ID)
		h.notifyTradeRecorded(s.instance, s.rec.RecordingSummary, "写盘失败："+err.Error())
		return
	}
	h.log("[replay] 交易记录已保存：%s（%d 步 / %d 条语音 / %d 个操作 / %d KB）→ %s",
		sum.ID, sum.StepCount, sum.AudioCount, sum.EventCount, sum.Size>>10, h.records.Dir())
	h.notifyTradeRecorded(s.instance, sum, "")
}

// notifyTradeRecorded 把「这笔交易记完了」告诉还在这个实例里的人。
//
// 注意它**不是**一条 event 广播：交易记录是独立于实例的数据集，它不该出现在
// 「操作记录」Tab 里，也不该让前端进入某种「正在录制」的状态。
func (h *Hub) notifyTradeRecorded(instanceID string, sum RecordingSummary, warning string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	// 收尾的原因可能是「实例被删了」：那一刻实例已经从 h.instances 里消失，
	// 按 id 找连接会一个人都找不到。但这条记录确实刚被保存，用户还等着它在列表里
	// 出现 —— 所以退化成广播给所有人（前端收到只会刷新一次列表，没有副作用）。
	if instanceID != "" {
		if _, ok := h.instances[instanceID]; !ok {
			instanceID = ""
		}
	}
	data, err := json.Marshal(TradeRecordedMsg{
		Type:      MsgTradeRecorded,
		Recording: sum,
		Dir:       h.records.Dir(),
		Warning:   warning,
	})
	if err != nil {
		return
	}
	for _, c := range h.clients {
		if instanceID == "" || c.InstanceID == instanceID {
			h.writeLocked(c, data)
		}
	}
}

// checkpointTrades 把「正在记的那几笔」每两秒写一次盘。
//
// 自动记录带来一个副作用：一笔交易可能持续几十分钟甚至过夜，只在收尾时落盘的话，
// 一次崩溃就把整段操作丢光了。所以只要这一轮有新内容（dirty），就把当前这份写成
// 草稿 —— 记录里长什么样就是什么样（Stats.Closed 还是 false），重启后它作为一条
// 「持仓中」的记录留在列表里，不会被当成坏文件清掉。
//
// 序列化必须在**锁内**做完：副本里的 Steps / Base 切片和正在推进的那一份是同一份
// 内存，锁外再 marshal 就会撞上并发写。写盘（IO）仍然在锁外。
func (h *Hub) checkpointTrades() {
	if h.records == nil || !h.records.Enabled() {
		return
	}
	type draft struct {
		inst string
		sum  RecordingSummary
		data []byte
	}
	var list []draft
	h.mu.Lock()
	for _, id := range h.order {
		inst, ok := h.instances[id]
		if !ok || inst.trade == nil || !inst.trade.dirty {
			continue
		}
		inst.trade.dirty = false
		sum, cp := inst.trade.draft()
		buf, err := json.Marshal(cp)
		if err != nil {
			h.log("[replay] 交易记录 %s 的检查点序列化失败：%v", sum.ID, err)
			continue
		}
		list = append(list, draft{inst: id, sum: sum, data: buf})
	}
	h.mu.Unlock()

	for _, d := range list {
		if err := h.records.SaveDraft(d.sum, d.data); err != nil {
			h.log("[replay] 交易记录检查点没写成（%s）：%v", d.sum.ID, err)
			// 写失败就把脏标记放回去：否则这一次的改动要等下一次操作才有机会落盘，
			// 而用户可能正好在这之后关掉程序。
			h.mu.Lock()
			if inst, ok := h.instances[d.inst]; ok && inst.trade != nil {
				inst.trade.dirty = true
			}
			h.mu.Unlock()
		}
	}
}

// TradeRecordCount 是「正在记录的笔数」（0 或 1，每个实例最多一笔）。
// 只给测试和日志用：界面上不该出现「正在记录」这个概念。
func (inst *Instance) TradeRecordCount() int {
	if inst != nil && inst.trade != nil {
		return 1
	}
	return 0
}
