package replay

// 一个「复盘实例」= 一个标的 + 一个时间锚点 + 一条游标，以及挂在它上面的标注、
// 挂单、成交、持仓。实例本身不认识网络：所有操作都是纯方法，由 hub 负责广播。
//
// 这条分界很重要：机器人测试和单元测试直接调这些方法，走的和真人点按钮**同一条代码路径**。
// 如果操作逻辑写在消息处理里，测试就只能靠连 WS，出错时还分不清是逻辑错还是协议错。

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"time"
)

// ErrNoMoreBars 表示数据目录里已经没有更晚的 K 线了。
var ErrNoMoreBars = errors.New("数据已经到最后一根 K 线了")

const (
	maxEventsKept    = 500 // 每个实例保留多少条操作记录
	eventsInSnapshot = 100 // 快照里带多少条
	maxOrderQuantity = 10000
)

// Instance 是一个复盘实例的全部状态。
//
// Bars 同时包含「已经揭示的」和「已加载但还没揭示的」：游标 Cursor 之后的部分
// 只留在后端，永远不会出现在快照里 —— 前端拿不到，就不可能提前看到未来。
type Instance struct {
	ID     string
	Symbol string
	Folder string
	Tick   float64 // 最小跳动（来自 _symbols.json），价格轴按它定小数位
	// TickValue 是一个 tick 值多少美元（MNQ 0.5 / MES 1.25 / MGC 1 / CL 10）。
	// 0 = _symbols.json 没登记、按标的名也认不出来：盈亏退化成「每点 1 美元」。
	TickValue float64
	Anchor    string // 锚点 K 线的时间字符串

	Bars   []Bar
	Cursor int

	Annotations []Annotation
	Orders      []Order
	Trades      []Trade
	Position    Position
	RealizedPnl float64

	Operator  string // 当前操作者的名字；空 = 没人接管
	CreatedBy string
	CreatedAt int64

	Seq    int
	Events []Event

	dayFiles []string // 该标的的全部日文件名（升序）
	nextDay  int      // 下一个还没加载的日文件下标
	notes    []string // 加载过程中的实情（坏行、空文件），原样交给界面

	seqAnn   int
	seqOrder int
	seqTrade int

	botStop     chan struct{}
	botInterval time.Duration
	deleted     bool
}

// currentBar 是游标所在的那根。
func (inst *Instance) currentBar() Bar { return inst.Bars[inst.Cursor] }

// hiddenBars 是「已加载但没揭示」的根数。
func (inst *Instance) hiddenBars() int {
	if inst.Cursor >= len(inst.Bars) {
		return 0
	}
	return len(inst.Bars) - 1 - inst.Cursor
}

// hasMore 表示数据目录里还有没加载的日文件。
func (inst *Instance) hasMore() bool { return inst.nextDay < len(inst.dayFiles) }

// nextAnnotationID / nextOrderID / nextTradeID 都是「实例内自增」，
// 让日志里的 id 短且稳定（a1、o2、t3），排查时一眼能对上。
func (inst *Instance) nextAnnotationID() string {
	inst.seqAnn++
	return fmt.Sprintf("a%d", inst.seqAnn)
}

func (inst *Instance) nextOrderID() string {
	inst.seqOrder++
	return fmt.Sprintf("o%d", inst.seqOrder)
}

func (inst *Instance) nextTradeID() string {
	inst.seqTrade++
	return fmt.Sprintf("t%d", inst.seqTrade)
}

// ---------------------------------------------------------------------------
// 时间 -> 下标
// ---------------------------------------------------------------------------

// indexOfTime 在 Bars 里二分找**精确**匹配的时间。找不到返回 false。
// 时间字符串是 "yyyy-MM-dd HH:MM"，字典序与时间序一致，所以可以直接比。
func (inst *Instance) indexOfTime(t string) (int, bool) {
	i := sort.Search(len(inst.Bars), func(i int) bool { return inst.Bars[i].Time >= t })
	if i < len(inst.Bars) && inst.Bars[i].Time == t {
		return i, true
	}
	return 0, false
}

