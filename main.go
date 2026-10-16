// tradingview-j —— 把 TradingView 图表页收到的 K 线旁路抓下来，按标的+日期落成 CSV。
//
// 使用方式只有一种：双击（或终端里运行）这个 exe。程序**不接受任何命令行参数**，
// 所有配置都在 exe 同目录的 config.json 里。
//
// 工作流程：
//
//	启动 -> 等 Chrome 的调试端口 -> 等 TradingView 图表页 -> 被动监听 WebSocket 帧
//	     -> 解出 5 分钟 K 线 -> 覆盖写 <数据目录>/<标的>/<yyyy-MM-dd>.csv
//
// 想停止就 Ctrl+C：会先把内存里的当天数据刷盘再退出。
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"syscall"
	"time"

	"tradingview-j/internal/capture"
	"tradingview-j/internal/config"
	"tradingview-j/internal/logx"
	"tradingview-j/internal/store"
)

var version = "1.0.0"

func main() {
	lg := logx.New(os.Stdout)
	baseDir := baseDir()
	exitCode := run(lg, baseDir)
	lg.ClearStatus()
	if exitCode != 0 {
		os.Exit(exitCode)
	}
}

// baseDir 取 exe 所在目录。
//
// 双击运行时当前工作目录可能是任意位置（甚至是被系统改过的），
// 所以配置和数据目录都以 exe 的位置为准 —— 这样「双击」和「在终端里跑」
// 看到的是同一份配置和同一份数据。
func baseDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

func run(lg *logx.Logger, baseDir string) int {
	banner(lg, baseDir)

	cfg, res, err := config.Load(baseDir)
	if err != nil {
		lg.Error("%v", err)
		pauseIfNeeded(true)
		return 1
	}
	if res.Created {
		lg.Info("没找到配置文件，已生成一份默认的：%s", res.Path)
	} else {
		lg.Info("配置：%s", res.Path)
	}
	for _, k := range res.UnknownKeys {
		lg.Warn("配置里有这一项但不认识：%q（多半是拼错了，已忽略）", k)
	}
	for _, n := range res.Notes {
		lg.Warn("%s", n)
	}

	printConfig(lg, cfg)

	st, err := store.Open(store.Options{
		Root:     cfg.OutPath(),
		Header:   cfg.CSVHeader,
		Location: time.Local,
		Log:      func(format string, args ...any) { lg.Info(format, args...) },
	})
	if err != nil {
		lg.Error("%v", err)
		pauseIfNeeded(cfg.PauseOnExit)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sess := capture.New(cfg, st, lg)
	lg.Info("开始运行。要停止：在这个窗口按 Ctrl+C")
	lg.Rule(72)

	err = sess.Run(ctx)

	// 退出前一定要刷盘：用户可能刚拖出来一大段历史
	lg.Info("正在收尾：把内存里的数据写进 CSV…")
	if ferr := st.Close(); ferr != nil {
		lg.Error("有文件没写成功：%v", ferr)
	} else {
		lg.Info("落盘完成")
	}
	printSummary(lg, st)
	lg.Rule(72)

	if err != nil {
		lg.Error("运行出错：%v", err)
		pauseIfNeeded(cfg.PauseOnExit)
		return 1
	}
	return 0
}

func banner(lg *logx.Logger, baseDir string) {
	lg.Rule(72)
	lg.Plain("  tradingview-j  v%s", version)
	lg.Plain("  旁路抓 TradingView 图表页的 WebSocket 行情帧，把 5 分钟 K 线写成 CSV")
	lg.Plain("  目录：%s", baseDir)
	lg.Plain("  运行时：Go %s / %s", runtime.Version(), runtime.GOOS)
	lg.Rule(72)
}

func printConfig(lg *logx.Logger, cfg *config.Config) {
	lg.Plain("  匹配页面      %s", cfg.MatchURL)
	lg.Plain("  只收周期      %ds", cfg.IntervalSeconds)
	if cfg.Symbol != "" {
		lg.Plain("  强制品种      %s", cfg.Symbol)
	} else {
		lg.Plain("  品种识别      自动（帧里的 symbol_resolved，兜底用页面 URL 的 ?symbol=）")
	}
	lg.Plain("  数据目录      %s", cfg.OutPath())
	lg.Plain("  原始帧留档    %s", enabled(cfg.SaveRawFrames, cfg.RawPath()))
	lg.Plain("  时区          %s（文件名与 CSV 时间列都用它）", time.Local.String())
	if cfg.CDPPort > 0 {
		lg.Plain("  CDP 端口      %d（配置指定）", cfg.CDPPort)
	} else {
		lg.Plain("  CDP 端口      自动发现（DevToolsActivePort + 9222/9229/9333 探测）")
	}
	lg.Rule(72)
	lg.Plain("  接入 Chrome 的前置操作（只需做一次，每个浏览器会话要做一次）：")
	lg.Plain("    1. 打开你平时登录着 TradingView 的那个 Chrome")
	lg.Plain("    2. 地址栏输入 chrome://inspect/#remote-debugging")
	lg.Plain("    3. 勾选 “Allow remote debugging for this browser instance”")
	lg.Plain("    4. 保持 Chrome 开着，并打开 TradingView 的图表页")
	lg.Plain("  程序会自己等这些条件满足，不需要重启程序。")
	lg.Rule(72)
}

func enabled(on bool, detail string) string {
	if on {
		return detail
	}
	return "关闭"
}

func printSummary(lg *logx.Logger, st *store.Store) {
	stats := st.Stats()
	if len(stats) == 0 {
		lg.Plain("  本次没有写入任何 K 线（可能是还没拖动过图表）。")
		return
	}
	lg.Plain("  本次写入的标的：")
	for _, s := range stats {
		lg.Plain("    %-24s %5d 根 / %d 个日文件   推送 %d 次   %s ~ %s",
			s.Folder, s.Bars, s.Days, s.Accepted, s.FirstTime, s.LastTime)
	}
}

// pauseIfNeeded 双击运行时，程序一闪而过会把报错也带走。
// 只在「出错」路径上等回车，正常运行到 Ctrl+C 时不等 —— 那会显得很啰嗦。
func pauseIfNeeded(on bool) {
	if !on {
		return
	}
	if !isTerminal() {
		return
	}
	fmt.Print("\n按回车键关闭窗口…")
	var b [1]byte
	os.Stdin.Read(b[:])
}

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}
