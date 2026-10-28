package replay

// 考核账户的规则钉子。所有用例都用 TEST:ABC 那个 1 美元/点的口径（Tick/TickValue 不填，
// pointValue 退化成 1），这样金额能直接口算；手续费在 account_test 里一律设 0，
// 扣费口径由 commission_test.go 单独钉住。

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// accountBars 造 days 天、每天两根（09:00 开盘 + 15:55 收盘），
// 天与天之间隔了 17 小时（> 30 分钟），isSessionOpen 会认成交易日分界。
func accountBars(days int) []Bar {
	base := []string{"09:00", "15:55"}
	out := make([]Bar, 0, days*2)
	for d := 0; d < days; d++ {
		day := fmt.Sprintf("2026-01-%02d", 5+d)
		for _, hm := range base {
			out = append(out, Bar{Time: day + " " + hm, Open: 100, High: 101, Low: 99, Close: 100})
		}
	}
	return out
}

func accountTemplate(t *testing.T) AccountRules {
	t.Helper()
	r, ok := accountTemplateByID("lucid-flex-50k")
	if !ok {
		t.Fatal("内置模板 lucid-flex-50k 不见了")
	}
	return r
}

// accountInstance 造一个考核账户实例：已建户、费率 0、游标在第 1 天开盘。
func accountInstance(t *testing.T, bars []Bar) *Instance {
	t.Helper()
	inst := &Instance{
		ID: "ins-acct", Symbol: testSymbol, Folder: testFolder,
		Bars: bars, Cursor: 0, Operator: "human",
	}
	inst.account = newAccountState(accountTemplate(t))
	return inst
}

func TestAccountInitialState(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	dto := inst.accountDTO()
	if dto.Phase != PhaseEval {
		t.Fatalf("新建账户应当是 EVAL，实际 %q", dto.Phase)
	}
	if dto.Balance != 50000 || dto.PeakEOD != 50000 {
		t.Fatalf("初始余额/峰值都该是 50000：balance=%v peak=%v", dto.Balance, dto.PeakEOD)
	}
	// 一开始就有 2000 的缓冲：亏损线 = 50000 - 2000
	if dto.Floor != 48000 {
		t.Fatalf("初始亏损线应当是 48000，实际 %v", dto.Floor)
	}
	if dto.Target != 53000 {
		t.Fatalf("EVAL 目标余额应当是 53000，实际 %v", dto.Target)
	}
	if dto.Locked || dto.DayLocked || dto.ProfitDays != 0 {
		t.Fatalf("新建账户不该有锁定/盈利日：%+v", dto)
	}
}

// TestAccountFloorTrailsEOD 钉住日终追踪回撤：亏损线跟着日终峰值上移，
// 到 52100 之后锁在 50100 不再动。
func TestAccountFloorTrailsEOD(t *testing.T) {
	cases := []struct {
		peak      float64
		wantFloor float64
		wantLock  bool
	}{
		{50000, 48000, false}, // 建户
		{51000, 49000, false}, // 涨了 1000，线跟着上移 1000
		{52000, 50000, false},
		{52100, 50100, true}, // 到达锁定点：min(50100, 50100)
		{51500, 50100, true}, // 已经锁定，峰值回落也不下移（先锁定再判）
	}
	a := newAccountState(accountTemplate(t))
	for _, c := range cases {
		a.PeakEOD = c.peak
		a.applyFloor()
		if a.Floor != c.wantFloor || a.Locked != c.wantLock {
			t.Fatalf("峰值 %.2f：期望 floor=%.2f locked=%v，实际 floor=%.2f locked=%v",
				c.peak, c.wantFloor, c.wantLock, a.Floor, a.Locked)
		}
	}
}

// TestAccountFloorLockBoundary 钉住边界：差一分钱都还没锁。
func TestAccountFloorLockBoundary(t *testing.T) {
	a := newAccountState(accountTemplate(t))
	a.PeakEOD = 52099.99
	a.applyFloor()
	if a.Locked {
		t.Fatal("峰值 52099.99 还没到锁定点，不该锁")
	}
	if math.Abs(a.Floor-50099.99) > 1e-9 {
		t.Fatalf("亏损线应当是 50099.99，实际 %v", a.Floor)
	}
}

