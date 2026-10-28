package replay

// Prop Firm 交易考核账户（v0.31.0）。
//
// 一个「账户」不是独立实体，而是挂在实例上的一个可选字段（Instance.account）：
// 盘面、成交、持仓、交易记录全都还是实例那一套，账户只多管三件事 ——
// 余额、亏损线、阶段。nil = 自由模式（没有余额概念，和以前完全一样）。
//
// 为什么不做成独立实体：真正的考核账户本来也是「一张盘面 + 一套规则」，
// 而实例已经是这张盘面了。做成两个实体就要维护「账户 ↔ 实例」的对应关系、
// 各自一套落盘和广播，收益为零。
//
// 记账原则：**只存不可推导的事实**。余额是算出来的（Initial + 已实现净额 - 阶段基线），
// 所以不存在「余额字段和成交对不上」这种问题。已实现净额里已经扣了手续费
// （见 instance.go applyFill），账户这边不需要再扣一次。

import (
	"fmt"
	"math"
)

// accountTemplates 是首批内置的考核模板。
//
// 放在代码里而不是 presets/*.json：这是「出厂规则」，不是用户偏好
// （presets 那套是给样式模板用的，用户会改；考核规则改了就等于换了另一个产品）。
// 已经建出来的账户会把规则快照进 meta.json，所以改这里不影响老账户。
var accountTemplates = []AccountRules{
	{
		ID:   "lucid-flex-50k",
		Name: "LucidFlex 50K",
		// EVAL：初始 50000，盈利目标 3000（余额到 53000 通过）
		Initial:      50000,
		ProfitTarget: 3000,
		// 日终追踪回撤 2000：亏损线 = max(初始, 日终权益峰值) - 2000，
		// 日终权益到 52100 之后锁在 50100（= 52100 - 2000），不再上移。
		MaxDrawdown: 2000,
		LockBalance: 52100,
		FloorCap:    50100,
		// 当日净盈利到 1200 就禁止再开新仓（平仓、撤单照常）。
		DailyLimit: 1200,
		// MES / MNQ / MGC 每个品种最多 40 手。
		MaxContracts: 40,
		// FUNDED：5 个盈利日，每个至少净赚 150。
		ProfitDays:   5,
		ProfitDayMin: 150,
	},
}

// accountTemplateByID 按 id 找模板。找不到返回 false（调用方给用户一句中文）。
func accountTemplateByID(id string) (AccountRules, bool) {
	for _, r := range accountTemplates {
		if r.ID == id {
			return r, true
		}
	}
	return AccountRules{}, false
}

// accountState 是一个考核账户的全部状态。
//
// 字段全部是「不可推导的事实」：余额不在这里，它是 balance() 算的。
// 这样重启、回放、手工改存档都不会出现两本对不上的账。
type accountState struct {
	Rules AccountRules `json:"rules"`
	Phase string       `json:"phase"`

	// BaselinePnl 是当前阶段的已实现盈亏基线。EVAL→FUNDED 时重置成当时的累计值，
	// 于是余额回到 50000（EVAL 赚的钱不带过去）。这是「余额重置」的唯一实现方式。
	BaselinePnl float64 `json:"baselinePnl"`

	// 日终追踪回撤。PeakEOD 只在日终更新，Floor 由它算出来（见 applyFloor）。
	PeakEOD float64 `json:"peakEod"`
	Floor   float64 `json:"floor"`
	Locked  bool    `json:"locked"` // 亏损线已经贴在 FloorCap 上，不再上移

	// 当前交易日：DayDate 是「正在过的这一天」（开盘首根的日期），用来给日终结算去重；
	// DayStartPnl 是这一天开始时的累计已实现盈亏，当日净额 = 现在 - 它。
	DayDate     string  `json:"dayDate"`
	DayStartPnl float64 `json:"dayStartPnl"`
	DayLocked   bool    `json:"dayLocked"` // 当日净盈利已封顶

	// 阶段进度
	ProfitDays    int     `json:"profitDays"`
	PendingFunded bool    `json:"pendingFunded,omitempty"` // EVAL 已达标，等平仓才切 FUNDED
	Payout        float64 `json:"payout,omitempty"`
	ClosedAt      string  `json:"closedAt,omitempty"`
	FailReason    string  `json:"failReason,omitempty"`

	// note 是「待广播的一句中文」，不落盘：hub 在广播出口把它消费成一条
	// account_changed 事件（见 hub.go broadcastInstanceLocked）。
	note string `json:"-"`
}