// resolvePoint 把一个标注端点落到「某一根已经揭示的 K 线」上。
//
// 这是需求里「标注严格基于时间价格坐标」的执行点：横坐标不存像素，只存 K 线时间；
// 后端不信任前端传来的价格 —— snap=true 时（用户按住 Ctrl）后端自己在这根 K 线的
// OHLC 里挑最近的那个，前端画出来的预览和后端存下来的结果因此必然一致。
func (inst *Instance) resolvePoint(p Point, snap bool) (Point, int, error) {
	if p.Time == "" {
		return Point{}, 0, errors.New("标注端点缺少时间（必须指向一根 K 线）")
	}
	if math.IsNaN(p.Price) || math.IsInf(p.Price, 0) || math.Abs(p.Price) > 1e12 {
		return Point{}, 0, errors.New("标注端点的价格不合法")
	}

	idx, exact := inst.indexOfTime(p.Time)
	if !exact {
		// 落在两根之间（前端理论上不会这样，但机器人/手工构造的消息可能）：
		// 取时间上最近的那一根，而不是报错 —— 用户看到的仍然是「吸到最近的 K 线」。
		j := sort.Search(len(inst.Bars), func(i int) bool { return inst.Bars[i].Time >= p.Time })
		switch {
		case j == 0:
			idx = 0
		case j == len(inst.Bars):
			idx = len(inst.Bars) - 1
		default:
			before, after := j-1, j
			if p.Time < inst.Bars[before].Time {
				idx = before
			} else if p.Time >= inst.Bars[after].Time {
				idx = after
			} else if diffsCloser(inst.Bars[before].Time, inst.Bars[after].Time, p.Time) {
				idx = before
			} else {
				idx = after
			}
		}
	}
	if idx > inst.Cursor {
		return Point{}, 0, errors.New("标注只能画在已经出现的 K 线上")
	}

	bar := inst.Bars[idx]
	price := p.Price
	if snap {
		price = nearestOHLC(bar, price)
	}
	// 价格统一修到两位小数：和 K 线、价格轴、下单框显示的值一致，
	// 否则一条标注可能是 30061.499999999996，图上和面板里对不上。
	return Point{Time: bar.Time, Price: roundPrice(price)}, idx, nil
}

// diffsCloser 判断 target 更靠近 a 还是 b（都是 "yyyy-MM-dd HH:MM"）。
// 时间字符串等长同格式，按字符串比较即可判断前后，不需要再解析成时间。
func diffsCloser(a, b, target string) bool {
	// 只有 target 落在 (a, b) 之间才会走到这里：谁离得近看两边字符串差。
	// 字符串没法直接减，这里转成"第几分钟"来比。数据量小，且只在罕见情况下调用。
	ta, ea := parseMinute(a)
	tb, eb := parseMinute(b)
	tt, et := parseMinute(target)
	if ea != nil || eb != nil || et != nil {
		return true
	}
	return tt-ta <= tb-tt
}

func parseMinute(s string) (int, error) {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		return 0, err
	}
	return int(t.Unix() / 60), nil
}

// nearestOHLC 在 O/H/L/C 里挑离 price 最近的那个。相等时按 O、H、L、C 顺序取。
func nearestOHLC(b Bar, price float64) float64 {
	best, bestDiff := b.Open, math.Abs(b.Open-price)
	for _, v := range []float64{b.High, b.Low, b.Close} {
		if d := math.Abs(v - price); d < bestDiff {
			best, bestDiff = v, d
		}
	}
	return best
}

// ---------------------------------------------------------------------------
// 标注
// ---------------------------------------------------------------------------

