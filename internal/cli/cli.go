// Package cli 是程序的外壳：进程启动、横幅、菜单主循环，以及各菜单项的编排。
//
// 它只干「和用户打交道」的事：读配置、决定什么时候等输入、把统计结果交给对应的
// 表现层、决定退出码。业务实现全在别的包 —— 抓取在 internal/capture，
// 网页报表在 internal/webreport，复盘在 internal/replay。
//
// 菜单只有三项：1 启动抓取（内含网页版分析）、2 复盘、3 退出。
//
// 之所以从 package main 里拆出来：main 只该负责「启动」这一件事。
package cli

import (
	"os"
	"path/filepath"
	"runtime"

	"tradingview-j/internal/config"
	"tradingview-j/internal/logx"
)

// App 承载一次运行的上下文。抽成结构体是为了不再把 (日志器, 基准目录) 这对参数
// 沿着每个函数一路传下去。
type App struct {
	Version string       // 构建期注入的版本号（见根目录 main.go）
	Dir     string       // 基准目录 = exe 所在目录
	Log     *logx.Logger // 终端输出
}

// Run 是程序的全部行为：打横幅 → 循环菜单 → 返回退出码。
// 唯一入参是构建期注入的版本号，由根目录的 main.go 传进来。
func Run(version string) int {
	app := &App{
		Version: version,
		Dir:     exeDir(),
		Log:     logx.New(os.Stdout),
	}
	banner(app)
	code := app.menu()
	app.Log.ClearStatus()
	return code
}

// exeDir 取 exe 所在目录。
//
// 双击运行时当前工作目录可能是任意位置（甚至是被系统改过的），
// 所以配置和数据目录都以 exe 的位置为准 —— 这样「双击」和「在终端里跑」
// 看到的是同一份配置和同一份数据。
func exeDir() string {
	exe, err := os.Executable()
	if err != nil {
		wd, _ := os.Getwd()
		return wd
	}
	return filepath.Dir(exe)
}

// loadConfig 每次进菜单分支都重新读一遍 config.json。
// 读一次缓着更省事，但用户改完配置却要重开程序才能生效，是很难解释的行为。
func (a *App) loadConfig() (*config.Config, bool) {
	cfg, res, err := config.Load(a.Dir)
	if err != nil {
		a.Log.Error("%v", err)
		return nil, false
	}
	if res.Created {
		a.Log.Info("没找到配置文件，已生成一份默认的：%s", res.Path)
	} else {
		a.Log.Info("配置：%s", res.Path)
	}
	for _, k := range res.UnknownKeys {
		a.Log.Warn("配置里有这一项但不认识：%q（多半是拼错了，已忽略）", k)
	}
	for _, n := range res.Notes {
		a.Log.Warn("%s", n)
	}
	return cfg, true
}

func banner(a *App) {
	a.Log.Rule(72)
	a.Log.Plain("  tradingview-j  v%s", a.Version)
	a.Log.Plain("  旁路抓 TradingView 图表页的 WebSocket 行情帧，把 5 分钟 K 线写成 CSV")
	a.Log.Plain("  目录：%s", a.Dir)
	a.Log.Plain("  运行时：Go %s / %s", runtime.Version(), runtime.GOOS)
	a.Log.Rule(72)
	a.Log.Plain("  接入 Chrome 的前置操作（每个浏览器会话做一次，抓取前先做掉）：")
	a.Log.Plain("    1. 打开你平时登录着 TradingView 的那个 Chrome")
	a.Log.Plain("    2. 地址栏输入 chrome://inspect/#remote-debugging")
	a.Log.Plain("    3. 勾选 “Allow remote debugging for this browser instance”")
	a.Log.Plain("    4. 打开 TradingView 的图表页，周期切到 5 分钟")
	a.Log.Plain("  前 4 步没做也没关系：抓取会一直等，条件一满足就自动接上，不用重启程序。")
	a.Log.Rule(72)
}