// newAccountState 按模板建一个新账户。
//
// DayDate 故意留空：第一次日终结算要能把「建户这一天的收盘」算进去，
// 预先填上日期反而会让第一次结算被去重掉（亏损线和峰值就永远差一天）。
func newAccountState(r AccountRules) *accountState {
	a := &accountState{
		Rules:       r,
		Phase:       PhaseEval,
		PeakEOD:     r.Initial,
		DayStartPnl: 0, // 建户那一刻累计已实现盈亏是 0（新实例）
	}
	a.applyFloor()
	// 刻意不设 note：建户走的是 sendStateLocked（不是广播），
	// 在这里挂一句话会让它拖到用户第一次下单时才蹦出来，看起来像「下单引起的」。
	return a
}

// phaseText 是阶段的中文名。错误提示和界面文案共用这一份，
// 免得同一个阶段在两处叫两个名字。
func phaseText(phase string) string {
	switch phase {
	case PhaseEval:
		return "考核中"
	case PhaseFunded:
		return "出金账户"
	case PhasePayout:
		return "已出金"
	case PhaseBreached:
		return "已失败"
	}
	return phase
}

func (a *accountState) clone() *accountState {
	if a == nil {
		return nil
	}
	cp := *a
	return &cp
}

// balance 是「已实现口径」的余额：初始 + 本阶段已实现净额。手续费已经在净额里了。
func (a *accountState) balance(realized float64) float64 {
	return a.Rules.Initial + (realized - a.BaselinePnl)
}

// dayNet 是当日已实现净额，用来判「当日封顶」。
// 用已实现而不是权益：日内浮盈不该把人锁死（平掉才算数）。
func (a *accountState) dayNet(realized float64) float64 {
	return realized - a.DayStartPnl
}

// equityAt 是「如果现在价格是 price，权益是多少」：余额 + 该价格下的浮动盈亏。
// 判爆仓用它（含浮动），判封顶用 dayNet（不含浮动），两者刻意不同。
func (a *accountState) equityAt(inst *Instance, price float64) float64 {
	bal := a.balance(inst.RealizedPnl)
	q := inst.Position.Quantity
	if q == 0 {
		return bal
	}
	return bal + (price-inst.Position.AveragePrice)*q*inst.pointValue()
}

// applyFloor 按当前峰值算亏损线。只在日终和阶段切换时调用。
//
// 日终权益到 LockBalance（52100）之后，max() 给出的基准就是 52100，
// min(52100-2000, 50100) 自然等于 50100 —— 锁定不需要单独判一次，
// 但要把 Locked 记下来（界面上要说「已锁定」）。
func (a *accountState) applyFloor() {
	if a.Locked {
		a.Floor = a.Rules.FloorCap
		return
	}
	base := math.Max(a.Rules.Initial, a.PeakEOD)
	a.Floor = math.Min(base-a.Rules.MaxDrawdown, a.Rules.FloorCap)
	if a.Floor >= a.Rules.FloorCap {
		a.Floor = a.Rules.FloorCap
		a.Locked = true
	}
}

