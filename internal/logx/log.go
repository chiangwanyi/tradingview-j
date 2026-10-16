// Package logx 是给命令行程序用的最小日志器。
//
// 两件事值得说明：
//
//  1. **状态行原地刷新。** 程序是常驻的，每隔十几秒要报一次进度。如果每次都换行打印，
//     终端会被刷屏淹没，出问题时真正要紧的那几行反而被顶掉。所以进度用 \r 回到行首重画，
//     而日志行打印前先把状态行擦掉 —— 否则日志会接在半截状态行后面，读起来一团糟。
//
//  2. **擦除不靠 ANSI 转义。** cmd.exe（conhost）默认不解释 `ESC[K` 这类序列，
//     直接输出会变成一行 "←[K" 的乱码。所以这里改用「补空格盖掉上一次的内容」，
//     并且按**显示宽度**计算（中文一个字占两列，按字节数补会越擦越乱）。
//     输出被重定向到文件时则退化成每次换行打印，不往文件里写回车。
package logx

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"
)

type Logger struct {
	mu          sync.Mutex
	out         io.Writer
	tty         bool
	statusShown bool
	statusWidth int // 上一次状态行的显示宽度，用来决定补多少空格
	now         func() time.Time
}

func New(out *os.File) *Logger {
	return &Logger{out: out, tty: isTerminal(out), now: time.Now}
}

// NewTo 面向任意 Writer 的构造（测试用）：不做原地刷新，
// 因为测试捕获的是字节流，\r 覆盖没有意义、只会干扰断言。
func NewTo(out io.Writer) *Logger {
	return &Logger{out: out, tty: false, now: time.Now}
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

// Plain 打印一行不带时间戳/级别的内容（横幅、分隔线这类）
func (l *Logger) Plain(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clearStatusLocked()
	fmt.Fprintf(l.out, format+"\n", args...)
}

func (l *Logger) Info(format string, args ...any)  { l.line("信息", format, args...) }
func (l *Logger) Warn(format string, args ...any)  { l.line("注意", format, args...) }
func (l *Logger) Error(format string, args ...any) { l.line("错误", format, args...) }

func (l *Logger) line(level, format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clearStatusLocked()
	fmt.Fprintf(l.out, "%s [%s] %s\n", l.now().Format("15:04:05"), level, fmt.Sprintf(format, args...))
}

// Status 原地刷新一行进度
func (l *Logger) Status(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	text := fmt.Sprintf(format, args...)
	if !l.tty {
		// 重定向场景：老老实实换行，别用回车符污染文件
		fmt.Fprintln(l.out, text)
		return
	}
	w := displayWidth(text)
	pad := 0
	if w < l.statusWidth {
		pad = l.statusWidth - w
	}
	fmt.Fprint(l.out, "\r"+text+strings.Repeat(" ", pad))
	l.statusShown = true
	l.statusWidth = w
}

// ClearStatus 在退出前把残留的状态行擦掉，否则 shell 提示符会接在半截内容后面
func (l *Logger) ClearStatus() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clearStatusLocked()
}

func (l *Logger) clearStatusLocked() {
	if !l.statusShown {
		return
	}
	fmt.Fprint(l.out, "\r"+strings.Repeat(" ", l.statusWidth)+"\r")
	l.statusShown = false
	l.statusWidth = 0
}

// Rule 打一条分隔线（横幅用）
func (l *Logger) Rule(n int) {
	l.Plain("%s", strings.Repeat("─", n))
}

// displayWidth 估算一行文本占几个终端列。
// 不需要严格按 Unicode 规范实现：只要做到「中日韩全角算 2、组合字符算 0」，
// 补空格擦除就不会擦不干净或擦过头。
func displayWidth(s string) int {
	w := 0
	for _, r := range s {
		switch {
		case r == 0:
			continue
		case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r):
			continue
		case isWide(r):
			w += 2
		default:
			w++
		}
	}
	return w
}

func isWide(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // 谚文字母
		r >= 0x2E80 && r <= 0x303E, // 中日韩部首、假名标点
		r >= 0x3041 && r <= 0x33FF,
		r >= 0x3400 && r <= 0x4DBF,
		r >= 0x4E00 && r <= 0x9FFF, // 汉字
		r >= 0xA000 && r <= 0xA4CF,
		r >= 0xAC00 && r <= 0xD7A3, // 谚文音节
		r >= 0xF900 && r <= 0xFAFF,
		r >= 0xFE30 && r <= 0xFE6F,
		r >= 0xFF00 && r <= 0xFF60, // 全角
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}
