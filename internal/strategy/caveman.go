package strategy

// Caveman（野人）：两根 K 线的反转形态，出自 Al Brooks 价格行为学派。
//
// 形态本身两句话：
//
//	信号棒  一根强趋势棒 —— 实体够大、收盘贴在极值附近（一方全力推出去）
//	触发棒  紧接的一根反向棒，收盘**吞回信号棒的起点**（另一方一口气打回来）
//
// 含义是「失败的突破」：推出去的那一方被打回去了，顺着打回来的方向做。
// 做空 = 强阳棒之后一根阴棒收在阳棒开盘价之下；做多反之。
//
// 止损放在**信号棒极值外**一个 tick（空单在信号棒最高价上方），目标 = 入场价
// 加减 R 的若干倍。这两条决定了这个策略的盈亏结构，不是可调的风格选项。
//
// 原文的止盈写的是「0.5R 和 1R 剥头皮」，但同一份材料里案例目标从 1.5R 一路
// 写到 4R、7R —— 说明真实交易里目标是看着盘面临时定的。可回测的版本必须固定
// 一个，所以这里默认 1R：在 2024-03~2026-10 的 820 天数据上，1R 的零成本毛期望
// 是三个候选里最高的（+0.75 美元/笔，MNQ），0.5R 反而被成本吃掉。
//
// ⚠️ 这个策略在本项目的撮合里跑的是**零滑点、零手续费**。真实的市价单要跨过
// 一个 tick 的买卖价差，那一个 tick 就吃掉大部分毛利。界面上看到的是「最有利
// 假设下」的结果，不是实盘能拿到的结果。

import (
	"math"
	"strconv"
)

func init() { Register(func() Strategy { return &caveman{} }) }

// caveman 是策略实例。参数在 v 里，状态只有「处理到哪根」和「今天开了几单」。
type caveman struct {
	v Values

	lastIdx int    // 上一次处理过的 K 线下标（-1 = 还没开始）
	day     string // 当前交易日（06:00 口径），换日时清零计数
	trades  int    // 这个交易日已经开过几单
}

func (s *caveman) ID() string { return "caveman" }

func (s *caveman) Name() string { return "Caveman（野人）" }

func (s *caveman) Summary() string {
	return "强趋势棒被紧接的反向棒吞回起点 → 顺着吞没方向做反转；止损在信号棒极值外，目标 R 倍。零成本假设。"
}

// cavemanParams 是出厂参数。**改默认值就改这里**，前端启动面板读的也是这份。
var cavemanParams = []Param{
	{
		Key: "bodyMin", Label: "信号棒实体占比", Default: 0.5, Min: 0.1, Max: 0.95, Step: 0.05,
		Help: "实体长度 ÷ 整根振幅。0.5 = 实体至少占一半，才算「强趋势棒」。",
	},
	{
		Key: "closePosMin", Label: "信号棒收盘位置", Default: 0.75, Min: 0.5, Max: 1, Step: 0.05,
		Help: "阳线收盘要落在振幅的上 25%，阴线要落在下 25%。0.75 = 收盘至少到 75% 位置。",
	},
	{
		Key: "targetR", Label: "止盈（R 倍数）", Default: 1, Min: 0.2, Max: 5, Step: 0.1,
		Help: "目标价 = 入场价 ± R × 该倍数。R 是入场到止损的距离。原文用 0.5R/1R。",
	},
	{
		Key: "stopBufferTicks", Label: "止损外扩（tick）", Default: 1, Min: 0, Max: 20, Step: 1,
		Help: "止损放在信号棒极值之外多少个最小跳动。1 = 刚出极值一格。",
	},
	{
		Key: "minRTicks", Label: "最小风险（tick）", Default: 4, Min: 0, Max: 200, Step: 1,
		Help: "入场到止损不足这么多 tick 就不做：空间太小，形态再漂亮也不够付成本。",
	},
	{
		Key: "maxTradesPerDay", Label: "每日最多开仓", Default: 4, Min: 0, Max: 50, Step: 1,
		Help: "一个交易日最多开几单。0 = 不限制（不建议：连续假突破会把当天的钱赔光）。",
	},
	{
		Key: "allowLong", Label: "允许做多", Default: 1, Min: 0, Max: 1, Step: 1,
		Help: "0 = 只做空。用来验证「单边行情里某一侧是不是真的更差」。",
	},
	{
		Key: "allowShort", Label: "允许做空", Default: 1, Min: 0, Max: 1, Step: 1,
		Help: "0 = 只做多。",
	},
}

func (s *caveman) Params() []Param {
	out := make([]Param, len(cavemanParams))
	copy(out, cavemanParams)
	return out
}

func (s *caveman) Configure(values Values) error {
	s.v = Merge(s.Params(), values)
	return nil
}

func (s *caveman) Reset() {
	s.lastIdx = -1
	s.day = ""
	s.trades = 0
}

