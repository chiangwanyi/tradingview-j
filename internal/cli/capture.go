package cli

// 菜单里的「1  启动抓取」。
//
// 这里只做编排：读配置 → 开落盘出口 → **起网页版分析** → 起抓取会话 → 等 Ctrl+C
// → 刷盘 → 停网页 → 报本次写入了什么。抓取本身（等调试端口、等图表页、挂监听、解帧）
// 在 internal/capture，网页在 internal/webreport。
//
// 网页服务和抓取是同一个生命周期：抓取一起步它就起来（浏览器也自动打开），
// 抓取一停它就跟着停。中间没有任何「同步状态」的代码 ——
// 页面靠磁盘指纹发现新数据（见 webreport.snapshotter），
// 而抓取那边每 flushSeconds 就已经把当天那份 CSV 重写了一遍。

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tradingview-j/internal/capture"
	"tradingview-j/internal/config"
	"tradingview-j/internal/store"
)

// runCapture 就是菜单里的「1」。返回后回到菜单，由调用方决定怎么处理退出码。
func (a *App) runCapture() int {
	cfg, ok := a.loadConfig()
	if !ok {
		return 1
	}
	a.printConfig(cfg)

	st, err := store.Open(store.Options{
		Root:     cfg.OutPath(),
		Header:   cfg.CSVHeader,
		Location: time.Local,
		Log:      func(format string, args ...any) { a.Log.Info(format, args...) },
	})
	if err != nil {
		a.Log.Error("%v", err)
		pauseIfNeeded(cfg.PauseOnExit)
		return 1
	}

	// 网页先起：抓取要一直等着 Chrome 和图表页，这段时间页面已经在等着了，
	// 用户可以在浏览器里先把分析页放到一边，等数据自己长出来。
	// 起不来不影响抓取（返回 nil），只是少了那个观察窗。
	web := a.startAnalysis(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	sess := capture.New(cfg, st, a.Log)
	a.Log.Info("开始运行。要停止：在这个窗口按 Ctrl+C（会先刷盘、再关掉网页，然后回到菜单）")
	a.Log.Rule(72)

	runErr := sess.Run(ctx)

	// 退出前一定要刷盘：用户可能刚拖出来一大段历史。
	// 也一定要在关网页**之前**刷：页面最后一次看到的应当是完整的一天，而不是刷了一半的文件。
	a.Log.Info("正在收尾：把内存里的数据写进 CSV…")
	if ferr := st.Close(); ferr != nil {
		a.Log.Error("有文件没写成功：%v", ferr)
	} else {
		a.Log.Info("落盘完成")
	}
	a.printSummary(st)
	web.close(a.Log)
	a.Log.Rule(72)

	// 刷完盘才交还 SIGINT：这样收尾期间再来一次 Ctrl+C 仍是「等它写完」，
	// 而不是把写了一半的进程干掉；回到菜单之后再按 Ctrl+C 才是真的退出。
	stop()

	if runErr != nil {
		a.Log.Error("运行出错：%v", runErr)
		pauseIfNeeded(cfg.PauseOnExit)
		return 1
	}
	return 0
}

func (a *App) printConfig(cfg *config.Config) {
	a.Log.Plain("  匹配页面      %s", cfg.MatchURL)
	a.Log.Plain("  只收周期      %ds", cfg.IntervalSeconds)
	if cfg.Symbol != "" {
		a.Log.Plain("  强制品种      %s", cfg.Symbol)
	} else {
		a.Log.Plain("  品种识别      自动（帧里的 symbol_resolved，兜底用页面 URL 的 ?symbol=）")
	}
	a.Log.Plain("  数据目录      %s", cfg.OutPath())
	a.Log.Plain("  原始帧留档    %s", enabled(cfg.SaveRawFrames, cfg.RawPath()))
	a.Log.Plain("  时区          %s（文件名与 CSV 时间列都用它）", time.Local.String())
	if cfg.CDPPort > 0 {
		a.Log.Plain("  CDP 端口      %d（配置指定）", cfg.CDPPort)
	} else {
		a.Log.Plain("  CDP 端口      自动发现（DevToolsActivePort + 9222/9229/9333 探测）")
	}
	a.Log.Rule(72)
}

func enabled(on bool, detail string) string {
	if on {
		return detail
	}
	return "关闭"
}

func (a *App) printSummary(st *store.Store) {
	stats := st.Stats()
	if len(stats) == 0 {
		a.Log.Plain("  本次没有写入任何 K 线（可能是还没拖动过图表）。")
		return
	}
	a.Log.Plain("  本次写入的标的：")
	for _, s := range stats {
		a.Log.Plain("    %-24s %5d 根 / %d 个日文件   推送 %d 次   %s ~ %s",
			s.Folder, s.Bars, s.Days, s.Accepted, s.FirstTime, s.LastTime)
	}
}

// pauseIfNeeded 双击运行时，程序一闪而过会把报错也带走。
// 只在「出错」路径上等回车，正常运行到 Ctrl+C 时不等 —— 那会显得很啰嗦。
func pauseIfNeeded(on bool) {
	if !on {
		return
	}
	waitEnter("\n按回车键关闭窗口…")
}