// accountSettleDay 在「跨交易日那一根」结算上一个交易日。
//
// 调用点在 advance：Cursor++ 之后、撮合之前，此时 Position 还是昨天收盘时的样子，
// prevBar 就是昨天最后一根 —— 日终权益就按它的收盘价估（含浮动）。
// 用含浮动的权益而不是已实现余额：盈利目标和回撤必须同一个口径，否则
// 「带着浮盈过夜不计入目标、带着浮亏过夜却算进回撤」就成了套利空间。
func (inst *Instance) accountSettleDay(prevBar Bar) {
	a := inst.account
	if a == nil {
		return
	}
	day := barDate(inst.Bars[inst.Cursor].Time)
	if day == "" || day == a.DayDate {
		return
	}
	eod := a.equityAt(inst, prevBar.Close)
	if eod > a.PeakEOD {
		a.PeakEOD = eod
	}
	a.applyFloor()

	switch a.Phase {
	case PhaseEval:
		if eod >= a.Rules.Initial+a.Rules.ProfitTarget {
			a.PendingFunded = true
			a.note = fmt.Sprintf("EVAL 达标：日终权益 %.2f ≥ %.2f，下一个交易日开盘进入 FUNDED（先平仓）",
				eod, a.Rules.Initial+a.Rules.ProfitTarget)
		}
	case PhaseFunded:
		if net := a.dayNet(inst.RealizedPnl); net >= a.Rules.ProfitDayMin {
			a.ProfitDays++
			a.note = fmt.Sprintf("第 %d/%d 个盈利日（当日净盈利 %.2f）", a.ProfitDays, a.Rules.ProfitDays, net)
		}
		if a.ProfitDays >= a.Rules.ProfitDays {
			a.Phase = PhasePayout
			a.Payout = eod - a.Rules.Initial
			a.ClosedAt = prevBar.Time
			a.note = fmt.Sprintf("FUNDED 达标，转为 PAYOUT：可出金 %.2f", a.Payout)
		}
	}

	a.DayDate = day
	a.DayStartPnl = inst.RealizedPnl
	a.DayLocked = false
	// 结算完才切换：新的一天从 FUNDED 的基线开始算，这一天也就成了第一个 FUNDED 日。
	a.maybeFunded(inst)
}

// maybeFunded 在「EVAL 已达标 + 空仓」时切到 FUNDED。
//
// **必须等空仓**：切换要把已实现盈亏的基线重置成现值（余额回 50000），
// 如果那时候还带着一笔浮亏，这笔亏损就凭空消失了 —— 这是整套规则里最好利用的洞。
// 所以宁可让用户先平仓（界面上有 waitingFund 提示），也不代他平仓：
// 强制平仓要在成交流水里凭空多出一笔不存在于用户操作里的成交，污染记录。
func (a *accountState) maybeFunded(inst *Instance) {
	if !a.PendingFunded || inst.Position.Quantity != 0 {
		return
	}
	a.Phase = PhaseFunded
	a.PendingFunded = false
	a.BaselinePnl = inst.RealizedPnl
	a.PeakEOD = a.Rules.Initial
	a.Locked = false
	a.applyFloor()
	a.ProfitDays = 0
	a.DayStartPnl = inst.RealizedPnl
	a.DayLocked = false
	a.note = fmt.Sprintf("进入 FUNDED：余额重置为 %.2f（EVAL 的盈利不转入），需要 %d 个盈利日（每个 ≥ %.2f）",
		a.Rules.Initial, a.Rules.ProfitDays, a.Rules.ProfitDayMin)
}

// accountEvaluate 判一次账户状态（不改订单）。返回 true = 本根不该再继续成交。
//
// 三个判定的顺序有意义：先看有没有该切阶段（可能刚平完仓就达标），
// 再判爆仓，最后判当日封顶。
func (inst *Instance) accountEvaluate(bar Bar) bool {
	a := inst.account
	if a == nil {
		return false
	}
	a.maybeFunded(inst)
	switch a.Phase {
	case PhaseBreached, PhasePayout:
		return false
	}

	// 判线用「最差点」：多头看最低价、空头看最高价。收盘价安全不代表盘中没打穿，
	// 只按收盘判会系统性偏乐观（和 OCO 里「止损优先」是同一套保守原则）。
	worst := bar.Close
	switch {
	case inst.Position.Quantity > 0:
		worst = bar.Low
	case inst.Position.Quantity < 0:
		worst = bar.High
	}
	if eq := a.equityAt(inst, worst); eq <= a.Floor {
		a.Phase = PhaseBreached
		a.FailReason = fmt.Sprintf("%s 权益 %.2f 触及亏损线 %.2f", bar.Time, eq, a.Floor)
		a.note = "考核失败：" + a.FailReason
		return true
	}

	if !a.DayLocked && a.dayNet(inst.RealizedPnl) >= a.Rules.DailyLimit {
		a.DayLocked = true
		a.note = fmt.Sprintf("当日净盈利 %.2f 已达上限 %.2f，今天不能再开新仓（平仓、撤单照常）",
			a.dayNet(inst.RealizedPnl), a.Rules.DailyLimit)
	}
	return false
}