// TestAccountBreachAtExactlyFloor 钉住「等于即爆」：
// 真实规则里「触及」包含等于，所以用 <= 而不是 <。
func TestAccountBreachAtExactlyFloor(t *testing.T) {
	// 100 手、成本 100：最低价 80 时权益正好 50000 + (80-100)*100 = 48000 = 亏损线
	mk := func(low float64) *Instance {
		inst := accountInstance(t, accountBars(2))
		inst.Position = Position{Quantity: 100, AveragePrice: 100}
		return inst
	}
	if !mk(80).accountEvaluate(Bar{Time: "2026-01-05 09:00", High: 101, Low: 80, Close: 100}) {
		t.Fatal("权益正好等于亏损线就该判违规")
	}
	if mk(80.01).accountEvaluate(Bar{Time: "2026-01-05 09:00", High: 101, Low: 80.01, Close: 100}) {
		t.Fatal("权益高出 1 美元不该判违规")
	}
}

// TestAccountBreachUsesWorstPrice 钉住「看最差点」：收盘价好看不代表盘中没打穿。
func TestAccountBreachUsesWorstPrice(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	inst.Position = Position{Quantity: 100, AveragePrice: 100}
	// 收盘 101（权益 50100，安全）但最低 79（权益 47900，爆）
	if !inst.accountEvaluate(Bar{Time: "2026-01-05 09:00", High: 102, Low: 79, Close: 101}) {
		t.Fatal("盘中最低价打穿亏损线就该判违规，不能只看收盘")
	}

	// 空头看最高价
	short := accountInstance(t, accountBars(2))
	short.Position = Position{Quantity: -100, AveragePrice: 100}
	// 高价 121：50000 + (121-100)*(-100) = 47900
	if !short.accountEvaluate(Bar{Time: "2026-01-05 09:00", High: 121, Low: 99, Close: 100}) {
		t.Fatal("空头要按最高价判，盘中冲高打穿也算违规")
	}
}

// TestAccountBreachCancelsOrders 钉住爆仓后的善后：所有挂单撤销，之后只能平仓。
func TestAccountBreachCancelsOrders(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	inst.Position = Position{Quantity: 100, AveragePrice: 100}
	inst.Orders = []Order{
		{ID: "o1", Side: "buy", Kind: KindLimit, Price: 90, Quantity: 5, Status: OrderOpen},
		{ID: "o2", Side: "sell", Kind: KindStop, Price: 70, Quantity: 100, Status: OrderOpen},
	}
	inst.accountEvaluate(Bar{Time: "2026-01-05 09:00", High: 101, Low: 80, Close: 100})
	inst.accountEnforce()

	if inst.account.Phase != PhaseBreached {
		t.Fatalf("应当已爆仓，实际 %q", inst.account.Phase)
	}
	for _, o := range inst.Orders {
		if o.Status != OrderCancelled {
			t.Fatalf("爆仓后挂单应当全部撤销：%s 还是 %s", o.ID, o.Status)
		}
	}
	if inst.account.FailReason == "" {
		t.Fatal("爆仓要留下原因，界面上要显示")
	}
	if err := inst.accountGate("buy", 1); err == nil {
		t.Fatal("爆仓后开仓必须被拒")
	}
	// 平仓仍然放行：否则用户手里那一仓就永远出不掉了
	if err := inst.accountGate("sell", 100); err != nil {
		t.Fatalf("爆仓后平仓应当放行：%v", err)
	}
}

