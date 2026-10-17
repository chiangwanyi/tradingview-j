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
	"time"
)

const (
	botDefaultIntervalMs = 1200
	botMinIntervalMs     = 150
	botMaxIntervalMs     = 10000
	botMaxAnnotations    = 40
	botMaxOpenOrders     = 12
)

var botTexts = []string{"支撑", "阻力", "假突破", "缩量", "放量", "这里很关键", "等待确认"}

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
	interval := req.IntervalMs
	if interval <= 0 {
		interval = botDefaultIntervalMs
	}
	if interval < botMinIntervalMs {
		interval = botMinIntervalMs
	}
	if interval > botMaxIntervalMs {
		interval = botMaxIntervalMs
	}

	stop := make(chan struct{})
	inst.botStop = stop
	inst.botInterval = time.Duration(interval) * time.Millisecond
	inst.Operator = "bot"

	h.broadcastInstanceLocked(inst, ActControlChanged, ControlChangedPayload{Operator: "bot"}, c.Name)
	h.broadcastInstanceLocked(inst, ActBotStarted, BotPayload{IntervalMs: interval}, c.Name)
	h.broadcastInstancesLocked()
	h.log("[replay] 机器人接管实例 %s，每 %dms 动作一次", inst.ID, interval)

	go h.runBot(inst, stop, inst.botInterval)
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
	close(inst.botStop)
	inst.botStop = nil
	inst.botInterval = 0
	inst.Operator = restore
	h.broadcastInstanceLocked(inst, ActBotStopped, nil, "bot")
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

// botTickLocked 随机挑一个动作执行。权重刻意偏向「步进」：
// 演示时最想看到的是行情一根根往前走，标注和下单是穿插着发生的。
func (h *Hub) botTickLocked(inst *Instance) {
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

	kinds := []string{AnnLine, AnnRect, AnnText}
	kind := kinds[h.rnd.Intn(len(kinds))]
	text := ""
	if kind == AnnText {
		text = botTexts[h.rnd.Intn(len(botTexts))]
		end = Point{}
	}
	snap := h.rnd.Intn(2) == 0
	if _, err := h.doAddAnnotationLocked(inst, "bot", kind, start, end, text, botColor(kind), snap); err != nil {
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
	end := Point{}
	if a.Kind != AnnText {
		bar2 := inst.Bars[h.randomRevealedIndexLocked(inst)]
		end = Point{Time: bar2.Time, Price: roundPrice(randomPriceIn(h.rnd, bar2))}
	}
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

func botColor(kind string) string {
	switch kind {
	case AnnRect:
		return "#f59e0b"
	case AnnText:
		return "#e5e7eb"
	default:
		return "#38bdf8"
	}
}
