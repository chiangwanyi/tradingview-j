package cli

// 菜单里的「3 复盘（网页）」。
//
// 拉起 internal/replay 的本地服务：同一个端口既是 HTTP（前端页面），也是 WebSocket（全部业务）。
// 和菜单 2 的区别要说清楚：菜单 2 是**只读**的统计报表，菜单 3 是**真的会改状态**的复盘 ——
// 标注、挂单、成交都记在服务端内存里，不写回 CSV，也不碰数据目录。

import (
	"time"

	"tradingview-j/internal/replay"
)

func (a *App) runReplay() int {
	cfg, ok := a.loadConfig()
	if !ok {
		return 1
	}
	root := cfg.OutPath()
	dist := cfg.ReplayDistPath()
	state := cfg.ReplayStatePath()
	recs := cfg.RecordingPath()

	a.Log.Rule(72)
	a.Log.Plain("  复盘 · 数据目录 %s", root)
	a.Log.Plain("  前端产物 %s", dist)
	a.Log.Plain("  实例存档 %s", state)
	a.Log.Plain("  录像目录 %s（一条录像一个文件夹：recording.json + audio-N.wav）", recs)
	a.Log.Plain("  一根 K 线 %d 秒 · 时区 %s（和 CSV 里的时间列一致）", cfg.IntervalSeconds, time.Local)
	a.Log.Plain("  只读数据目录：标注、挂单、成交只存在服务端（外加一份实例存档），不写回 CSV")
	a.Log.Plain("  多人连接时只有一人能操作，其余人观看；操作可随时交还或接管")
	a.Log.Rule(72)

	if !replay.DistExists(dist) {
		a.Log.Warn("没找到前端产物（%s 里没有 index.html）", dist)
		a.Log.Plain("  开发模式：在 web 目录执行 npm install 和 npm run dev，然后开 http://127.0.0.1:5173")
		a.Log.Plain("  或用本服务托管：在 web 目录执行 npm run build，再刷新下面的地址")
	} else {
		a.Log.Plain("  提示：改过前端代码要重新 npm run build（静态文件是构建产物，不是源码）")
	}

	srv, err := replay.Start(replay.Options{
		DataRoot:     root,
		DistDir:      dist,
		StateDir:     state,
		RecordingDir: recs,
		Version:      a.Version,
		Location:     time.Local,
		Log:          func(format string, args ...any) { a.Log.Info(format, args...) },
	}, cfg.ReplayPort)
	if err != nil {
		a.Log.Error("%v", err)
		return 1
	}
	if srv.Moved {
		a.Log.Warn("端口 %d 起不来（多半被占着），已改用 %d —— 要固定就改 config.json 的 replayPort", cfg.ReplayPort, srv.Port)
		a.Log.Warn("开发模式的 /ws 代理指向 %d，端口变了要同步改 web/vite.config.ts", cfg.ReplayPort)
	}
	a.Log.Plain("  页面      %s", srv.URL)
	a.Log.Plain("  WebSocket ws://127.0.0.1:%d/ws", srv.Port)
	a.Log.Plain("  已尝试用默认浏览器打开；没打开就手动把上面的地址粘进去。")
	a.Log.Rule(72)

	openBrowser(srv.URL)

	a.Log.Info("复盘服务运行中。停止并回到菜单：在这个窗口按 Ctrl+C")
	waitStop()

	a.Log.Info("正在停止复盘服务…")
	if err := srv.Close(); err != nil {
		a.Log.Warn("关闭服务时出错：%v", err)
	}
	a.Log.Plain("  复盘服务已停止。实例、标注、挂单、成交都写进了 %s", state)
	a.Log.Plain("  录像（含录音）在 %s", recs)
	a.Log.Plain("  下次选 3 进来还在列表里（操作者不保存，谁进来谁重新拿操作权）")
	return 0
}
