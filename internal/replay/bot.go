package replay

// 机器人：一个只会「做操作」的假用户，用来验证 K 线步进、标注、模拟交易这三条链路。
//
// 它**不走任何后门**：和真人一样经过「操作权 -> 校验 -> 改状态 -> 广播」，
// 广播里 by = "bot"。所以只要机器人能跑通，真人点按钮就一定跑得通；
// 反过来，机器人报错也说明真人会遇到同样的错。
//
// 启动机器人时它接管操作权（真的接管，界面会显示"机器人操作中"），
// 这样一个人开着页面就能看演示 —— 正是需求里「支持演示模式观看他人操作」。

import (
	"fmt"
	"math"
	"strings"
	"time"

	"tradingview-j/internal/strategy"
)

const (
	botDefaultIntervalMs = 1200
	botMinIntervalMs     = 150
	botMaxIntervalMs     = 10000
	botMaxAnnotations    = 40
	botMaxOpenOrders     = 12
)

var botColors = []string{"#f59e0b", "#0ea5e9", "#ec4899", "#65a30d"}

func (h *Hub) handleBotStart(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if inst.botStop != nil {
		h.fail(c, req, "机器人已经在跑了")
		return
	}
	if inst.Operator != "" && inst.Operator != c.Name {
		h.fail(c, req, fmt.Sprintf("现在由 %s 操作，机器人起不来；等他交还或退出", inst.Operator))
		return
	}

	// 先把策略造出来再接管操作权：参数不合法（策略 id 打错、手数是小数）就直接报错。
	// 反过来先接管再失败的话，界面上会显示「机器人操作中」，实际什么都不会发生，
	// 用户只能干等 —— 这种「看起来在跑其实没跑」的状态是最难排查的。
	var run *strategyRun
	payload := BotPayload{}
	defaultInterval, minInterval, maxInterval := botDefaultIntervalMs, botMinIntervalMs, botMaxIntervalMs
	if id := strings.TrimSpace(req.Strategy); id != "" {
		strat, err := strategy.New(id, strategy.Values(req.Params))
		if err != nil {
			h.fail(c, req, err.Error())
			return
		}
		qty, err := strategyQuantity(req.Quantity)
		if err != nil {
			h.fail(c, req, err.Error())
			return
		}
		run = newStrategyRun(strat, qty)
		payload.Strategy = strat.ID()
		payload.Name = strat.Name()
		payload.Quantity = qty
		// 策略机器人跑得比演示快：一天 276 根，300ms 一根约 85 秒。
		defaultInterval, minInterval, maxInterval = botStrategyDefaultIntervalMs, botStrategyMinIntervalMs, botStrategyMaxIntervalMs
	}

	interval := clampBotInterval(req.IntervalMs, defaultInterval, minInterval, maxInterval)
	payload.IntervalMs = interval

	stop := make(chan struct{})
	inst.botStop = stop
	inst.botInterval = time.Duration(interval) * time.Millisecond
	inst.Operator = "bot"
	inst.run = run
	inst.botStrategy = payload.Strategy
	inst.botQty = payload.Quantity

	h.broadcastInstanceLocked(inst, ActControlChanged, ControlChangedPayload{Operator: "bot"}, c.Name)
	h.broadcastInstanceLocked(inst, ActBotStarted, payload, c.Name)
	h.broadcastInstancesLocked()
	if run != nil {
		h.log("[replay] 策略机器人 %s 接管实例 %s：每笔 %.0f 手，每 %dms 推进一根",
			payload.Name, inst.ID, payload.Quantity, interval)
	} else {
		h.log("[replay] 随机演示机器人接管实例 %s，每 %dms 动作一次", inst.ID, interval)
	}

	go h.runBot(inst, stop, inst.botInterval)
}