// accountEnforce 执行 accountEvaluate 判出来的副作用：撤单。
//
// **不能放在撮合循环里**：循环正在按下标遍历 inst.Orders，中途改状态会让
// 「本根谁该成交」的判断建立在半改过的数据上。所以循环里只判定并 break，
// 撤单统一在这里做（幂等，重复调用无害）。
func (inst *Instance) accountEnforce() {
	a := inst.account
	if a == nil {
		return
	}
	if a.Phase != PhaseBreached && !a.DayLocked {
		return
	}
	cancelled := 0
	for i := range inst.Orders {
		o := &inst.Orders[i]
		if o.Status != OrderOpen {
			continue
		}
		// 爆仓：撤全部（账户已经终止，任何挂单都不该再成交）。
		// 封顶：只撤「会增加暴露」的，保护性止损/止盈留着 —— 那两张只会减仓。
		if a.Phase == PhaseBreached || inst.orderIncreases(*o) {
			o.Status = OrderCancelled
			cancelled++
		}
	}
	if cancelled > 0 {
		if a.note == "" {
			a.note = fmt.Sprintf("已撤销 %d 张挂单", cancelled)
		} else {
			a.note = fmt.Sprintf("%s；已撤销 %d 张挂单", a.note, cancelled)
		}
	}
}

// ---------------------------------------------------------------------------
// 下单闸门
// ---------------------------------------------------------------------------

// accountGate 是下单前的账户闸门。返回 nil = 放行。
//
// 只拦**会增加暴露**的单（开仓、加仓、超量反手）：考核失败或已出金之后，
// 用户仍然必须能把手里那一仓平掉、把挂单撤掉，否则账户就锁死了。
func (inst *Instance) accountGate(side string, qty float64) error {
	a := inst.account
	if a == nil {
		return nil
	}
	if !inst.increasesExposure(side, qty) {
		return nil
	}
	switch a.Phase {
	case PhaseBreached:
		return fmt.Errorf("考核已失败（%s）：只能平仓", a.FailReason)
	case PhasePayout:
		return fmt.Errorf("考核已结束（已出金 %.2f）：不能再开仓", a.Payout)
	}
	if a.DayLocked {
		return fmt.Errorf("当日净盈利 %.2f 已达上限 %.2f，今天不能再开仓",
			a.dayNet(inst.RealizedPnl), a.Rules.DailyLimit)
	}
	if exp := inst.exposure(side); exp+qty > a.Rules.MaxContracts+1e-9 {
		return fmt.Errorf("%s 最多 %.0f 手：现在持仓+挂单已占 %.0f 手，这一单会到 %.0f 手",
			inst.Symbol, a.Rules.MaxContracts, exp, exp+qty)
	}
	return nil
}

// increasesExposure 判断这一单会不会让持仓变大（含反手时超量的那部分）。
func (inst *Instance) increasesExposure(side string, qty float64) bool {
	pos := inst.Position.Quantity
	if pos == 0 {
		return true // 空仓下的任何一单都是开仓
	}
	if (side == "buy") == (pos > 0) {
		return true // 同向加仓
	}
	return qty > math.Abs(pos) // 反向但量大过持仓 = 反手，净暴露增加
}

// orderIncreases 判断一张挂单会不会增加暴露（封顶时只撤这些）。
func (inst *Instance) orderIncreases(o Order) bool {
	return inst.increasesExposure(o.Side, o.Quantity)
}

