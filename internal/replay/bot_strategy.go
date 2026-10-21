package replay

// 策略机器人：把「策略」和「执行」接起来的那一层。
//
// 分工：
//
//	策略（internal/strategy）  看着已经收盘的 K 线，说「现在该做多了，止损在这里、目标在这里」
//	这里                        真的下单、挂保护单、盯成交、日终平仓、换日清仓
//
// 为什么不让策略自己下单：策略会因此拿到 Instance，测试要起 Hub 才能跑，
// 而且「策略自己的成交价」和「复盘台的撮合」迟早会分叉。这里让策略只出信号，
// 所有成交都走 doMarketOrderLocked / doPlaceOrderLocked —— 和真人点按钮同一条路径，
// 所以跳空取开盘价、只撮合上一根之前的单，这些规矩一条都不会漏。
//
// 一个交易日内的完整循环（每推进一根 K 线走一遍）：
//
//	步进一根   ->  advance 撮合上一根挂着的止损/止盈单（bot 不用管价格怎么穿）
//	跨日兜底   ->  日历日变了还有仓位：上一日漏了 23:55，立刻平（吃掉隔夜跳空）
//	换日检查   ->  交易日变了：清仓 + 撤残留保护单（绝不隔夜）
//	同步保护单 ->  止损或止盈成交了，撤掉另一张（这就是 OCO）
//	23:55 收摊 ->  日历日最后一根：**无条件平仓 + 撤光所有挂单**，本根不问策略
//	结算收尾   ->  04:50 之后：清仓，不再开新仓
//	空仓才问策略 ->  有信号就市价开仓 + 挂止损 + 挂止盈
//
// 「23:55 收摊」是**全局硬约束**：不管哪个策略、多大手数、什么方向，一律在
// 日历日最后一根上被平掉，任何仓位都不留过夜。判据只看 K 线时间（CSV 里的本地
// 明文，就是北京时间），不做时区换算。
//
// 每一单的成交价都来自复盘台自己的撮合：**零滑点、零手续费**，市价单按当根收盘价成交。
// 这是需求里明确的假设，也意味着界面上跑出来的盈亏是「最有利情况」，不是实盘结果。

import (
	"errors"

	"tradingview-j/internal/strategy"
)

const (
	// 策略机器人默认 300ms 走一根：一天 276 根约 85 秒跑完，眼睛跟得上。
	// 随机演示机器人是 1200ms —— 它要留时间让人看清每一个乱画的动作。
	botStrategyDefaultIntervalMs = 300
	botStrategyMinIntervalMs     = 60
	botStrategyMaxIntervalMs     = 5000
)

// strategyRun 是策略机器人**一次运行**的全部状态。
//
// 每次 bot_start 新建一个，bot_stop 丢弃。它不落盘：机器人是进程内的运行状态，
// 重启进程后不会自己接着跑（和老的演示机器人一致）。
type strategyRun struct {
	strat strategy.Strategy
	qty   float64

	// bars 是已揭示 K 线的镜像，增量追加（只追加，不回退）。
	// 每次把整串重新转一遍是 O(n²)，一天 276 根时看不出来，跑满一年就是白费。
	// ⚠️ 传给策略的切片会被复用，策略不能保存它的引用。
	bars []strategy.Bar

	day string // 当前交易日（06:00 口径）；换日时清仓

	// calDay 是当前**日历日**（CSV 文件那一天）。和 day 差 6 小时：
	// 23:55 是日历日的最后一根，06:00 才是交易日的开始。跨日历日时兜底平仓。
	calDay string

	// 这一单的两张保护单。空串 = 没有。
	// 只认自己记下的编号：盘面上用户手动挂的单，机器人一律不碰。
	stopOrder   string
	targetOrder string

	entries int // 这次运行一共开了几单（只用于日志）
}

func newStrategyRun(s strategy.Strategy, qty float64) *strategyRun {
	return &strategyRun{strat: s, qty: qty}
}

// revealedBars 把实例里已揭示的部分同步进 run.bars，返回可以直接交给策略的切片。
func (run *strategyRun) revealedBars(inst *Instance) []strategy.Bar {
	need := inst.Cursor + 1
	if need > len(inst.Bars) {
		need = len(inst.Bars)
	}
	for len(run.bars) < need {
		b := inst.Bars[len(run.bars)]
		run.bars = append(run.bars, strategy.Bar{
			Time:   b.Time,
			Open:   b.Open,
			High:   b.High,
			Low:    b.Low,
			Close:  b.Close,
			Volume: b.Volume,
		})
	}
	return run.bars[:need]
}