// clampBotInterval 收敛步进节奏。两种机器人的上下限不同（演示要慢、策略要快），
// 所以区间由调用方传入，而不是在这里按类型分支。
func clampBotInterval(v, def, lo, hi int) int {
	if v <= 0 {
		return def
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func (h *Hub) handleBotStop(c *Client, req Request) {
	inst, ok := h.instanceLocked(c, req)
	if !ok {
		return
	}
	if inst.botStop == nil {
		h.fail(c, req, "机器人没在跑")
		return
	}
	// 操作权在机器人手上时，任何人（实例成员）都可以叫停；在别人手上时不行。
	if inst.Operator != "bot" && inst.Operator != c.Name {
		h.fail(c, req, "现在不是你操作，也不是机器人在跑")
		return
	}
	// 停掉之后操作权交回给叫停的人：他刚在看着，接着自己操作最自然。
	h.stopBotLocked(inst, c.Name)
	h.broadcastInstancesLocked()
}

// stopBotLocked 停机器人并把操作权交给 restore（空 = 谁都不给）。
func (h *Hub) stopBotLocked(inst *Instance, restore string) {
	if inst.botStop == nil {
		return
	}

	// 停之前撤掉这次运行挂的保护单。**仓位不平**：用户可能只是想暂停、自己接手接着做，
	// 界面上的持仓和盈亏都还在，他看得见。但保护单必须撤 —— 它记在这次运行的编号里，
	// 不撤就会在盘面上留下两张没人管的挂单，下次启动机器人时又挂两张，越积越多。
	if inst.run != nil {
		inst.run.cancelProtectionLocked(h, inst)
	}

	payload := BotPayload{
		IntervalMs: int(inst.botInterval / time.Millisecond),
		Strategy:   inst.botStrategy,
		Quantity:   inst.botQty,
	}
	if payload.Strategy != "" {
		payload.Name = strategyName(payload.Strategy)
	}

	close(inst.botStop)
	inst.botStop = nil
	inst.botInterval = 0
	inst.botStrategy = ""
	inst.botQty = 0
	inst.run = nil
	inst.Operator = restore
	h.broadcastInstanceLocked(inst, ActBotStopped, payload, "bot")
	h.broadcastInstanceLocked(inst, ActControlChanged, ControlChangedPayload{Operator: restore}, "bot")
}

func (h *Hub) runBot(inst *Instance, stop chan struct{}, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			h.mu.Lock()
			// 实例被删、或者机器人已经被换过一轮：这个循环该退出了。
			if inst.deleted || inst.botStop != stop {
				h.mu.Unlock()
				return
			}
			h.botTickLocked(inst)
			h.mu.Unlock()
		}
	}
}

// botTickLocked 每拍做一件事。
//
// 两种机器人共用同一个循环和同一把锁，区别只在「这一拍干什么」：
// 随机演示机器人抽签决定动作；策略机器人固定「推进一根 → 让策略看一眼」。
func (h *Hub) botTickLocked(inst *Instance) {
	if inst.run != nil {
		h.botStrategyTickLocked(inst, inst.run)
		return
	}
	// 随机挑一个动作执行。权重刻意偏向「步进」：
	// 演示时最想看到的是行情一根根往前走，标注和下单是穿插着发生的。
	switch n := h.rnd.Intn(100); {
	case n < 50:
		// 走到数据末尾是正常的：机器人接着画标注、挂单，不再步进。
		_ = h.doStepLocked(inst, "bot")
	case n < 68:
		h.botAddAnnotationLocked(inst)
	case n < 78:
		h.botMoveAnnotationLocked(inst)
	case n < 86:
		h.botDeleteAnnotationLocked(inst)
	case n < 98:
		h.botPlaceOrderLocked(inst)
	default:
		h.botCancelOrderLocked(inst)
	}
}

func (h *Hub) botAddAnnotationLocked(inst *Instance) {
	if len(inst.Annotations) >= botMaxAnnotations {
		return
	}
	if inst.Cursor < 1 {
		return
	}
	a := h.randomRevealedIndexLocked(inst)
	b := h.randomRevealedIndexLocked(inst)
	if a > b {
		a, b = b, a
	}
	sa := inst.Bars[a]
	sb := inst.Bars[b]
	start := Point{Time: sa.Time, Price: roundPrice(randomPriceIn(h.rnd, sa))}
	end := Point{Time: sb.Time, Price: roundPrice(randomPriceIn(h.rnd, sb))}

	kinds := []string{AnnTrend, AnnRay, AnnRect, AnnArrow, AnnFib}
	kind := kinds[h.rnd.Intn(len(kinds))]
	snap := h.rnd.Intn(2) == 0
	if _, err := h.doAddAnnotationLocked(inst, "bot", kind, start, end, botStyle(h.rnd, kind), snap); err != nil {
		h.log("[replay] 机器人加标注失败：%v", err)
	}
}

func (h *Hub) botMoveAnnotationLocked(inst *Instance) {
	if len(inst.Annotations) == 0 {
		return
	}
	a := inst.Annotations[h.rnd.Intn(len(inst.Annotations))]
	bar := inst.Bars[h.randomRevealedIndexLocked(inst)]
	start := Point{Time: bar.Time, Price: roundPrice(randomPriceIn(h.rnd, bar))}
	bar2 := inst.Bars[h.randomRevealedIndexLocked(inst)]
	end := Point{Time: bar2.Time, Price: roundPrice(randomPriceIn(h.rnd, bar2))}
	if _, err := h.doMoveAnnotationLocked(inst, "bot", a.ID, start, end, false); err != nil {
		h.log("[replay] 机器人移动标注失败：%v", err)
	}
}