// TestAccountDailyLimit 钉住当日封顶：净盈利到 1200 就禁止再开仓，平仓照常。
func TestAccountDailyLimit(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	inst.Position = Position{Quantity: 5, AveragePrice: 100}
	inst.RealizedPnl = 1200
	bar := Bar{Time: "2026-01-05 09:00", High: 101, Low: 99, Close: 100}
	inst.accountEvaluate(bar)

	if !inst.account.DayLocked {
		t.Fatalf("当日净盈利 1200 应当封顶")
	}
	if err := inst.accountGate("buy", 1); err == nil {
		t.Fatal("封顶后加仓必须被拒")
	}
	if err := inst.accountGate("sell", 5); err != nil {
		t.Fatalf("封顶后平仓应当放行：%v", err)
	}

	// 封顶那一刻要撤掉「会增加暴露」的挂单 —— 否则规则只对点按钮的人生效
	inst.Orders = []Order{
		{ID: "o1", Side: "buy", Kind: KindLimit, Price: 90, Quantity: 2, Status: OrderOpen},
		{ID: "o2", Side: "sell", Kind: KindStop, Price: 80, Quantity: 5, Status: OrderOpen},
	}
	inst.accountEnforce()
	if inst.Orders[0].Status != OrderCancelled {
		t.Fatal("封顶后同向挂单应当撤销")
	}
	if inst.Orders[1].Status != OrderOpen {
		t.Fatal("保护性止损只会减仓，封顶时不该撤")
	}

	// 净盈利 1199.99 不该封顶
	inst2 := accountInstance(t, accountBars(2))
	inst2.RealizedPnl = 1199.99
	inst2.accountEvaluate(bar)
	if inst2.account.DayLocked {
		t.Fatal("1199.99 还没到 1200，不该封顶")
	}
}

// TestAccountMaxContractsIncludesOpenOrders 钉住 40 手上限：
// 持仓 + 同向未成交挂单一起算，反向保护单不算。
func TestAccountMaxContractsIncludesOpenOrders(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	inst.Position = Position{Quantity: 38, AveragePrice: 100}
	inst.Orders = []Order{
		{ID: "o1", Side: "buy", Kind: KindLimit, Price: 90, Quantity: 3, Status: OrderOpen},
		{ID: "o2", Side: "sell", Kind: KindStop, Price: 80, Quantity: 38, Status: OrderOpen},
	}
	// 持仓 38 + 同向挂单 3 = 41，再加 1 手就是 42
	if err := inst.accountGate("buy", 1); err == nil {
		t.Fatal("超过 40 手必须被拒")
	}
	if err := inst.accountGate("sell", 38); err != nil {
		t.Fatalf("平仓不会被手数上限拦：%v", err)
	}
	if err := inst.accountGate("sell", 1); err != nil {
		t.Fatalf("减仓不会被手数上限拦：%v", err)
	}
}

// TestAccountFundedResetsBaseline 钉住「EVAL 的盈利不转入 FUNDED」：
// 达标后的下一个交易日开盘切阶段，余额回到 50000、亏损线回到 48000。
func TestAccountFundedResetsBaseline(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	inst.RealizedPnl = 3000 // 余额 53000，正好达标
	ds := emptyDS()

	if _, _, err := inst.advance(ds); err != nil { // 当天 15:55
		t.Fatalf("步进失败：%v", err)
	}
	if _, _, err := inst.advance(ds); err != nil { // 次日开盘 → 结算 + 切 FUNDED
		t.Fatalf("步进失败：%v", err)
	}

	a := inst.account
	if a.Phase != PhaseFunded {
		t.Fatalf("EVAL 达标后应当进入 FUNDED，实际 %q（pending=%v）", a.Phase, a.PendingFunded)
	}
	if a.balance(inst.RealizedPnl) != 50000 {
		t.Fatalf("FUNDED 余额应当重置为 50000，实际 %v", a.balance(inst.RealizedPnl))
	}
	if a.Floor != 48000 || a.PeakEOD != 50000 || a.Locked {
		t.Fatalf("FUNDED 的亏损线要重新起算：floor=%v peak=%v locked=%v", a.Floor, a.PeakEOD, a.Locked)
	}
	if a.ProfitDays != 0 {
		t.Fatalf("FUNDED 的盈利日从 0 开始，实际 %d", a.ProfitDays)
	}
}