// botStrategyTickLocked 是策略机器人的一拍：推进一根，然后把上面那七步走一遍。
func (h *Hub) botStrategyTickLocked(inst *Instance, run *strategyRun) {
	// 1. 推进一根。这一步顺便撮合了上一根挂着的止损/止盈 —— 不需要机器人自己判价格。
	if err := h.doStepLocked(inst, "bot"); err != nil {
		if errors.Is(err, ErrNoMoreBars) {
			// 数据到头就当收盘：先把仓位平掉再停。留着一个永远不会变的持仓，
			// 用户既等不到它成交，也判断不出这算不算「还在跑」。
			if inst.Position.Quantity != 0 {
				h.botClosePositionLocked(inst, run)
			}
			run.cancelProtectionLocked(h, inst)
			h.log("[replay] 策略机器人跑到数据末尾，平仓后停止（实例 %s，共 %d 单）", inst.ID, run.entries)
			h.stopBotLocked(inst, "")
			h.broadcastInstancesLocked()
		}
		return
	}

	bar := inst.currentBar()
	day := strategy.TradeDay(bar.Time)
	cal := strategy.CalendarDay(bar.Time)

	// 2. 跨日历日兜底（正常数据走不到这里，见第 5 步）。
	//     如果上一日没能在 23:55 平掉（那天缺了最后一根、或者当时平仓失败），
	//     时间已经跨过午夜，这一刻必须无条件清空 —— 会吃一次隔夜跳空，
	//     但比「留着仓位继续跑」诚实：那是真的过夜了，就该按过夜的代价算。
	//
	//     清完就 return，不在同一根上再开新仓：这是异常路径，先把账面擦干净，
	//     想接着做就等下一根（和 23:55 的处理保持同一个形状）。
	crossed := run.calDay != "" && cal != "" && cal != run.calDay
	run.calDay = cal
	if crossed && inst.Position.Quantity != 0 {
		h.closeOutLocked(inst, run, "跨日兜底：上一日没能按 23:55 平仓")
		return
	}

	// 3. 换交易日：上一日的仓位和残留保护单必须清掉。
	//     这一步和「23:55 日历日收盘」「04:50 结算收尾」是三道路闸 —— 万一某天的数据
	//     缺了最后一根，靠这里也不会把仓位带过夜。
	if run.day != "" && day != run.day {
		run.cancelProtectionLocked(h, inst)
		if inst.Position.Quantity != 0 {
			h.botClosePositionLocked(inst, run)
		}
	}
	run.day = day

	// 4. 止损/止盈成交之后撤掉另一张。放在开新仓之前：同一根里刚平掉的仓
	//    不能还被当成「有仓位」，否则下一个信号要等到下一根。
	run.syncProtectionLocked(h, inst)

	// 5. 日历日收盘（23:55 那根）：**无条件平仓 + 撤掉所有未成交挂单**，并且本根不问策略。
	//
	//     为什么 return 而不是继续往下走：下面第 7 步会问策略要不要开仓，
	//     23:55 开的仓没人接、必然留过夜 —— 刚平完又开一张是最荒谬的结果。
	//     为什么撤「所有」挂单而不只是自己那两张：残留的任何一张（用户睡前的挂单、
	//     以前某次运行漏下的）都可能在 00:00 之后被触发，凭空开出一个过夜仓。
	if strategy.DayClose(bar.Time) {
		h.closeOutLocked(inst, run, "23:55 日历日收盘")
		return
	}

	// 6. 结算收尾时段（04:50~05:55）：清仓，不再开新仓。
	if strategy.SessionEnd(bar.Time) {
		h.closeOutLocked(inst, run, "04:50 结算收尾")
		return
	}

	// 7. 有仓位就不问策略。策略自己也只在空仓时出信号（双保险）：
	//    万一以后有个策略忘了判断，也不会在同一个方向上叠成一堆。
	if inst.Position.Quantity != 0 {
		return
	}

	ctx := &strategy.Context{
		Bars:     run.revealedBars(inst),
		Cursor:   inst.Cursor,
		Tick:     inst.Tick,
		Position: strategy.Position{Quantity: inst.Position.Quantity, AveragePrice: inst.Position.AveragePrice},
	}
	sigs := run.strat.OnBar(ctx)
	if len(sigs) == 0 {
		return
	}
	// 一根 K 线最多开一单：策略返回多个信号时只认第一个，
	// 否则同一根柱子上会把同一个理由做成好几笔重复的仓。
	h.botEnterLocked(inst, run, sigs[0], bar)
}

