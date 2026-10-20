package webreport

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"time"

	"tradingview-j/internal/store"
)

// 前端资源内嵌。static/ 下面是 index.html / app.js / style.css / vendor/*。
// 用 embed 而不是「读 exe 同目录的 static 文件夹」：双击运行时用户只拷了 exe，
// 少一个目录就白屏 —— 内嵌让「一个 exe」这个承诺继续成立。
//
//go:embed static
var staticFS embed.FS

var (
	staticDir = mustSub(staticFS, "static")
	indexHTML = mustRead(staticFS, "static/index.html")
)

func mustSub(f fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		// 只在编译期就没内嵌成功时才可能发生，属于开发期错误，直接炸掉比白屏好排查
		panic("webreport: 内嵌资源缺少 " + dir + "：" + err.Error())
	}
	return sub
}

func mustRead(f fs.FS, name string) []byte {
	b, err := fs.ReadFile(f, name)
	if err != nil {
		panic("webreport: " + name + "：" + err.Error())
	}
	return b
}

// Options 是渲染与扫描需要的全部输入。
type Options struct {
	Root     string         // 数据目录（cfg.OutPath()）
	Step     int64          // 一根 K 线多少秒
	Location *time.Location // 空则用本机时区
	Version  string         // 程序版本，显示在页脚
	Log      func(format string, args ...any)
}

// New 组装出 HTTP 路由。
//
// 只暴露两个入口：`/api/analysis` 给数据，其余交给内嵌的静态文件。
// 没有「执行动作」的接口 —— 网页版是纯只读的，
// 一个只读工具不需要、也不该有一个能改数据的 API 面。
func New(opts Options) http.Handler {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	mux := http.NewServeMux()

	mux.HandleFunc("/api/analysis", func(w http.ResponseWriter, r *http.Request) {
		// 走的是 store.Scan：不建目录、不写索引、不改文件。
		start := time.Now()
		res := store.Scan(opts.Root, opts.Location, opts.Step)
		rep := Build(res, opts.Location, opts.Version)
		rep.Meta.GeneratedAt = time.Now().Unix()
		rep.Meta.ScanMs = time.Since(start).Milliseconds()

		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		// 每次刷新都重新扫盘，不缓存：用户按下「重新扫描」就该看到这一刻的磁盘实况
		w.Header().Set("Cache-Control", "no-store")
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false) // 路径里的 & < > 原样输出，别写成 \u0026
		if err := enc.Encode(rep); err != nil && opts.Log != nil {
			opts.Log("[web] 写 /api/analysis 响应失败：%v", err)
		}
	})

	// 静态资源：内嵌的，运行时改不了，所以让浏览器别再重复要（但也别长缓存，
	// 免得以后改了页面用户还在看旧的 —— 这里只给一个协商缓存）
	fileServer := http.FileServerFS(staticDir)
	mux.Handle("/static/", noCache(http.StripPrefix("/static/", fileServer)))

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" && r.URL.Path != "/index.html" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		// 同理：不缓存，免得改完页面刷新还是旧的
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(indexHTML)
	})
	return mux
}

func noCache(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		h.ServeHTTP(w, r)
	})
}

// Server 是跑起来的网页服务。
type Server struct {
	URL   string // 形如 http://127.0.0.1:8765/
	Port  int
	Moved bool // 配置的端口被占用，已改用系统分配的端口（如实报给用户，不静默换）

	httpSrv *http.Server
	ln      net.Listener
}

// Start 起服务。port <= 0 表示随便给一个空闲端口。
//
// 只监听 127.0.0.1，绝不监听 0.0.0.0：这个页面上会显示本地数据目录的绝对路径，
// 局域网里的别人没有任何理由看到它。要远程看就自己开隧道，那是用户的选择。
func Start(opts Options, port int) (*Server, error) {
	ln, moved, err := listen(port)
	if err != nil {
		return nil, err
	}
	addr := ln.Addr().(*net.TCPAddr)
	s := &Server{
		URL:   fmt.Sprintf("http://127.0.0.1:%d/", addr.Port),
		Port:  addr.Port,
		Moved: moved,
	}
	s.httpSrv = &http.Server{
		Handler:           New(opts),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.ln = ln
	go func() {
		// Serve 只在监听出错时返回；此时进程还在跑菜单，不能让这个错误把程序带崩，
		// 但也不能吞掉 —— 记一行日志，用户从菜单回到终端就能看到。
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed && opts.Log != nil {
			opts.Log("[web] 服务退出：%v", err)
		}
	}()
	return s, nil
}

func listen(port int) (net.Listener, bool, error) {
	if port < 0 || port > 65535 {
		port = 0
	}
	if port == 0 {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, false, fmt.Errorf("起不了本地服务：%w", err)
		}
		return ln, false, nil
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err == nil {
		return ln, false, nil
	}
	// 端口被占用（或没权限）不该让整个功能不可用，但也不能闷声换一个 ——
	// 用户是拿这个端口号去浏览器里敲地址的。换端口这件事必须说出来。
	ln, err2 := net.Listen("tcp", "127.0.0.1:0")
	if err2 != nil {
		return nil, false, fmt.Errorf("端口 %d 起不了（%v），换成随机端口也失败：%w", port, err, err2)
	}
	return ln, true, nil
}

// Close 关掉监听。
func (s *Server) Close() error {
	if s.httpSrv == nil {
		return nil
	}
	return s.httpSrv.Close()
}
