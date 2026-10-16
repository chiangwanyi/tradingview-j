package cli

// 菜单里的「3 网页版分析」。
//
// 和「2 分析已抓取的数据」是同一件事的两个界面：数据都来自 store.Scan 那一次只读扫描，
// 区别只在于一个往终端排版、一个往浏览器送 JSON。所以这里的代码很薄 ——
// 起服务、开浏览器、等 Ctrl+C、关服务，一行业务逻辑都没有。
//
// 网页版**同样是只读的**：它不建目录、不写索引、不改文件，也没有任何能改数据的接口。

import (
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"tradingview-j/internal/webreport"
)

func (a *App) runWeb() int {
	cfg, ok := a.loadConfig()
	if !ok {
		return 1
	}
	root := cfg.OutPath()

	a.Log.Rule(72)
	a.Log.Plain("  网页版分析 · %s", root)
	a.Log.Plain("  一根 K 线 %d 秒 · 时区 %s · 与菜单 2 同一份统计代码", cfg.IntervalSeconds, time.Local)
	a.Log.Plain("  只读：页面上的任何操作都不会改动数据目录里的文件")
	a.Log.Rule(72)

	srv, err := webreport.Start(webreport.Options{
		Root:     root,
		Step:     cfg.IntervalSeconds,
		Location: time.Local,
		Version:  a.Version,
		Log:      func(format string, args ...any) { a.Log.Info(format, args...) },
	}, cfg.WebPort)
	if err != nil {
		a.Log.Error("%v", err)
		return 1
	}
	if srv.Moved {
		// 换了端口必须说出来：用户是拿这个端口号去浏览器里敲地址的
		a.Log.Warn("端口 %d 起不来（多半被别的程序占着），已改用 %d —— 要固定就用别的端口改 config.json 的 webPort", cfg.WebPort, srv.Port)
	}
	a.Log.Plain("  地址      %s", srv.URL)
	a.Log.Plain("  已尝试用默认浏览器打开；没打开就手动把上面的地址粘进去。")
	a.Log.Rule(72)

	openBrowser(srv.URL)

	a.Log.Info("服务运行中。停止并回到菜单：在这个窗口按 Ctrl+C")
	waitStop()

	a.Log.Info("正在停止网页服务…")
	if err := srv.Close(); err != nil {
		a.Log.Warn("关闭服务时出错：%v", err)
	}
	a.Log.Plain("  网页服务已停止。")
	return 0
}

// waitStop 阻塞到用户按 Ctrl+C。
//
// 这里刻意**不用回车**来停：要在等回车的同时响应 Ctrl+C，就得在后台起一个读 stdin 的
// goroutine，而 Ctrl+C 先到的话那个 goroutine 会一直挂在 stdin 上 —— 菜单读的是同一份
// 缓冲 reader（见 prompt.go），于是它会把用户回到菜单后敲的第一个字符吃掉，
// 表现成「选了没反应」。抓取那条路径也是 Ctrl+C 停，两处行为一致，不用记两套。
func waitStop() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)
	<-sig
}

// openBrowser 用系统默认浏览器打开地址。
//
// 打不开不算错误：地址已经打印在上面了，用户手点一下就行 ——
// 为了「自动开浏览器失败」把整个功能判成失败，是本末倒置。
func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// 第一个空参数是 start 命令的「窗口标题」占位：少了它，带引号的 URL
		// 会被 start 当成标题而不是地址。
		cmd = exec.Command("cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