// addAnnotation 校验并加入一个标注。
//
// 五种图形都要求两个端点（趋势线/射线/箭头线是线的两端，矩形是对角，斐波那契是价格区间），
// 两个端点都必须落在已揭示的 K 线上 —— 标注是画在历史上的，指向一根还不存在的 K 线
// 既没意义，也会提前泄露后面的行情。
func (inst *Instance) addAnnotation(by, kind string, start, end Point, style AnnotationStyle, snap bool) (Annotation, error) {
	if !isDrawKind(kind) {
		return Annotation{}, fmt.Errorf("不认识的标注类型 %q（只支持 trend / ray / rect / arrow / fib）", kind)
	}
	s, _, err := inst.resolvePoint(start, snap)
	if err != nil {
		return Annotation{}, err
	}
	e, _, err := inst.resolvePoint(end, snap)
	if err != nil {
		return Annotation{}, err
	}

	a := Annotation{
		ID:        inst.nextAnnotationID(),
		Kind:      kind,
		Start:     s,
		End:       e,
		Style:     sanitizeStyle(kind, style),
		Snapped:   snap,
		CreatedBy: by,
		CreatedAt: time.Now().Unix(),
	}
	inst.Annotations = append(inst.Annotations, a)
	return a, nil
}

// moveAnnotation 把已有标注挪到新的端点。
//
// 「整体移动」和「只动起点/终点」是同一条路：前端算好两个端点各自的新位置发过来，
// 后端只管校验 —— 只动一个端点时，另一个原样发回。
// **样式不跟着移动变**：样式在画下去那一刻就定了。
func (inst *Instance) moveAnnotation(by, id string, start, end Point, snap bool) (Annotation, error) {
	for i := range inst.Annotations {
		if inst.Annotations[i].ID != id {
			continue
		}
		a := &inst.Annotations[i]
		s, _, err := inst.resolvePoint(start, snap)
		if err != nil {
			return Annotation{}, err
		}
		e, _, err := inst.resolvePoint(end, snap)
		if err != nil {
			return Annotation{}, err
		}
		a.Start = s
		a.End = e
		a.Snapped = snap
		return *a, nil
	}
	return Annotation{}, fmt.Errorf("没有这个标注：%s（可能已被别人删掉）", id)
}

// deleteAnnotation 删掉一个标注。
func (inst *Instance) deleteAnnotation(id string) error {
	for i, a := range inst.Annotations {
		if a.ID != id {
			continue
		}
		inst.Annotations = append(inst.Annotations[:i], inst.Annotations[i+1:]...)
		return nil
	}
	return fmt.Errorf("没有这个标注：%s（可能已被别人删掉）", id)
}

// ---------------------------------------------------------------------------
// 模拟交易
// ---------------------------------------------------------------------------

// placeOrder 校验并挂一张单。
//
// 没有「初始资金」这个概念：这里只记数量、价格和盈亏，能不能买得起不判。
// 也不支持止损/止盈类型 —— 用户想止损就自己挂一张 stop 单，规则和手动挂单完全一样。
// 市价单走另一条路径（marketOrder）：它下单即成交，不需要挂单价格。
//
// 这里**不校验「这个方向该用哪种单」**：挂反了（比如把止损买挂在现价下方）在真实市场里
// 等于一张已经触发的市价单，撮合时按开盘价成交（见 fillPriceFor），不会凭空拿到更好的价格。
// 界面上的右键菜单会按方向自动选单型，手填价格的下单框允许用户自己决定。
func (inst *Instance) placeOrder(by, side, kind string, price, qty float64) (Order, error) {
	if err := validateSide(side); err != nil {
		return Order{}, err
	}
	if kind != KindLimit && kind != KindStop {
		return Order{}, fmt.Errorf("订单类型只能是 limit / stop，收到 %q", kind)
	}
	if !finitePositive(price) {
		return Order{}, errors.New("挂单价格必须是大于 0 的有限数字")
	}
	qty, err := normalizeQuantity(qty)
	if err != nil {
		return Order{}, err
	}
	bar := inst.currentBar()
	o := Order{
		ID:          inst.nextOrderID(),
		Side:        side,
		Kind:        kind,
		Price:       roundPrice(price),
		Quantity:    qty,
		Status:      OrderOpen,
		PlacedAt:    bar.Time,
		PlacedIndex: inst.Cursor,
		FilledIndex: -1,
	}
	inst.Orders = append(inst.Orders, o)
	return o, nil
}

