package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"tradingview-j/internal/logx"
)

// stdin 是全程唯一的输入源。
//
// 不能「菜单里用一个带缓冲的 reader、按回车继续时又直接读 os.Stdin」：缓冲层
// 可能已经把用户后面敲的字符读进去了，绕开它直接读裸 fd 会把那部分吃掉，
// 表现成「只按了一次回车，菜单却连着跳过了两项」。所以下面的 waitEnter 也走 readLine。
var stdin = bufio.NewReader(os.Stdin)

// readLine 读一行输入（去掉行尾换行）。
// 第二个返回值为 false 表示输入流已经结束（管道读完 / 控制台被关掉）——
// 这时调用方应当退出，而不是继续转圈问，否则会空转出一个刷屏的死循环。
func readLine() (string, bool) {
	line, err := stdin.ReadString('\n')
	line = strings.TrimRight(line, "\r\n")
	if err != nil {
		if line == "" {
			return "", false
		}
		return line, true // 最后一行没有换行符，仍算一次有效输入
	}
	return line, true
}

func isTerminal() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// waitEnter 等一次回车。stdin 不是终端时（被重定向、被当后台任务跑）直接返回 ——
// 否则会卡在等输入上，那种「没反应」比「看不见输出」更难排查。
func waitEnter(prompt string) {
	if !isTerminal() {
		return
	}
	fmt.Print(prompt)
	readLine()
}

// menu 是程序的主循环。只有选 4 才返回，返回的退出码交给 main。
func menu(lg *logx.Logger, baseDir string) int {
	for {
		printMenu(lg)
		fmt.Print("  请选择 [1/2/3/4]（直接回车重画菜单）：")
		line, ok := readLine()
		if !ok {
			fmt.Println()
			lg.Plain("  输入已结束，退出。")
			return 0
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "1":
			runCapture(lg, baseDir)
			waitEnter("\n  按回车键返回菜单…")
		case "2":
			runAnalyze(lg, baseDir)
			waitEnter("\n  按回车键返回菜单…")
		case "3":
			// runWeb 自己等 Ctrl+C 停下来，回菜单前不用再等一次回车
			runWeb(lg, baseDir)
		case "4", "q", "quit", "exit":
			lg.Plain("  已退出。")
			return 0
		case "":
			// 空回车：重画一遍菜单就好
		default:
			lg.Warn("这个选项不认识：%q（请输入 1、2、3 或 4）", strings.TrimSpace(line))
		}
	}
}

func printMenu(lg *logx.Logger) {
	lg.Rule(72)
	// 三个标签在显示宽度上都是 20 列（中文一个字占两列），所以描述从同一列开始。
	lg.Plain("  1  启动抓取            %s", "等 Chrome + 图表页，把 5 分钟 K 线录成 CSV")
	lg.Plain("  2  分析已抓取的数据    %s", "只读统计：每个标的每天多少根、缺口在哪")
	lg.Plain("  3  网页版分析          %s", "同一份统计结果放进浏览器，可排序筛选")
	lg.Plain("  4  退出")
	lg.Rule(72)
}
