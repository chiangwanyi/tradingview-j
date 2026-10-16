//go:build windows

package logx

import "syscall"

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procSetConsoleOutputCP = kernel32.NewProc("SetConsoleOutputCP")
	procSetConsoleCP       = kernel32.NewProc("SetConsoleCP")
	procSetConsoleMode     = kernel32.NewProc("SetConsoleMode")
)

const enableVirtualTerminalProcessing = 0x0004

// init 做两件 Windows 控制台必须做的事：
//
//  1. 代码页切到 UTF-8(65001)。cmd.exe 默认是 GBK(936)，写 UTF-8 中文会变乱码 ——
//     用户双击 exe 看到的正是 cmd 风格的窗口，不做这一步所有提示都没法看。
//  2. 打开虚拟终端处理。老年份的 conhost 默认不解释 ANSI 转义，
//     状态行的原地刷新会退化成乱码。日志器本身已经不依赖 ANSI 了，这一步是双保险，
//     顺便让将来加颜色能直接用。
//
// 没有控制台（输出被重定向）时这些调用会失败，失败不影响运行。
func init() {
	procSetConsoleOutputCP.Call(65001)
	procSetConsoleCP.Call(65001)

	h, err := syscall.GetStdHandle(syscall.STD_OUTPUT_HANDLE)
	if err != nil {
		return
	}
	var mode uint32
	if err := syscall.GetConsoleMode(h, &mode); err != nil {
		return
	}
	// Go 的 syscall 只提供了 GetConsoleMode，设置要走 kernel32
	procSetConsoleMode.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
}