// marketOrder 按当前（游标那根）K 线的收盘价**立即成交**。
//
// 复盘里「现在」就是最后一根已揭示 K 线的收盘：没有盘口、没有滑点，也不等下一根 ——
// 成交价和下单框上显示的「现价」是同一个数。成交以后订单直接是 filled，
// 不会被 advance 再撮合一次（PlacedIndex 也等于 Cursor）。
//
// 盈亏、持仓、成交流水都走和挂单成交完全相同的 applyFill，
// 所以「市价买入再市价平掉」和「两张限价单被触发」算出来的数字口径一致。
func (inst *Instance) marketOrder(by, side string, qty float64) (Order, Trade, error) {
	if err := validateSide(side); err != nil {
		return Order{}, Trade{}, err
	}
	qty, err := normalizeQuantity(qty)
	if err != nil {
		return Order{}, Trade{}, err
	}
	bar := inst.currentBar()
	price := roundPrice(bar.Close)
	if !finitePositive(price) {
		return Order{}, Trade{}, errors.New("当前 K 线没有可成交的收盘价")
	}
	o := Order{
		ID:          inst.nextOrderID(),
		Side:        side,
		Kind:        KindMarket,
		Price:       price,
		Quantity:    qty,
		Status:      OrderFilled,
		PlacedAt:    bar.Time,
		PlacedIndex: inst.Cursor,
		FilledAt:    bar.Time,
		FilledIndex: inst.Cursor,
		FillPrice:   price,
	}
	inst.Orders = append(inst.Orders, o)
	tr := inst.applyFill(o, price, bar.Time)
	inst.Trades = append(inst.Trades, tr)
	inst.RealizedPnl += tr.RealizedPnl
	inst.Position = inst.position()
	return o, tr, nil
}

// validateSide 是 buy / sell 之外的任何值都不收。
func validateSide(side string) error {
	if side != "buy" && side != "sell" {
		return fmt.Errorf("买卖方向只能是 buy / sell，收到 %q", side)
	}
	return nil
}

// normalizeQuantity 校验手数并夹成整数。
//
// 手数只能是整数：期货没有「0.4 手」这种报单，允许小数会让持仓、成交、
// 平仓配对全都跟着出现半手，「2 手分两次开仓」也会退化成一条平均成本的线。
func normalizeQuantity(qty float64) (float64, error) {
	if !finitePositive(qty) {
		return 0, errors.New("数量必须是大于 0 的有限数字")
	}
	if math.Abs(qty-math.Round(qty)) > 1e-9 {
		return 0, fmt.Errorf("手数必须是整数（1、2、3…），收到 %v", qty)
	}
	qty = math.Round(qty)
	if qty > maxOrderQuantity {
		return 0, fmt.Errorf("单笔手数最多 %d 手", maxOrderQuantity)
	}
	return qty, nil
}

// moveOrder 改一张挂单的价格 —— 画布上拖动挂单线就是它。
//
// 只改价格，**不改单型、不改数量、不改编号**：
//   - 单型是下单那一刻按方向定的（界面上是 orderKindFor）。拖过现价就自动换个单型，
//     会让订单在用户手里悄悄变性质 —— 一张「跌破 95 才卖」的止损单往上拖过现价，
//     会变成「涨到 105 才卖」的限价单，用户以为自己只是挪了条线。想换单型就撤了重挂。
//   - 编号不变，成交三角和操作记录里那条线就还是同一个订单。
//
// 只有还挂着的单能改：已成交/已撤销的单改了价格不会再参与撮合，改它只会让
// 历史记录和实际成交价对不上。
func (inst *Instance) moveOrder(id string, price float64) (Order, error) {
	if !finitePositive(price) {
		return Order{}, errors.New("挂单价格必须是大于 0 的有限数字")
	}
	for i := range inst.Orders {
		o := &inst.Orders[i]
		if o.ID != id {
			continue
		}
		if o.Status != OrderOpen {
			return Order{}, fmt.Errorf("这张单已经%s，不能改价", statusText(o.Status))
		}
		o.Price = roundPrice(price)
		return *o, nil
	}
	return Order{}, fmt.Errorf("没有这张单：%s", id)
}

