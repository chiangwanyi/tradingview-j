package replay

// 交易日历（AccountDTO.Days）的钉子：一天 = 一个日文件（barDate），
// 成交要落到它自己那一天，游标之后没揭示的 K 线一根都不能算进来。

import "testing"

func TestAccountDaysSplitBySessionGap(t *testing.T) {
	inst := accountInstance(t, accountBars(3)) // 3 天 × 2 根，天与天之间隔 17 小时
	inst.Cursor = 5                            // 第 3 天的收盘那根：三天全走完
	inst.Trades = []Trade{
		{Time: "2026-01-05 10:00", RealizedPnl: 120},
		{Time: "2026-01-06 10:00", RealizedPnl: -50},
		{Time: "2026-01-06 15:00", RealizedPnl: 130},
		{Time: "2026-01-07 15:00", RealizedPnl: 30},
	}

	days := inst.accountDays()
	if len(days) != 3 {
		t.Fatalf("3 个交易日应当切成 3 格，实际 %d：%+v", len(days), days)
	}
	want := []AccountDayDTO{
		{Date: "2026-01-05", Start: "2026-01-05 09:00", End: "2026-01-05 15:55", Bars: 2, Trades: 1, Pnl: 120, Cum: 120},
		{Date: "2026-01-06", Start: "2026-01-06 09:00", End: "2026-01-06 15:55", Bars: 2, Trades: 2, Pnl: 80, Cum: 200},
		{Date: "2026-01-07", Start: "2026-01-07 09:00", End: "2026-01-07 15:55", Bars: 2, Trades: 1, Pnl: 30, Cum: 230, Current: true},
	}
	for i := range want {
		if days[i] != want[i] {
			t.Fatalf("第 %d 天对不上：\n got %+v\nwant %+v", i+1, days[i], want[i])
		}
	}
}

// 游标之后的部分（已加载但没揭示）不该出现在日历里，最后一天要标成「正在过」。
func TestAccountDaysStopAtCursor(t *testing.T) {
	inst := accountInstance(t, accountBars(3))
	inst.Cursor = 1 // 只走到第 1 天收盘

	days := inst.accountDays()
	if len(days) != 1 {
		t.Fatalf("只走到第 1 天，日历就该只有 1 格，实际 %d", len(days))
	}
	if !days[0].Current {
		t.Fatal("最后一格（正在过的这天）应当标 Current")
	}
	if days[0].End != "2026-01-05 15:55" {
		t.Fatalf("End 应当是游标那根，实际 %q", days[0].End)
	}
}