// TestAccountFundedWaitsForFlat 钉住「带着仓不切阶段」：
// 基线重置会抹掉过夜浮亏，所以必须等空仓 —— 否则那笔亏损就凭空消失了。
func TestAccountFundedWaitsForFlat(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	inst.RealizedPnl = 3000
	inst.Position = Position{Quantity: 1, AveragePrice: 100}
	ds := emptyDS()

	if _, _, err := inst.advance(ds); err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if _, _, err := inst.advance(ds); err != nil { // 次日开盘，但还带着仓
		t.Fatalf("步进失败：%v", err)
	}
	if inst.account.Phase != PhaseEval {
		t.Fatalf("带着仓不能切 FUNDED，实际 %q", inst.account.Phase)
	}
	if !inst.account.PendingFunded {
		t.Fatal("应当记下「已达标，等平仓」")
	}
	if dto := inst.accountDTO(); !dto.WaitingFund {
		t.Fatal("界面上要能看到「等待平仓后进入 FUNDED」")
	}

	// 平掉之后立刻切
	inst.Position = Position{}
	inst.accountEvaluate(inst.currentBar())
	if inst.account.Phase != PhaseFunded {
		t.Fatalf("空仓后应当切到 FUNDED，实际 %q", inst.account.Phase)
	}
}

// TestAccountEvalTargetIsNet 钉住「按扣完手续费的余额判达标」：
// 毛赚 3000、手续费 1.0 时余额 52999，不能算过。
func TestAccountEvalTargetIsNet(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	inst.RealizedPnl = 2999 // 毛 3000 - 手续费 1
	ds := emptyDS()
	if _, _, err := inst.advance(ds); err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if _, _, err := inst.advance(ds); err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	if inst.account.PendingFunded {
		t.Fatalf("余额 52999 < 53000，不该达标（手续费必须算进去）")
	}
}

// TestAccountProfitDayMinAndPayout 钉住 FUNDED 的两条：盈利日门槛 150、
// 攒够 5 天转 PAYOUT 并记下出金金额。
func TestAccountProfitDayMinAndPayout(t *testing.T) {
	inst := accountInstance(t, accountBars(7))
	inst.account.Phase = PhaseFunded
	ds := emptyDS()

	// 第 1 天只赚 149.99：不该计一个盈利日
	if _, _, err := inst.advance(ds); err != nil { // 当天 15:55
		t.Fatalf("步进失败：%v", err)
	}
	inst.RealizedPnl += 149.99
	if _, _, err := inst.advance(ds); err != nil { // 次日开盘结算
		t.Fatalf("步进失败：%v", err)
	}
	if inst.account.ProfitDays != 0 {
		t.Fatalf("149.99 不够 150，不该计盈利日，实际 %d", inst.account.ProfitDays)
	}

	// 再攒 5 天、每天 150
	for i := 0; i < 5; i++ {
		if _, _, err := inst.advance(ds); err != nil {
			t.Fatalf("步进失败：%v", err)
		}
		inst.RealizedPnl += 150
		if _, _, err := inst.advance(ds); err != nil {
			t.Fatalf("步进失败：%v", err)
		}
	}
	if inst.account.Phase != PhasePayout {
		t.Fatalf("5 个盈利日应当转 PAYOUT，实际 %q（%d 天）", inst.account.Phase, inst.account.ProfitDays)
	}
	if math.Abs(inst.account.Payout-(149.99+5*150)) > 1e-6 {
		t.Fatalf("出金金额应当是全部净盈利，实际 %v", inst.account.Payout)
	}
	if inst.account.ClosedAt == "" {
		t.Fatal("PAYOUT 要记下达成时间")
	}
	// PAYOUT 之后只能平仓
	if err := inst.accountGate("buy", 1); err == nil {
		t.Fatal("PAYOUT 之后不能再开仓")
	}
}

// TestAccountPayoutAllowsClose 钉住「出金后仍然能把仓位平掉」，否则账户就锁死了。
func TestAccountPayoutAllowsClose(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	inst.account.Phase = PhasePayout
	inst.Position = Position{Quantity: 3, AveragePrice: 100}
	if err := inst.accountGate("sell", 3); err != nil {
		t.Fatalf("PAYOUT 之后平仓应当放行：%v", err)
	}
	if err := inst.accountGate("buy", 1); err == nil {
		t.Fatal("PAYOUT 之后开仓必须被拒")
	}
}