// cancelOrder 撤单。已成交/已撤销的单不能撤。
func (inst *Instance) cancelOrder(id string) (Order, error) {
	for i := range inst.Orders {
		o := &inst.Orders[i]
		if o.ID != id {
			continue
		}
		if o.Status != OrderOpen {
			return Order{}, fmt.Errorf("这张单已经%s，不能撤", statusText(o.Status))
		}
		o.Status = OrderCancelled
		return *o, nil
	}
	return Order{}, fmt.Errorf("没有这张单：%s", id)
}

func statusText(s string) string {
	switch s {
	case OrderFilled:
		return "成交"
	case OrderCancelled:
		return "撤销"
	default:
		return "挂着"
	}
}

func finitePositive(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0) && v > 0 && v < 1e12
}

// advance 揭示下一根 K 线，并按这根 K 线的最高/最低价撮合挂单。
//
// 撮合方式（刻意做得很朴素，都写在文档里）：
//   - 限价买：本根最低价 <= 挂单价 -> 成交；限价卖：最高价 >= 挂单价 -> 成交
//   - 止损买：本根最高价 >= 挂单价 -> 成交；止损卖：最低价 <= 挂单价 -> 成交
//   - **成交价由 fillPriceFor 定**：正常情况下就是挂单价，开盘就跳过挂单价时取开盘价
//     （跳空白捡便宜是这类回放工具最容易出错的地方）
//   - 数量按挂单数量一次成交，不做部分成交
//   - 只撮合「上一根或更早」挂的单：本根挂的单从下一根开始参与，避免用已经收完的
//     K 线的最高最低价倒推成交
func (inst *Instance) advance(ds *DataSource) (Bar, []Trade, error) {
	if inst.Cursor+1 >= len(inst.Bars) {
		if err := inst.ensureFuture(ds); err != nil {
			return Bar{}, nil, err
		}
	}
	inst.Cursor++
	bar := inst.Bars[inst.Cursor]

	var fills []Trade
	for i := range inst.Orders {
		o := &inst.Orders[i]
		if o.Status != OrderOpen || o.PlacedIndex >= inst.Cursor {
			continue
		}
		if !orderTriggered(*o, bar) {
			continue
		}
		o.Status = OrderFilled
		o.FilledAt = bar.Time
		o.FilledIndex = inst.Cursor
		o.FillPrice = fillPriceFor(*o, bar)
		tr := inst.applyFill(*o, o.FillPrice, bar.Time)
		inst.Trades = append(inst.Trades, tr)
		inst.RealizedPnl += tr.RealizedPnl
		fills = append(fills, tr)
	}
	inst.Position = inst.position()
	return bar, fills, nil
}

// orderTriggered 判断一张挂单是否被这根 K 线触发。
//
// 四种组合正好是两对镜像：触发看的是**价格往哪个方向穿过挂单价**，
// 限价单和止损单对方向的要求完全相反 —— 这也是右键菜单要按方向选单型的根源。
func orderTriggered(o Order, b Bar) bool {
	switch o.Kind {
	case KindLimit:
		if o.Side == "buy" {
			return b.Low <= o.Price
		}
		return b.High >= o.Price
	case KindStop:
		if o.Side == "buy" {
			return b.High >= o.Price
		}
		return b.Low <= o.Price
	}
	return false
}

