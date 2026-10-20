package cli

// 菜单主循环。这里是**唯一**决定「有哪些菜单项、选完怎么回到菜单」的地方；
// 每个菜单项的实现各自在自己的文件里（capture.go / web.go / replay.go）。

import (
	"fmt"
	"strings"
)

// menu 是程序的主循环。只有选 4 才返回，返回的退出码交给 Run。
//
// 注意抓取/起服务失败**不会**退出程序：报错、等一次回车、回菜单，让用户能改完
// 配置接着试。所以这里的返回值实际上只有「正常退出」一种。
func (a *App) menu() int {
	for {
		a.printMenu()
		fmt.Print("  请选择 [1/2/3/4]（直接回车重画菜单）：")
		line, ok := readLine()
		if !ok {
			fmt.Println()
			a.Log.Plain("  输入已结束，退出。")
			return 0
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "1":
			a.runCapture()
			waitEnter("\n  按回车键返回菜单…")
		case "2":
			// runWeb 自己等 Ctrl+C 停下来，回菜单前不用再等一次回车
			a.runWeb()
		case "3":
			// 同菜单 2：Ctrl+C 停服务并回菜单
			a.runReplay()
		case "4", "q", "quit", "exit":
			a.Log.Plain("  已退出。")
			return 0
		case "":
			// 空回车：重画一遍菜单就好
		default:
			a.Log.Warn("这个选项不认识：%q（请输入 1、2、3 或 4）", strings.TrimSpace(line))
		}
	}
}

func (a *App) printMenu() {
	a.Log.Rule(72)
	// 三个标签在显示宽度上都是 20 列（中文一个字占两列），所以描述从同一列开始。
	a.Log.Plain("  1  启动抓取            %s", "等 Chrome + 图表页，把 5 分钟 K 线录成 CSV")
	a.Log.Plain("  2  网页版分析          %s", "只读统计：每个标的每天多少根、缺口在哪")
	a.Log.Plain("  3  复盘                %s", "随机/指定时间点，逐根回放、画标注、模拟下单")
	a.Log.Plain("  4  退出")
	a.Log.Rule(72)
}