// TestAccountSettlesOncePerDay 钉住日终去重：同一交易日结算两次不会多计盈利日，
// 也不会把亏损线往上推错。
func TestAccountSettlesOncePerDay(t *testing.T) {
	inst := accountInstance(t, accountBars(3))
	inst.account.Phase = PhaseFunded
	ds := emptyDS()

	if _, _, err := inst.advance(ds); err != nil {
		t.Fatalf("步进失败：%v", err)
	}
	inst.RealizedPnl += 200
	if _, _, err := inst.advance(ds); err != nil { // 次日开盘 → 结算第 1 天
		t.Fatalf("步进失败：%v", err)
	}
	if inst.account.ProfitDays != 1 {
		t.Fatalf("应当计 1 个盈利日，实际 %d", inst.account.ProfitDays)
	}
	// 手动再结算一次同一根（模拟数据里多出一个缺口）：DayDate 已经挡住
	inst.accountSettleDay(inst.Bars[inst.Cursor-1])
	if inst.account.ProfitDays != 1 {
		t.Fatalf("同一交易日不该重复计盈利日，实际 %d", inst.account.ProfitDays)
	}
}

// TestAccountRulesFrozen 钉住「建户时快照规则」：改账户里的规则不会影响模板本身。
func TestAccountRulesFrozen(t *testing.T) {
	before := accountTemplate(t).Initial
	inst := accountInstance(t, accountBars(2))
	inst.account.Rules.Initial = 1
	if got := accountTemplate(t).Initial; got != before {
		t.Fatalf("模板被改动了：%v → %v", before, got)
	}
}

// TestAccountFreeInstanceUntouched 钉住「自由模式完全不受影响」：
// 没有 account 时判线和闸门都是空操作。
func TestAccountFreeInstanceUntouched(t *testing.T) {
	inst := newTradingInstance()
	inst.Position = Position{Quantity: 100, AveragePrice: 100}
	if inst.accountEvaluate(Bar{Time: "2026-01-05 09:00", High: 101, Low: 1, Close: 100}) {
		t.Fatal("自由模式没有亏损线，不该判违规")
	}
	if err := inst.accountGate("buy", 10000); err != nil {
		t.Fatalf("自由模式不受手数上限约束：%v", err)
	}
	if inst.accountDTO() != nil {
		t.Fatal("自由模式不该下发账户字段")
	}
}

// TestAccountPersistRoundTrip 钉住落盘：账户逐字段回来，且 toFiles 交出去的是副本
// （flushState 在锁外序列化，交指针会和结算写同一块内存）。
func TestAccountPersistRoundTrip(t *testing.T) {
	inst := accountInstance(t, accountBars(2))
	inst.account.Phase = PhaseBreached
	inst.account.FailReason = "测试"
	inst.account.PeakEOD = 51500
	inst.account.applyFloor()
	inst.account.Floor = 49500
	inst.account.ProfitDays = 2

	f := inst.toFiles()
	if f.Meta.Account == nil {
		t.Fatal("meta 里没有账户")
	}
	// 改副本不该影响原对象
	f.Meta.Account.ProfitDays = 99
	if inst.account.ProfitDays != 2 {
		t.Fatal("toFiles 必须交出副本，不能是同一个指针")
	}

	back := f.toInstance()
	if back.account == nil {
		t.Fatal("读回来没有账户")
	}
	if back.account.Phase != PhaseBreached || back.account.FailReason != "测试" ||
		back.account.Floor != 49500 || back.account.ProfitDays != 99 {
		t.Fatalf("账户字段没完整回来：%+v", back.account)
	}
}

// TestAccountLegacyArchiveNoAccount 钉住兼容：没有 account 字段的老存档读出来就是自由模式。
func TestAccountLegacyArchiveNoAccount(t *testing.T) {
	inst := newTradingInstance()
	f := inst.toFiles()
	back := f.toInstance()
	if back.account != nil {
		t.Fatal("老存档不该凭空长出账户")
	}
}

// ---------------------------------------------------------------------------
// 建户这条链路（hub 层）
// ---------------------------------------------------------------------------