// closeOutLocked 是「收摊」的唯一实现：市价平掉仓位 + 撤掉盘面上**所有**还挂着的单。
//
// 三处都调它（23:55 日历日收盘、跨日兜底、04:50 结算收尾），保证口径只有一份：
// 不会出现「23:55 撤了保护单但漏了用户挂的那张」「04:50 只平仓不撤单」这类分叉。
//
// 平仓价是**当根 K 线的收盘价**（复盘台的市价单口径，零滑点）：23:55 那根的 close
// 就是 00:00 之前的最后一个成交价，所以「23:55 收盘时平掉」在数据上就是这一格。
func (h *Hub) closeOutLocked(inst *Instance, run *strategyRun, reason string) {
	if inst.Position.Quantity != 0 {
		h.log("[replay] %s：市价平掉 %.0f 手（实例 %s）", reason, inst.Position.Quantity, inst.ID)
		h.botClosePositionLocked(inst, run) // 里面会撤掉两张保护单
	} else {
		run.clearProtection()
	}
	h.cancelAllOrdersLocked(inst, "bot")
}

// cancelAllOrdersLocked 撤掉这个实例上所有还挂着的单。
//
// 先收集编号再逐张撤：doCancelOrderLocked 会改 inst.Orders 里的状态（并广播），
// 边遍历边改会让断言和回调都变得不好推理。
func (h *Hub) cancelAllOrdersLocked(inst *Instance, by string) {
	var ids []string
	for i := range inst.Orders {
		if inst.Orders[i].Status == OrderOpen {
			ids = append(ids, inst.Orders[i].ID)
		}
	}
	for _, id := range ids {
		if err := h.doCancelOrderLocked(inst, by, id); err != nil {
			// 撤不掉（已经被撮合掉了之类）不算错误，记一行就够 —— 关键是别静默。
			h.log("[replay] 收摊时撤单 %s 失败：%v", id, err)
		}
	}
}

// botEnterLocked 按信号开仓，并挂上止损和止盈两张保护单。
//
// 开仓用市价单（按当前收盘价成交）而不是挂单：策略的入场定义就是「触发棒收盘时进」，
// 挂单要等下一根才可能成交，那已经是另一个价位了。
func (h *Hub) botEnterLocked(inst *Instance, run *strategyRun, sig strategy.Signal, at Bar) {
	if sig.Side != "buy" && sig.Side != "sell" {
		h.log("[replay] 策略给了不认识的方向 %q，忽略", sig.Side)
		return
	}
	side := sig.Side
	if _, tr, err := h.doMarketOrderLocked(inst, "bot", side, run.qty); err != nil {
		h.log("[replay] 策略开仓失败：%v", err)
		return
	} else if tr.ID == "" {
		return
	}

	closeSide := "sell"
	if side == "sell" {
		closeSide = "buy"
	}

	// 两张保护单用同一个 OCO 组。
	//
	// 这是必须的：5 分钟 K 线只有最高/最低价，看不出这根里先摸到止损还是先摸到止盈。
	// 不打组的话，一根长影线同时穿过两边时，止损先成交（仓位归零），止盈接着成交
	// （仓位变成 -1），凭空多出一个没人管的反向仓 —— 实测真实数据里一整天能出现好几次。
	// 打上组之后，同根只成交先挂的那张（止损），另一张自动撤销。
	group := inst.nextGroupID()

	// 止损：stop 单。价格在开仓价的哪一侧由策略给定，这里只负责「反方向平掉」。
	stop, err := h.doPlaceOrderGroupedLocked(inst, "bot", closeSide, KindStop, sig.Stop, run.qty, group)
	if err != nil {
		// 挂不上止损就不该留着仓位裸奔 —— 立刻平掉，当作这单没做。
		h.log("[replay] 挂止损失败，撤掉刚开的仓：%v", err)
		h.botClosePositionLocked(inst, run)
		return
	}
	target, err := h.doPlaceOrderGroupedLocked(inst, "bot", closeSide, KindLimit, sig.Target, run.qty, group)
	if err != nil {
		h.log("[replay] 挂止盈失败，撤掉止损后平仓：%v", err)
		_ = h.doCancelOrderLocked(inst, "bot", stop.ID)
		h.botClosePositionLocked(inst, run)
		return
	}

	run.stopOrder = stop.ID
	run.targetOrder = target.ID
	run.entries++
	h.log("[replay] 策略开仓 %s %.0f 手 @ %v（%s），止损 %v，目标 %v",
		side, run.qty, at.Close, sig.Reason, sig.Stop, sig.Target)
}

