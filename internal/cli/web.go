package cli

// 「1 启动抓取」里顺带起的网页版分析。
//
// 这一层只做编排：起服务、报地址、开浏览器、抓取结束时报停。
// 数据怎么算全在 internal/webreport（再往下是 store.Scan），这里一行业务逻辑都没有。
//
// 为什么并进菜单 1 而不是留成独立的菜单项：抓取是常驻的，用户真正想知道的是
// 「现在抓到多少、缺在哪天」。让页面和抓取同生共死，就不存在「先开哪个」
// 「页面停在哪份旧数据上」这类问题 —— 而且页面里的数字靠磁盘指纹自动跟着刷新，
// 不需要任何跨进程的推数据通道（见 webreport.snapshotter 与 store.Scanner）。
//
// 网页版**依旧是只读的**：它不建目录、不写索引、不改文件，也没有任何能改数据的接口。

import (
	"os/exec"
	"runtime"
	"time"

	"tradingview-j/internal/config"
	"tradingview-j/internal/logx"
	"tradingview-j/internal/webreport"
)

// analysisServer 是「跟着抓取一起跑的那个网页」。nil 表示没起来 ——
// 网页起不来不影响抓取，抓取是主功能，页面只是观察窗。
type analysisServer struct {
	srv *webreport.Server
}

// startAnalysis 起网页服务并打开浏览器。调用方拿返回值收尾。
func (a *App) startAnalysis(cfg *config.Config) *analysisServer {
	root := cfg.OutPath()

	a.Log.Plain("  网页版分析 · %s（和抓取一起起，Ctrl+C 时一起停）", root)
	a.Log.Plain("  一根 K 线 %d 秒 · 时区 %s · 统计口径在 store.Scan 一处", cfg.IntervalSeconds, time.Local)
	a.Log.Plain("  只读：页面上的任何操作都不会改动数据目录里的文件")

	srv, err := webreport.Start(webreport.Options{
		Root:     root,
		Step:     cfg.IntervalSeconds,
		Location: time.Local,
		Version:  a.Version,
		Log:      func(format string, args ...any) { a.Log.Info(format, args...) },
	}, cfg.WebPort)
	if err != nil {
		a.Log.Warn("网页版分析没起来：%v", err)
		a.Log.Plain("  抓取照常进行，只是没有那个实时页面。")
		a.Log.Rule(72)
		return nil
	}
	if srv.Moved {
		// 换了端口必须说出来：用户是拿这个端口号去浏览器里敲地址的
		a.Log.Warn("端口 %d 起不来（多半被别的程序占着），已改用 %d —— 要固定就用别的端口改 config.json 的 webPort", cfg.WebPort, srv.Port)
	}
	a.Log.Plain("  地址      %s", srv.URL)
	a.Log.Plain("  已尝试用默认浏览器打开；没打开就手动把上面的地址粘进去。")
	a.Log.Plain("  页面每 %d 秒自动同步一次，抓到的 K 线会自己出现在日历上（不用刷新）。", webreport.DefaultPollMs/1000)
	a.Log.Rule(72)

	openBrowser(srv.URL)
	return &analysisServer{srv: srv}
}

// close 停掉网页服务。抓取停下来、数据刷完盘之后才调 ——
// 先关服务再刷盘的话，页面最后一次能看到的就是残缺的一份。
func (s *analysisServer) close(lg *logx.Logger) {
	if s == nil {
		return
	}
	lg.Info("正在停止网页服务…")
	if err := s.srv.Close(); err != nil {
		lg.Warn("关闭网页服务时出错：%v", err)
	}
	lg.Plain("  网页服务已停止（那个页面从现在起读不到数据）。")
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