// writeCommissionData 造一份「有手续费档位」的数据目录（MNQ），
// 每天一根 09:00 的 K 线，天与天之间隔 24 小时（会被 isSessionOpen 认成交易日分界）。
//
// 不能用 writeTestData：那个标的叫 TEST:ABC，没有佣金档位，
// 而「没有档位就建不了考核账户」正是要验的规则之一。
func writeCommissionData(t *testing.T, days int) string {
	t.Helper()
	root := t.TempDir()
	const symbol, folder = "CME_MINI:MNQ1!", "CME_MINI_MNQ1!"
	if err := os.MkdirAll(filepath.Join(root, folder), 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	idx := map[string]any{
		"version": 1,
		"symbols": map[string]any{symbol: map[string]any{
			"folder": folder, "tick": 0.25, "tickValue": 0.5,
		}},
	}
	buf, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "_symbols.json"), buf, 0o644); err != nil {
		t.Fatalf("写索引失败：%v", err)
	}
	base := time.Date(2026, 1, 5, 9, 0, 0, 0, time.Local)
	for d := 0; d < days; d++ {
		day := base.AddDate(0, 0, d)
		name := filepath.Join(root, folder, day.Format("2006-01-02")+".csv")
		f, err := os.Create(name)
		if err != nil {
			t.Fatalf("建 CSV 失败：%v", err)
		}
		fmt.Fprintln(f, "time,open,high,low,close,volume")
		for i := 0; i < 3; i++ {
			ts := day.Add(time.Duration(i*5) * time.Minute)
			fmt.Fprintf(f, "%s,20000,20001,19999,20000,10\n", ts.Format("2006-01-02 15:04"))
		}
		f.Close()
	}
	return root
}

// TestAccountCreateRejectsFreeSymbol 钉住「没有佣金档位的标的建不了考核账户」：
// 费率 0 会让净盈亏退化成毛盈亏，那套目标和回撤就不是同一套规则了。
func TestAccountCreateRejectsFreeSymbol(t *testing.T) {
	hub, _ := newTestHub(t, 5)
	c := newFakeClient(t, hub)
	c.waitFor(MsgWelcome)

	c.send(hub, map[string]any{
		"type": "create_instance", "symbol": testSymbol,
		"accountTemplate": "lucid-flex-50k", "bars": 40,
	})
	e := c.waitFor(MsgError)
	if !strings.Contains(e["message"].(string), "手续费档位") {
		t.Fatalf("错误提示该说清为什么：%v", e["message"])
	}
	if len(hub.instances) != 0 {
		t.Fatal("被拒的建户不该留下实例")
	}
}

// TestAccountCreateAttachesAccount 钉住建户主链路：
// 选模板 → 随机一个交易日开盘 → 实例挂上账户，规则是快照、余额 50000、亏损线 48000。
func TestAccountCreateAttachesAccount(t *testing.T) {
	root := writeCommissionData(t, 4)
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	c := newFakeClient(t, hub)
	c.waitFor(MsgWelcome)

	c.send(hub, map[string]any{
		"type": "create_instance", "symbol": "CME_MINI:MNQ1!",
		"accountTemplate": "lucid-flex-50k", "bars": 40,
	})
	st := c.waitFor(MsgInstanceState)
	inst := st["instance"].(map[string]any)

	acct, ok := inst["account"].(map[string]any)
	if !ok {
		t.Fatalf("建出来的实例没有账户：%v", inst)
	}
	if acct["phase"] != PhaseEval {
		t.Fatalf("新账户应当是 EVAL，实际 %v", acct["phase"])
	}
	if acct["balance"] != 50000.0 || acct["floor"] != 48000.0 {
		t.Fatalf("初始余额/亏损线不对：%v / %v", acct["balance"], acct["floor"])
	}
	rules := acct["rules"].(map[string]any)
	if rules["initial"] != 50000.0 || rules["profitTarget"] != 3000.0 ||
		rules["maxDrawdown"] != 2000.0 || rules["lockBalance"] != 52100.0 ||
		rules["floorCap"] != 50100.0 || rules["dailyLimit"] != 1200.0 ||
		rules["maxContracts"] != 40.0 || rules["profitDays"] != 5.0 {
		t.Fatalf("规则快照不对：%v", rules)
	}
	// 锚点必须是当天较早的一根（开盘首根），不能是「现在」
	anchor, _ := inst["anchor"].(string)
	if !strings.HasSuffix(anchor, " 09:00") {
		t.Fatalf("考核账户要锚在开盘首根，实际 %s", anchor)
	}
}