func (h *Hub) botDeleteAnnotationLocked(inst *Instance) {
	if len(inst.Annotations) == 0 {
		return
	}
	a := inst.Annotations[h.rnd.Intn(len(inst.Annotations))]
	if err := h.doDeleteAnnotationLocked(inst, "bot", a.ID); err != nil {
		h.log("[replay] 机器人删标注失败：%v", err)
	}
}

func (h *Hub) botPlaceOrderLocked(inst *Instance) {
	bar := inst.currentBar()
	span := math.Max(bar.High-bar.Low, bar.Close*0.0005)
	offset := span * (1 + h.rnd.Float64())

	side := "buy"
	if h.rnd.Intn(2) == 0 {
		side = "sell"
	}
	kind := KindLimit
	if h.rnd.Intn(2) == 0 {
		kind = KindStop
	}
	price := bar.Close
	switch {
	case kind == KindLimit && side == "buy":
		price = bar.Close - offset
	case kind == KindLimit && side == "sell":
		price = bar.Close + offset
	case kind == KindStop && side == "buy":
		price = bar.Close + offset
	default:
		price = bar.Close - offset
	}
	if price <= 0 {
		price = bar.Close
	}

	// 挂单太多就先撤一张最老的：机器人跑久了不该把订单簿撑爆。
	if openOrderCount(inst) >= botMaxOpenOrders {
		for _, o := range inst.Orders {
			if o.Status == OrderOpen {
				if err := h.doCancelOrderLocked(inst, "bot", o.ID); err != nil {
					h.log("[replay] 机器人撤单失败：%v", err)
				}
				break
			}
		}
	}
	if _, err := h.doPlaceOrderLocked(inst, "bot", side, kind, roundPrice(price), 1); err != nil {
		h.log("[replay] 机器人下单失败：%v", err)
	}
}

func (h *Hub) botCancelOrderLocked(inst *Instance) {
	var open []Order
	for _, o := range inst.Orders {
		if o.Status == OrderOpen {
			open = append(open, o)
		}
	}
	if len(open) == 0 {
		return
	}
	o := open[h.rnd.Intn(len(open))]
	if err := h.doCancelOrderLocked(inst, "bot", o.ID); err != nil {
		h.log("[replay] 机器人撤单失败：%v", err)
	}
}

// randomRevealedIndexLocked 随机挑一根**已经揭示**的 K 线。
// 只在前 70% 里挑：靠近游标的那几根留给步进，否则标注全堆在最新一根上，演示看不出层次。
func (h *Hub) randomRevealedIndexLocked(inst *Instance) int {
	if inst.Cursor <= 0 {
		return 0
	}
	upper := inst.Cursor
	if upper > 3 {
		upper = int(float64(upper) * 0.7)
	}
	if upper < 1 {
		upper = 1
	}
	return h.rnd.Intn(upper + 1)
}

func openOrderCount(inst *Instance) int {
	n := 0
	for _, o := range inst.Orders {
		if o.Status == OrderOpen {
			n++
		}
	}
	return n
}

func randomPriceIn(rnd interface{ Float64() float64 }, b Bar) float64 {
	lo, hi := math.Min(b.Low, b.High), math.Max(b.Low, b.High)
	if hi-lo < 1e-9 {
		return b.Close
	}
	return lo + rnd.Float64()*(hi-lo)
}

// botStyle 给机器人随机挑一个样式：颜色随机、偶尔虚线、偶尔细一点。
// 目的是让演示里的图形看起来不是同一个模板画的 —— 顺便把「每条图形各带一份样式」
// 这条路走一遍（真人画图时样式来自模板，机器人不需要模板）。
func botStyle(rnd interface {
	Intn(int) int
	Float64() float64
}, kind string) AnnotationStyle {
	style := defaultStyle(kind)
	style.Color = botColors[rnd.Intn(len(botColors))]
	style.Width = 1 + math.Round(rnd.Float64()*2*10)/10 // 1.0 ~ 3.0
	if rnd.Intn(3) == 0 {
		style.Dash = DashDashed
	}
	if kind == AnnRect {
		style.Fill = style.Color
	}
	if kind == AnnFib {
		// 四条水平各自换色，但没有一条是重复的
		levels := defaultFibLevels()
		for i := range levels {
			levels[i].Color = botColors[(i+rnd.Intn(len(botColors)))%len(botColors)]
		}
		style.Levels = levels
	}
	return style
}