// exposure 是同方向的「持仓 + 未成交挂单」手数。
//
// 挂单必须算进去：否则「挂 40 张 1 手的买单，再手动市价 40 手」轻松突破上限。
// 反向挂单不计 —— 一个 40 手的仓位挂止损+止盈各 40 手，不该被算成 120 手。
func (inst *Instance) exposure(side string) float64 {
	total := 0.0
	if pos := inst.Position.Quantity; pos != 0 && (side == "buy") == (pos > 0) {
		total = math.Abs(pos)
	}
	for _, o := range inst.Orders {
		if o.Status == OrderOpen && o.Side == side {
			total += o.Quantity
		}
	}
	return total
}

// ---------------------------------------------------------------------------
// 下发
// ---------------------------------------------------------------------------

// dto 组装给前端的账户视图。所有数字在这里算好，前端只排版。
func (inst *Instance) accountDTO() *AccountDTO {
	a := inst.account
	if a == nil {
		return nil
	}
	rate := 0.0
	eq := a.balance(inst.RealizedPnl)
	if len(inst.Bars) > 0 && inst.Cursor >= 0 && inst.Cursor < len(inst.Bars) {
		rate = inst.currentBar().Close
		eq = a.equityAt(inst, rate)
	}
	return &AccountDTO{
		Rules:       a.Rules,
		Phase:       a.Phase,
		Balance:     a.balance(inst.RealizedPnl),
		Equity:      eq,
		Floor:       a.Floor,
		PeakEOD:     a.PeakEOD,
		Target:      a.Rules.Initial + a.Rules.ProfitTarget,
		ProfitDays:  a.ProfitDays,
		DayNet:      a.dayNet(inst.RealizedPnl),
		DayLocked:   a.DayLocked,
		Locked:      a.Locked,
		DayDate:     a.DayDate,
		Payout:      a.Payout,
		ClosedAt:    a.ClosedAt,
		FailReason:  a.FailReason,
		WaitingFund: a.PendingFunded,
		Days:        inst.accountDays(),
	}
}

// accountDays 把「已经揭示的 K 线」按交易日切成日历，并把成交按各自所在的那一天归集。
//
// 判据用的是 `barDate` —— 也就是**日文件那一天**，和 accountSettleDay 日终结算同一把尺子。
// 不能用「盘间断档」（session.go）来切：CME 每天 05:00 那个结算断档也在 30 分钟以上，
// 那样一个交易日会被切成两格，日历上的「当日盈亏」就和日终结算对不上。
// 也不能让前端按 `time[:10]` 自己分：口径一散，两处数字迟早打架。
//
// Pnl 用成交的 RealizedPnl 累加（开仓那笔是负数手续费），所以「一天赚了多少」和
// 余额的变化是同一个口径；Cum 只是它的前缀和，方便看进度。
func (inst *Instance) accountDays() []AccountDayDTO {
	if len(inst.Bars) == 0 || inst.Cursor < 0 {
		return nil
	}
	last := inst.Cursor
	if last >= len(inst.Bars) {
		last = len(inst.Bars) - 1
	}

	days := make([]AccountDayDTO, 0, 8)
	for i := 0; i <= last; i++ {
		b := inst.Bars[i]
		if i == 0 || barDate(inst.Bars[i-1].Time) != barDate(b.Time) {
			days = append(days, AccountDayDTO{Date: barDate(b.Time), Start: b.Time})
		}
		d := &days[len(days)-1]
		d.End = b.Time
		d.Bars++
	}

	// 成交按时间落到某一天：Time 和 K 线是同一把明文尺子，直接比字符串。
	// 指针只往后走（成交天然有序），所以整段是线性的。
	at := 0
	for _, t := range inst.Trades {
		for at+1 < len(days) && t.Time >= days[at+1].Start {
			at++
		}
		if t.Time < days[at].Start {
			continue // 锚点之前的成交（正常不会有）
		}
		days[at].Pnl += t.RealizedPnl
		days[at].Trades++
	}

	cum := 0.0
	for i := range days {
		cum += days[i].Pnl
		days[i].Cum = cum
	}
	days[len(days)-1].Current = true
	return days
}
