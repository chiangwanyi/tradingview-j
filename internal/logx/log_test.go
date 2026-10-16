package logx

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func ttyLogger() (*Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &Logger{out: buf, tty: true, now: func() time.Time { return time.Unix(0, 0) }}, buf
}

func pipeLogger() (*Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	return &Logger{out: buf, tty: false, now: func() time.Time { return time.Unix(0, 0) }}, buf
}

func TestDisplayWidthCountsWideRunesAsTwo(t *testing.T) {
	cases := map[string]int{
		"abc":    3,
		"中文":     4,
		"中a":     3,
		"":       0,
		"[状态] x": 8, // 6 个半角 + 2 个中日韩方括号 = 6 + 4
	}
	for in, want := range cases {
		if got := displayWidth(in); got != want {
			t.Fatalf("displayWidth(%q) = %d, 期望 %d", in, got, want)
		}
	}
}

func TestStatusOverwritesInPlaceWithoutAnsi(t *testing.T) {
	lg, buf := ttyLogger()
	lg.Status("AAAAAAAAAAAA")
	lg.Status("BB")

	out := buf.String()
	if strings.Contains(out, "\033") {
		t.Fatalf("不应当使用 ANSI 转义（cmd.exe 会显示成乱码）: %q", out)
	}
	// 用 \r 回到行首，并把上次剩下的 10 列用空格盖掉
	if !strings.HasPrefix(out, "\rAAAAAAAAAAAA") {
		t.Fatalf("第一次没有从行首开始: %q", out)
	}
	if !strings.HasSuffix(out, "\rBB"+strings.Repeat(" ", 10)) {
		t.Fatalf("第二次没有补空格擦除: %q", out)
	}
}

func TestStatusPadsByDisplayWidthNotBytes(t *testing.T) {
	lg, buf := ttyLogger()
	lg.Status("中文中文") // 显示宽度 8
	lg.Status("x")    // 显示宽度 1 → 需要补 7 个空格
	if got := buf.String(); !strings.HasSuffix(got, "\rx"+strings.Repeat(" ", 7)) {
		t.Fatalf("按字节数补空格会擦不干净: %q", got)
	}
}

func TestStatusDoesNotEmitCarriageReturnWhenRedirected(t *testing.T) {
	lg, buf := pipeLogger()
	lg.Status("进度 1")
	lg.Info("一条日志")

	out := buf.String()
	if strings.Contains(out, "\r") {
		t.Fatalf("重定向到文件时不该出现回车符: %q", out)
	}
	if !strings.Contains(out, "进度 1\n") || !strings.Contains(out, "一条日志\n") {
		t.Fatalf("输出不完整: %q", out)
	}
}

func TestLogLineClearsStatusFirst(t *testing.T) {
	lg, buf := ttyLogger()
	lg.Status("XXXXXXXXXX")
	lg.Info("发生了别的事")

	out := buf.String()
	// 状态行之后必须先把这一行擦掉，再另起一行打印日志；
	// 否则日志会接在半截状态行后面。
	if !strings.Contains(out, "\r"+strings.Repeat(" ", 10)+"\r") {
		t.Fatalf("日志前没有清行: %q", out)
	}
	if !strings.HasSuffix(out, "发生了别的事\n") {
		t.Fatalf("日志内容不对: %q", out)
	}
}

func TestClearStatusLeavesCleanLine(t *testing.T) {
	lg, buf := ttyLogger()
	lg.Status("正在等待 Chrome")
	before := buf.Len()
	lg.ClearStatus()
	tail := buf.String()[before:]
	if tail != "\r"+strings.Repeat(" ", displayWidth("正在等待 Chrome"))+"\r" {
		t.Fatalf("收尾擦除不正确: %q", tail)
	}
	// 再清一次应当是空操作
	before = buf.Len()
	lg.ClearStatus()
	if buf.Len() != before {
		t.Fatal("重复 ClearStatus 不该再输出")
	}
}