// OnBar 判断刚收盘的这一根是不是「触发棒」。
//
// 执行器每推进一根就会调一次，同一根可能因为重试被调多次 —— 用 lastIdx 去重，
// 否则一次信号会被算成好几单（每日计数和实际成交对不上）。
func (s *caveman) OnBar(ctx *Context) []Signal {
	cur := ctx.Cursor
	if cur < 1 || cur >= len(ctx.Bars) {
		return nil
	}
	if s.lastIdx == cur {
		return nil
	}
	s.lastIdx = cur

	trigger := ctx.Bar()
	signalBar, ok := ctx.Prev(1)
	if !ok {
		return nil
	}

	// 换交易日就清零计数。这里和「平仓」是两件事：平仓由执行器负责（它才看得见持仓），
	// 策略只负责「新的一天重新开始数单子」。
	if day := TradeDay(trigger.Time); day != s.day {
		s.day = day
		s.trades = 0
	}

	// 收盘收尾时段不再开新仓 —— 开了也没时间走，只会被日终平仓砍在半路。
	if SessionEnd(trigger.Time) {
		return nil
	}
	// 已经有仓位：保护单（止损/止盈）由执行器管，策略不叠仓、不反手。
	if !ctx.Flat() {
		return nil
	}
	if max := int(s.v.Float("maxTradesPerDay", 4)); max > 0 && s.trades >= max {
		return nil
	}

	bull0, bear0 := strongBar(signalBar, s.v.Float("bodyMin", 0.5), s.v.Float("closePosMin", 0.75))

	// 两种镜像形态：阳棒被阴棒吞回起点 → 做空；阴棒被阳棒吞回起点 → 做多。
	side := ""
	switch {
	case bull0 && trigger.Bear() && trigger.Close < signalBar.Open:
		side = "sell"
	case bear0 && trigger.Bull() && trigger.Close > signalBar.Open:
		side = "buy"
	}
	if side == "" {
		return nil
	}
	if side == "buy" && s.v.Float("allowLong", 1) < 0.5 {
		return nil
	}
	if side == "sell" && s.v.Float("allowShort", 1) < 0.5 {
		return nil
	}

	tick := ctx.Tick
	if tick <= 0 {
		tick = 0.01 // 标的名认不出来时的兜底，和 store.DefaultTick 一个思路
	}
	entry := trigger.Close
	buffer := s.v.Float("stopBufferTicks", 1) * tick

	var stop, target float64
	if side == "sell" {
		// 止损往上、目标往下：两边都往「更差」的方向对齐（见 AlignTickUp 的说明）
		stop = AlignTickUp(signalBar.High+buffer, tick)
	} else {
		stop = AlignTickDown(signalBar.Low-buffer, tick)
	}
	risk := math.Abs(entry - stop)
	if risk < s.v.Float("minRTicks", 4)*tick {
		return nil
	}
	if side == "sell" {
		target = AlignTickDown(entry-s.v.Float("targetR", 1)*risk, tick)
	} else {
		target = AlignTickUp(entry+s.v.Float("targetR", 1)*risk, tick)
	}
	if stop <= 0 || target <= 0 {
		return nil
	}

	s.trades++
	return []Signal{{
		Side:   side,
		Stop:   stop,
		Target: target,
		Reason: cavemanReason(side, risk, tick, s.v.Float("targetR", 1)),
	}}
}

// cavemanReason 拼一句能直接贴进日志的说明。数值都带上，事后看录像能对上。
//
// 走到这里的 side 已经决定了信号棒是哪种颜色：做空的信号棒一定是阳棒，
// 做多的一定是阴棒（见 OnBar 的 switch），所以这里不用再判断一遍。
func cavemanReason(side string, risk, tick, targetR float64) string {
	dir, name := "强阴棒被阳棒吞回起点", "多"
	if side == "sell" {
		dir, name = "强阳棒被阴棒吞回起点", "空"
	}
	return "Caveman " + name + "：" + dir +
		"，R=" + strconv.FormatFloat(math.Round(risk*100)/100, 'f', -1, 64) +
		"（" + strconv.Itoa(int(math.Round(risk/tick))) + " tick），目标 " +
		strconv.FormatFloat(targetR, 'f', -1, 64) + "R"
}

// strongBar 判断一根 K 线是不是「强趋势棒」：实体够大 + 收盘贴着自己的极值。
// 两个条件都要满足 —— 只看实体大，会把「上下影线都很长的大阳线」也算成强势，
// 那种棒说明的是双方在打架，不是一方推出去。
func strongBar(b Bar, bodyMin, closePosMin float64) (bull, bear bool) {
	r := b.Range()
	if r <= 0 {
		return false, false
	}
	if b.Body()/r < bodyMin {
		return false, false
	}
	pos := b.ClosePosition()
	if b.Bull() && pos >= closePosMin {
		return true, false
	}
	if b.Bear() && pos <= 1-closePosMin {
		return false, true
	}
	return false, false
}
