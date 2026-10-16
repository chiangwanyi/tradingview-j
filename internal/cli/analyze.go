package cli

// 菜单里的「2  分析已抓取的数据」。
//
// 这一条路径**只读**：走的是 store.Scan（纯扫描），而不是 store.Store。
// Store 是给写入用的 —— 它一 Open 就会建数据目录、会在 _symbols.json 里分配
// 标的目录名。统计动作不该对数据产生任何副作用，所以这里刻意绕开它。
//
// 本文件只负责「框」：标题、口径说明、空数据的提示、把结果交给 internal/termreport。
// 统计口径在 store，排版在 termreport，这里一行统计都不算。

import (
	"time"

	"tradingview-j/internal/store"
	"tradingview-j/internal/termreport"
)

func (a *App) runAnalyze() int {
	cfg, ok := a.loadConfig()
	if !ok {
		return 1
	}
	loc := time.Local
	root := cfg.OutPath()

	a.Log.Rule(72)
	a.Log.Plain("  数据分析 · %s", root)
	a.Log.Plain("  一根 K 线 %d 秒（config.json 的 intervalSeconds）· 时区 %s", cfg.IntervalSeconds, loc)
	a.Log.Rule(72)

	res := store.Scan(root, loc, cfg.IntervalSeconds)
	for _, n := range res.Notes {
		a.Log.Warn("%s", n)
	}
	if len(res.Folders) == 0 {
		if res.Exists {
			a.Log.Plain("  数据目录里还没有任何 K 线。")
			a.Log.Plain("  先选 1 启动抓取，然后在 TradingView 图表上拖一拖，数据就会落到这里。")
		}
		a.Log.Rule(72)
		return 0
	}

	termreport.Print(a.Log, res, loc)
	a.Log.Rule(72)
	return 0
}
