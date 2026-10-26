package cli

// 输入这一层有一条承重约定：**全程只有一个 stdin reader**，就是下面这个 stdin。
//
// 不能「菜单里用一个带缓冲的 reader、按回车继续时又直接读 os.Stdin」：缓冲层
// 可能已经把用户后面敲的字符读进去了，绕开它直接读裸 fd 会把那部分吃掉，
// 表现成「只按了一次回车，菜单却连着跳过了两项」。所以 waitEnter 也走 readLine。

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

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

// waitStop 阻塞到用户按 Ctrl+C（复盘服务用它，抓取那边用的是 signal.NotifyContext）。
//
// 这里刻意**不用回车**来停：要在等回车的同时响应 Ctrl+C，就得在后台起一个读 stdin 的
// goroutine，而 Ctrl+C 先到的话那个 goroutine 会一直挂在 stdin 上 —— 菜单读的是同一份
// 缓冲 reader（见上面那条约定的注释），于是它会把用户回到菜单后敲的第一个字符吃掉，
// 表现成「选了没反应」。
func waitStop() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	<-sig
}