// fillPriceFor 决定成交价：挂单价，或者**开盘价**（开盘就已经越过挂单价时）。
//
// 触发判定用的是整根 K 线的最高/最低价，但成交价不能一律取挂单价：开盘就跳过挂单价时，
// 那一笔真实成交发生在**开盘那一刻**，不是挂单价。按挂单价成交等于凭空占便宜，
// 止损买单最明显（跳空高开 106，还用 102.5 买到）。
//
// 取 min 还是 max 由「价格从哪边穿过来」决定：
//
//	限价买 / 止损卖 -> min(挂单价, 开盘价)   价格向下穿过，开盘更低就先成交在开盘
//	限价卖 / 止损买 -> max(挂单价, 开盘价)   价格向上穿过，开盘更高就先成交在开盘
//
// 这两行同时把两类单的区别说清楚了：限价单拿到的是**不比挂单价差**的价（等价格过来），
// 止损单拿到的是**不比挂单价好**的价（价格跑了才追）。把方向写反，就成了「无风险套利」。
func fillPriceFor(o Order, b Bar) float64 {
	if b.Open <= 0 { // 理论上不会发生（CSV 每根都有开盘价），兜底成挂单价
		return o.Price
	}
	if o.Side == "buy" {
		if o.Kind == KindLimit {
			return math.Min(o.Price, b.Open)
		}
		return math.Max(o.Price, b.Open)
	}
	if o.Kind == KindLimit {
		return math.Max(o.Price, b.Open)
	}
	return math.Min(o.Price, b.Open)
}

// applyFill 把一次成交记进持仓，返回成交流水。
//
// price 是**实际成交价**（通常等于挂单价，跳空时是开盘价，见 fillPriceFor）——
// 持仓成本和盈亏都按它算，不能拿挂单价顶替。
//
// 平均成本法：同向加仓摊平成本；反向先平掉能平的部分（实现盈亏 = (成交价-均价)*平掉的数量*方向），
// 平完还有剩余就是反手开仓，新仓均价 = 本次成交价。
func (inst *Instance) applyFill(o Order, price float64, t string) Trade {
	signed := o.Quantity
	if o.Side == "sell" {
		signed = -o.Quantity
	}
	pos := inst.Position.Quantity
	realized := 0.0

	switch {
	case pos == 0 || (pos > 0) == (signed > 0):
		total := math.Abs(pos) + math.Abs(signed)
		if total > 0 {
			inst.Position.AveragePrice = (math.Abs(pos)*inst.Position.AveragePrice + math.Abs(signed)*price) / total
		}
		inst.Position.Quantity = pos + signed
	default:
		closeQty := math.Min(math.Abs(pos), math.Abs(signed))
		dir := 1.0
		if pos < 0 {
			dir = -1
		}
		realized = (price - inst.Position.AveragePrice) * closeQty * dir * inst.pointValue()
		next := pos + signed
		if math.Abs(next) < 1e-9 {
			next = 0
			inst.Position.AveragePrice = 0
		} else if (next > 0) != (pos > 0) {
			// 反手：剩余仓位是本次新开的，成本就是本次成交价
			inst.Position.AveragePrice = price
		}
		inst.Position.Quantity = next
	}
	inst.Position = inst.position()

	return Trade{
		ID:          inst.nextTradeID(),
		OrderID:     o.ID,
		Time:        t,
		Side:        o.Side,
		Kind:        o.Kind,
		Price:       price,
		Quantity:    o.Quantity,
		RealizedPnl: realized,
	}
}

// position 把带符号的持仓整理成「数量 / 方向 / 均价」。
func (inst *Instance) position() Position {
	q := inst.Position.Quantity
	side := "flat"
	switch {
	case q > 1e-9:
		side = "long"
	case q < -1e-9:
		side = "short"
	default:
		q = 0
	}
	avg := inst.Position.AveragePrice
	if q == 0 {
		avg = 0
	}
	return Position{Quantity: q, Side: side, AveragePrice: avg}
}

// pointValue 是「价格每走 1.0，一手值多少美元」。
//
// 它由 _symbols.json 里的 tickValue / tick 算出来，而不是另存一个乘数：
// MNQ 0.50/0.25 = 2 美元/点，MES 1.25/0.25 = 5，MGC 1.00/0.1 = 10，CL 10.00/0.01 = 1000。
// 用户只要填「一跳值多少」，乘数自然就对了。没配置（或配置不成对）时退化成 1 美元/点 ——
// 至少数字还能互相比较，而不是算出一个 0。
func (inst *Instance) pointValue() float64 {
	if inst.Tick > 0 && inst.TickValue > 0 {
		return inst.TickValue / inst.Tick
	}
	return 1
}