// syncProtectionLocked 处理保护单的售后台账（OCO + 裸仓兜底）。
//
// 三件事，顺序不能换：
//  1. 止损单不在了而仓位还在 —— 保护失效（被人撤了/异常），立刻平仓，绝不裸着；
//  2. 任意一张不在了（成交了） —— 撤掉另一张，否则剩下那张会在平仓之后又开出一个反向仓；
//  3. 仓位已经没了但两张还挂着 —— 人工干预过，撤掉，别让它们继续留在盘面上。
func (run *strategyRun) syncProtectionLocked(h *Hub, inst *Instance) {
	if run.stopOrder == "" && run.targetOrder == "" {
		return
	}
	stopOpen := orderIsOpen(inst, run.stopOrder)
	targetOpen := orderIsOpen(inst, run.targetOrder)

	if run.stopOrder != "" && !stopOpen {
		if inst.Position.Quantity != 0 {
			h.log("[replay] 止损单不在了但仓位还在，立刻平仓（实例 %s）", inst.ID)
			h.botClosePositionLocked(inst, run)
			return
		}
		if targetOpen {
			_ = h.doCancelOrderLocked(inst, "bot", run.targetOrder)
		}
		run.clearProtection()
		return
	}
	if run.targetOrder != "" && !targetOpen {
		if stopOpen {
			_ = h.doCancelOrderLocked(inst, "bot", run.stopOrder)
		}
		run.clearProtection()
		return
	}
	if inst.Position.Quantity == 0 && (stopOpen || targetOpen) {
		if stopOpen {
			_ = h.doCancelOrderLocked(inst, "bot", run.stopOrder)
		}
		if targetOpen {
			_ = h.doCancelOrderLocked(inst, "bot", run.targetOrder)
		}
		run.clearProtection()
	}
}

// botClosePositionLocked 市价平掉当前仓位，并撤掉保护单。
func (h *Hub) botClosePositionLocked(inst *Instance, run *strategyRun) {
	q := inst.Position.Quantity
	if q == 0 {
		return
	}
	side := "sell"
	qty := q
	if q < 0 {
		side = "buy"
		qty = -q
	}
	if _, _, err := h.doMarketOrderLocked(inst, "bot", side, qty); err != nil {
		h.log("[replay] 策略平仓失败：%v", err)
		return
	}
	run.cancelProtectionLocked(h, inst)
}

// cancelProtectionLocked 撤掉这次运行挂的两张保护单（还挂着的那几张）。
//
// 只撤自己记下的编号：用户手动挂的单一张都不碰。撤不掉（已经成交/已经撤过）
// 不算错误，安静跳过 —— 这个函数在停止机器人、换日、平仓三条路径上都会被调到。
func (run *strategyRun) cancelProtectionLocked(h *Hub, inst *Instance) {
	for _, id := range []string{run.stopOrder, run.targetOrder} {
		if id != "" && orderIsOpen(inst, id) {
			if err := h.doCancelOrderLocked(inst, "bot", id); err != nil {
				h.log("[replay] 撤保护单 %s 失败：%v", id, err)
			}
		}
	}
	run.clearProtection()
}

func (run *strategyRun) clearProtection() {
	run.stopOrder = ""
	run.targetOrder = ""
}

// orderIsOpen 判断某张单还挂着。编号为空、或者找不到，都算「不在了」。
func orderIsOpen(inst *Instance, id string) bool {
	if id == "" {
		return false
	}
	for i := range inst.Orders {
		if inst.Orders[i].ID == id {
			return inst.Orders[i].Status == OrderOpen
		}
	}
	return false
}

// ---------------- 策略清单 ----------------

// handleListStrategies 把注册表里的策略转成协议格式发下去。
func (h *Hub) handleListStrategies(c *Client) {
	h.reply(c, StrategiesMsg{Type: MsgStrategies, Strategies: strategyInfos()})
}

// strategyInfos 是「注册表 -> 协议」的唯一转换点。
// 单独抽出来是为了让测试能直接断言清单内容，不用连 WS。
func strategyInfos() []StrategyInfo {
	infos := strategy.List()
	out := make([]StrategyInfo, 0, len(infos))
	for _, s := range infos {
		params := make([]StrategyParam, 0, len(s.Params))
		for _, p := range s.Params {
			params = append(params, StrategyParam{
				Key:     p.Key,
				Label:   p.Label,
				Default: p.Default,
				Min:     p.Min,
				Max:     p.Max,
				Step:    p.Step,
				Help:    p.Help,
			})
		}
		out = append(out, StrategyInfo{
			ID:      s.ID,
			Name:    s.Name,
			Summary: s.Summary,
			Params:  params,
		})
	}
	return out
}

// strategyName 按 id 找策略的中文名（日志和广播里用）；找不到就回原 id。
func strategyName(id string) string {
	for _, info := range strategy.List() {
		if info.ID == id {
			return info.Name
		}
	}
	return id
}

// strategyQuantity 把手数收敛到合法范围：0 或负数按 1 手，小数直接拒。
// 复用 normalizeQuantity，保证和下单框用的是同一套规则。
func strategyQuantity(qty float64) (float64, error) {
	if qty <= 0 {
		return 1, nil
	}
	return normalizeQuantity(qty)
}