// TestAccountNoteBecomesEvent 钉住「账户节点只在广播出口发一条」：
// note 由结算/封顶写进账户，broadcastInstanceLocked 末尾消费它，
// 先清再发 —— 否则会自己递归下去。
func TestAccountNoteBecomesEvent(t *testing.T) {
	hub, _ := newTestHub(t, 3)
	c := newFakeClient(t, hub)
	c.waitFor(MsgWelcome)
	id := createInstance(t, hub, c, "2026-01-06 09:30")

	inst := hub.instances[id]
	inst.account = newAccountState(accountTemplate(t))
	const wantNote = "第 1/5 个盈利日（当日净盈利 200.00）"
	inst.account.note = wantNote

	hub.broadcastInstanceLocked(inst, ActBarAdvanced, map[string]any{"cursor": 1}, "human")

	// 建户时还会排一条 instances 广播，先跳过非 event 的
	var seen []string
	for len(seen) < 2 {
		m := c.next()
		if m["type"] != MsgEvent {
			continue
		}
		seen = append(seen, m["action"].(string))
		if m["action"] == ActAccountChanged {
			p := m["payload"].(map[string]any)
			if p["note"] != wantNote {
				t.Fatalf("account_changed 必须带上那句中文，实际 %v", p["note"])
			}
			if _, ok := p["account"].(map[string]any); !ok {
				t.Fatal("account_changed 必须带完整账户视图")
			}
		}
	}
	if seen[0] != ActBarAdvanced || seen[1] != ActAccountChanged {
		t.Fatalf("广播顺序不对：%v", seen)
	}
	if inst.account.note != "" {
		t.Fatal("note 发出去之后要清掉，否则每次广播都会再发一遍")
	}
}

// TestAccountBotRefusedWhenEnded 钉住「终止的账户起不了机器人」：
// 起来只会被闸门一张张拒掉、刷一屏错误，不如一开始就说清楚。
func TestAccountBotRefusedWhenEnded(t *testing.T) {
	hub, _ := newTestHub(t, 3)
	c := newFakeClient(t, hub)
	c.waitFor(MsgWelcome)
	id := createInstance(t, hub, c, "2026-01-06 09:30")

	inst := hub.instances[id]
	inst.account = newAccountState(accountTemplate(t))
	inst.account.Phase = PhasePayout

	c.send(hub, map[string]any{"type": ReqBotStart, "instanceId": id})
	e := c.waitFor(MsgError)
	if !strings.Contains(e["message"].(string), "已经结束") {
		t.Fatalf("错误提示该说清账户已经结束：%v", e["message"])
	}
	if inst.botStop != nil {
		t.Fatal("被拒的启动不该留下机器人状态")
	}
}

// TestAccountEndedStopsBot 钉住「账户一终止就把机器人停掉」：
// 否则它下一根还会去下单，只会被一张张拒掉。
func TestAccountEndedStopsBot(t *testing.T) {
	hub, _ := newTestHub(t, 3)
	c := newFakeClient(t, hub)
	c.waitFor(MsgWelcome)
	id := createInstance(t, hub, c, "2026-01-06 09:30")

	inst := hub.instances[id]
	inst.account = newAccountState(accountTemplate(t))
	inst.account.Phase = PhaseBreached
	inst.account.note = "考核失败：权益 47990.00 触及亏损线 48000.00"
	// 假装机器人在跑：botStop 非空就是这个标记，这里不需要真的起协程。
	inst.botStop = make(chan struct{})
	inst.Operator = "bot"

	hub.broadcastInstanceLocked(inst, ActBarAdvanced, map[string]any{"cursor": 1}, "system")

	if inst.botStop != nil {
		t.Fatal("账户终止后机器人应当被停掉")
	}
	if inst.Operator != "" {
		t.Fatalf("机器人停下后操作权应当交还（空 = 谁都还没接管），实际 %q", inst.Operator)
	}
	// 事件流里要能看到 bot_stopped 和 account_changed（顺序无所谓，都在这一轮里）
	got := map[string]bool{}
	for i := 0; i < 10 && !(got[ActBotStopped] && got[ActAccountChanged]); i++ {
		m := c.next()
		if m["type"] != MsgEvent {
			continue
		}
		got[m["action"].(string)] = true
	}
	if !got[ActBotStopped] || !got[ActAccountChanged] {
		t.Fatalf("事件流缺东西：%v", got)
	}
}
