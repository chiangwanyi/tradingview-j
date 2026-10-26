package cli

// 菜单主循环。这里是**唯一**决定「有哪些菜单项、选完怎么回到菜单」的地方；
// 每个菜单项的实现各自在自己的文件里（capture.go / replay.go）。
//
// 菜单只有三项。原来的「1 启动抓取」「2 网页版分析」已经合成一项：
// 抓取一起来，网页版分析就在 127.0.0.1 上跟着起来了（见 web.go）——
// 对一个常驻的抓取进程来说，「看现在抓到多少」不是另一个功能，而是它的一部分。

import (
	"fmt"
	"strings"
)

// menu 是程序的主循环。只有选 3 才返回，返回的退出码交给 Run。
//
// 注意抓取/复盘失败**不会**退出程序：报错、回菜单，让用户能改完配置接着试。
// 所以这里的返回值实际上只有「正常退出」一种。
func (a *App) menu() int {
	for {
		a.printMenu()
		fmt.Print("  请选择 [1/2/3]（直接回车重画菜单）：")
		line, ok := readLine()
		if !ok {
			fmt.Println()
			a.Log.Plain("  输入已结束，退出。")
			return 0
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "1":
			// runCapture 自己等 Ctrl+C：停下来会先刷盘、再关掉随行的网页服务，然后回菜单
			a.runCapture()
		case "2":
			// 同菜单 1：Ctrl+C 停服务并回菜单
			a.runReplay()
		case "3", "q", "quit", "exit":
			a.Log.Plain("  已退出。")
			return 0
		case "":
			// 空回车：重画一遍菜单就好
		default:
			a.Log.Warn("这个选项不认识：%q（请输入 1、2 或 3）", strings.TrimSpace(line))
		}
	}
}

func (a *App) printMenu() {
	a.Log.Rule(72)
	// 三个标签在显示宽度上都是 6 列（中文一个字占两列），所以描述从同一列开始。
	a.Log.Plain("  1  启动抓取  %s", "等 Chrome + 图表页，把 5 分钟 K 线录成 CSV，同时打开实时分析页")
	a.Log.Plain("  2  复盘      %s", "随机/指定时间点，逐根回放、画标注、模拟下单、录制回放")
	a.Log.Plain("  3  退出")
	a.Log.Rule(72)
}