// unrealizedPnl 按当前这根 K 线的收盘价算浮动盈亏（美元）。
// 空头持仓 Quantity 为负，(close-avg)*qty 自然给出正确符号；再乘每点价值换成美元。
func (inst *Instance) unrealizedPnl() float64 {
	q := inst.Position.Quantity
	if q == 0 {
		return 0
	}
	return (inst.currentBar().Close - inst.Position.AveragePrice) * q * inst.pointValue()
}

// ---------------------------------------------------------------------------
// 快照
// ---------------------------------------------------------------------------

// snapshot 组装给前端的全量快照（只含已揭示的 K 线）。
func (inst *Instance) snapshot(members []Member) InstanceDTO {
	cursor := inst.Cursor
	if cursor < 0 {
		cursor = 0
	}
	if cursor >= len(inst.Bars) {
		cursor = len(inst.Bars) - 1
	}
	bars := make([]Bar, cursor+1)
	copy(bars, inst.Bars[:cursor+1])

	anns := make([]Annotation, len(inst.Annotations))
	copy(anns, inst.Annotations)
	orders := make([]Order, len(inst.Orders))
	copy(orders, inst.Orders)
	trades := make([]Trade, len(inst.Trades))
	copy(trades, inst.Trades)

	events := inst.Events
	if len(events) > eventsInSnapshot {
		events = events[len(events)-eventsInSnapshot:]
	}
	evs := make([]Event, len(events))
	copy(evs, events)

	notes := make([]string, len(inst.notes))
	copy(notes, inst.notes)

	return InstanceDTO{
		ID:            inst.ID,
		Symbol:        inst.Symbol,
		Folder:        inst.Folder,
		Anchor:        inst.Anchor,
		Cursor:        cursor,
		Tick:          inst.Tick,
		TickValue:     inst.TickValue,
		Bars:          bars,
		HiddenBars:    inst.hiddenBars(),
		HasMore:       inst.hasMore(),
		Annotations:   anns,
		Orders:        orders,
		Trades:        trades,
		Position:      inst.position(),
		RealizedPnl:   inst.RealizedPnl,
		UnrealizedPnl: inst.unrealizedPnl(),
		Operator:      inst.Operator,
		Members:       members,
		Bot:           inst.botStop != nil,
		Seq:           inst.Seq,
		Events:        evs,
		Notes:         notes,
		CreatedBy:     inst.CreatedBy,
		CreatedAt:     inst.CreatedAt,
	}
}

func (inst *Instance) summary(members int) InstanceSummary {
	return InstanceSummary{
		ID:          inst.ID,
		Symbol:      inst.Symbol,
		Anchor:      inst.Anchor,
		Cursor:      inst.Cursor,
		LoadedBars:  len(inst.Bars),
		Members:     members,
		Operator:    inst.Operator,
		Bot:         inst.botStop != nil,
		CreatedBy:   inst.CreatedBy,
		CreatedAt:   inst.CreatedAt,
		RealizedPnl: inst.RealizedPnl,
		Trades:      len(inst.Trades),
	}
}

// appendEvent 记录一条操作。只保留最近 maxEventsKept 条：复盘日志是给人看的，
// 不是审计系统，无限增长只会在长时间跑演示时把内存吃掉。
func (inst *Instance) appendEvent(by, action string, payload any) Event {
	inst.Seq++
	ev := Event{Seq: inst.Seq, At: time.Now().Unix(), By: by, Action: action, Payload: payload}
	inst.Events = append(inst.Events, ev)
	if len(inst.Events) > maxEventsKept {
		inst.Events = append([]Event(nil), inst.Events[len(inst.Events)-maxEventsKept:]...)
	}
	return ev
}
