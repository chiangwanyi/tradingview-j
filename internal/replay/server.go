package replay

// 复盘服务：一个本地 HTTP 服务，两个入口 —— `/ws` 是全部业务通信，
// 其余路径交给前端构建产物（web/dist）。
//
// 静态资源为什么不 embed：前端是 Vite 工程，`npm run build` 之后才有 dist，
// 而 Go 的 //go:embed 只能嵌本包目录下的文件（不能 `../web/dist`）。与其为了内嵌
// 在 build.cmd 里加一步「先构建前端再复制过来」，不如运行时从 exe 同目录的
// web/dist 读 —— 这个程序本来就是「在自己机器上跑」，dist 就在旁边。
// **但 WebSocket 服务不依赖 dist**：没有 dist 时页面会给出一句明确的提示，
// 开发期用 `npm run dev` 连的是同一个 WS 端口。

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// Options 是起服务需要的全部输入。
type Options struct {
	DataRoot string         // config.OutPath()
	DistDir  string         // config.ReplayDistPath()，通常是 <exe>/web/dist
	Version  string         // 显示在 welcome 里
	Location *time.Location // 空则本机时区
	Log      func(format string, args ...any)
}

// NewHandler 组装路由。测试里可以直接拿它配 httptest.NewServer。
func NewHandler(hub *Hub, distDir string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		if !originAllowed(r) {
			http.Error(w, "只允许本机页面连接", http.StatusForbidden)
			return
		}
		conn, err := upgrade(w, r)
		if err != nil {
			hub.Logf("[replay] 升级失败（%s）：%v", r.RemoteAddr, err)
			return
		}
		hub.Serve(conn)
	})

	// 给命令行 / 反代用的健康检查。没有任何业务含义，也不碰数据。
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})

	mux.Handle("/", staticHandler(distDir))
	return mux
}

// originAllowed 挡住「任意网页连本地服务」。浏览器发 WS 时会带 Origin，
// 本机工具只该被本机页面连；不带 Origin 的（自己写的 Go 客户端/测试）放行。
func originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u := strings.TrimPrefix(strings.TrimPrefix(origin, "http://"), "https://")
	host := u
	if i := strings.LastIndex(u, ":"); i > 0 && !strings.HasPrefix(u, "[") {
		host = u[:i]
	}
	switch host {
	case "127.0.0.1", "localhost", "[::1]", "::1":
		return true
	}
	return false
}

// staticHandler 提供 web/dist，并把未知路径回退到 index.html（SPA 习惯）。
func staticHandler(distDir string) http.Handler {
	fsys := http.FileServer(http.Dir(distDir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		index := filepath.Join(distDir, "index.html")
		if _, err := os.Stat(index); err != nil {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = w.Write([]byte(noDistHTML(distDir)))
			return
		}
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, index)
			return
		}
		// URL 路径要用 path 而不是 filepath：filepath.Clean 在 Windows 上会把
		// "/assets/x.js" 变成 `\assets\x.js`，于是下面那个 HasPrefix 永远不成立 ——
		// 长缓存头静默失效（页面照常能用，所以只能靠测试发现）。
		clean := path.Clean("/" + r.URL.Path)
		local := filepath.Join(distDir, filepath.FromSlash(clean))
		if fi, err := os.Stat(local); err == nil && !fi.IsDir() {
			if strings.HasPrefix(clean, "/assets/") {
				// Vite 产物文件名带内容哈希，可以长缓存
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			fsys.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFile(w, r, index)
	})
}

func noDistHTML(distDir string) string {
	return `<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><title>复盘 · 前端还没构建</title>
<style>body{font:14px/1.7 system-ui,"Microsoft YaHei",sans-serif;max-width:760px;margin:60px auto;padding:0 20px;color:#111}
code{background:#f3f4f6;padding:2px 6px;border-radius:4px}pre{background:#f3f4f6;padding:12px 14px;border-radius:6px;overflow:auto}
h1{font-size:20px}</style></head><body>
<h1>前端还没构建</h1>
<p>WebSocket 服务已经在跑（<code>/ws</code>），但没找到前端产物：<code>` + htmlEscape(distDir) + `</code></p>
<p>两种办法，任选一种：</p>
<ol>
<li><b>开发模式</b>（改代码即时生效）：在 <code>web</code> 目录执行 <code>npm install</code> 和 <code>npm run dev</code>，
然后打开 <code>http://127.0.0.1:5173</code>。dev server 会把 <code>/ws</code> 转发到本服务。</li>
<li><b>构建后由本服务托管</b>：在 <code>web</code> 目录执行 <code>npm install</code> 和 <code>npm run build</code>，
再回到这个地址刷新。</li>
</ol>
<p>数据目录里的标的由菜单 1 抓取产生；这个页面不会修改任何 CSV。</p>
</body></html>`
}

func htmlEscape(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// Server 是跑起来的复盘服务。
type Server struct {
	URL   string
	Port  int
	Moved bool
	Hub   *Hub

	httpSrv *http.Server
	ln      net.Listener
}

// Start 起服务。port <= 0 表示随便给一个空闲端口；端口被占时换一个并在 Moved 里如实说明。
func Start(opts Options, port int) (*Server, error) {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	hub := NewHub(NewDataSource(opts.DataRoot, opts.Location), opts.Version, opts.Log)

	ln, moved, err := listen(port)
	if err != nil {
		return nil, err
	}
	addr := ln.Addr().(*net.TCPAddr)
	s := &Server{
		URL:   fmt.Sprintf("http://127.0.0.1:%d/", addr.Port),
		Port:  addr.Port,
		Moved: moved,
		Hub:   hub,
	}
	s.httpSrv = &http.Server{
		Handler:           NewHandler(hub, opts.DistDir),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.ln = ln
	go func() {
		if err := s.httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed && opts.Log != nil {
			opts.Log("[replay] 服务退出：%v", err)
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
	ln, err2 := net.Listen("tcp", "127.0.0.1:0")
	if err2 != nil {
		return nil, false, fmt.Errorf("端口 %d 起不了（%v），换成随机端口也失败：%w", port, err, err2)
	}
	return ln, true, nil
}

// Close 先停业务（机器人、连接），再关监听。
func (s *Server) Close() error {
	if s.Hub != nil {
		s.Hub.Close()
	}
	if s.httpSrv != nil {
		return s.httpSrv.Close()
	}
	return nil
}

// DistExists 供命令行判断「前端构建产物在不在」，好如实提醒用户。
func DistExists(dir string) bool {
	st, err := os.Stat(filepath.Join(dir, "index.html"))
	return err == nil && !st.IsDir()
}
