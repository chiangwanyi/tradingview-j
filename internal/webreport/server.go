package webreport

import (
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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
	// PollMs 是页面的自动同步间隔（毫秒）。0 用默认值 DefaultPollMs。
	//
	// 它必须由服务端告诉页面，而不是写死在前端：这个数字取决于「一次重扫有多贵」，
	// 而那是服务端才知道的事（增量扫描之后一次重扫只剩几毫秒）。
	PollMs int
	Log    func(format string, args ...any)
}

// DefaultPollMs 是页面自动同步的默认间隔。
//
// 为什么是 2 秒：抓取侧每 flushSeconds（默认 1 秒）把内存里的一天重写一遍，
// 页面这边每 2 秒问一次磁盘，于是「图表上拖出来的那根」最迟两秒多就会出现在日历上。
// 再快没有意义 —— 一根 5 分钟 K 线本来就不会每秒多一根。
const DefaultPollMs = 2000

// payload 是一次「可以原样发出去」的响应。
//
// 之所以把编码好的字节留起来而不是每次重新 Marshal：报告本身有 3300 个日文件、
// 0.85MB JSON；**重复刷新**时这两件事都不该再做一遍 —— 见 snapshotter 的注释。
type payload struct {
	body []byte
	gz   []byte
	// cached 只用于日志与自查：true 表示这次没扫盘，发的是上一次的结果。
	cached bool
}

// snapshotter 把「扫一遍数据目录」这件事缓存起来，缓存键是**磁盘指纹**。
//
// 这里的原则没变：页面上看到的永远是磁盘这一刻的样子 —— 只是不再为此
// 每次都把 44MB / 3300 份 CSV 重新读一遍。指纹由每个日文件的名字 + 大小 +
// 修改时刻拼成（只读元数据，实测 ~30ms），任何一个文件被改写过，指纹就变，
// 下一次请求立刻重扫。
//
// 缓存失效的判据是「磁盘变了没有」，不是「过了多少秒」：
// 定时过期那种做法会在这里说谎 —— 用户刚抓完数据，刷新页面却还是十分钟前的快照。
// 也正因为这条判据是「精确」的，抓取过程中页面才能每两秒问一次而不出错：
// 变了就重扫（增量只看变过的那几份，见 store.Scanner），没变就发缓存。
type snapshotter struct {
	opts Options
	// scan 带着上一轮的记忆，所以「重扫」只解析变过的日文件。
	// 它必须在两次请求之间活着 —— 每次请求新建一个就等于每次都是全量扫描。
	scan *store.Scanner

	// mu 同时兼两件事：保护下面几个字段，以及「同一时刻只让一个人扫盘」。
	// 并发按两次 F5 不该跑两遍扫描 —— 后来的那个等前面扫完，
	// 拿到的是同一份结果（指纹一样就是同一份），代价只是等一次。
	mu   sync.Mutex
	key  string
	body []byte
	gz   []byte
	gen  int64
	ms   int64
	// scans 只给测试看：数一数到底真扫了几遍盘。
	// 「缓存命中」这件事从响应内容上是看不出来的（本来就该一模一样），
	// 所以只能在这里留一个计数器，否则测试只能靠时间戳去猜。
	scans int
	// parsed 同样只给测试看：累计真正解析过的日文件数。
	// 「增量扫描」的效果也只能这样断言 —— 结果本身必须和全量扫描一模一样。
	parsed int
}

// New 组装出 HTTP 路由。
//
// 只暴露两个入口：`/api/analysis` 给数据，其余交给内嵌的静态文件。
// 没有「执行动作」的接口 —— 网页版是纯只读的，
// 一个只读工具不需要、也不该有一个能改数据的 API 面。
func New(opts Options) http.Handler {
	return newSnapshotter(opts).handler()
}

func newSnapshotter(opts Options) *snapshotter {
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.PollMs <= 0 {
		opts.PollMs = DefaultPollMs
	}
	return &snapshotter{opts: opts, scan: store.NewScanner()}
}

func (h *snapshotter) handler() http.Handler {
	mux := http.NewServeMux()

	// 走的是 store.Scan：不建目录、不写索引、不改文件。
	mux.HandleFunc("/api/analysis", h.handle)

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

func (h *snapshotter) handle(w http.ResponseWriter, r *http.Request) {
	// refresh=1 是「我现在就要看磁盘实况」：跳过指纹、无条件重扫。
	// 页面上的「重新扫描」按钮走这条路，普通刷新（F5）走缓存。
	force := r.URL.Query().Get("refresh") != ""
	snap := h.snapshot(force)

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Vary", "Accept-Encoding")
	if len(snap.gz) > 0 && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		// 0.85MB -> 约 60KB。回环上省下的时间不多，但这个页面本来就可能是
		// 在别的机器上通过隧道看的，压缩是白送的那一份。
		w.Header().Set("Content-Encoding", "gzip")
		w.Header().Set("Content-Length", strconv.Itoa(len(snap.gz)))
		if _, err := w.Write(snap.gz); err != nil && h.opts.Log != nil {
			h.opts.Log("[web] 写 /api/analysis 响应失败：%v", err)
		}
		return
	}
	if _, err := w.Write(snap.body); err != nil && h.opts.Log != nil {
		h.opts.Log("[web] 写 /api/analysis 响应失败：%v", err)
	}
}

// prewarm 在服务刚起来时先扫一遍，把结果放进缓存。
//
// 用户点「网页版分析」之后，浏览器还要加载几个脚本才发得出第一个请求；
// 这段时间白白空着太可惜 —— 扫描提前跑完，首屏就只剩传输和渲染。
// 失败了也无所谓：下一次真实请求会重新扫，这里只是抢跑。
func (h *snapshotter) prewarm() {
	snap := h.snapshot(false)
	if h.opts.Log != nil && !snap.cached {
		h.opts.Log("[web] 已预先扫描数据目录（%d 毫秒），首屏直接用这份快照", h.ms)
	}
}

func (h *snapshotter) snapshot(force bool) payload {
	h.mu.Lock()
	defer h.mu.Unlock()

	key := fingerprint(h.opts.Root)
	if !force && key != "" && key == h.key && h.body != nil {
		return payload{body: h.body, gz: h.gz, cached: true}
	}

	start := time.Now()
	res := h.scan.Scan(h.opts.Root, h.opts.Location, h.opts.Step)
	h.scans++
	h.parsed = h.scan.ParsedFiles()
	rep := Build(res, h.opts.Location, h.opts.Version)
	// GeneratedAt 是给人看的时间（秒级）；Rev 是给页面判断「这份快照是不是新的」用的
	// 修订号（毫秒级）。抓取过程中一秒内完全可能重扫两次，拿秒级时间戳当判据
	// 会让页面把一次真实的变化当成「没变」而跳过重绘。
	rep.Meta.GeneratedAt = start.Unix()
	rep.Meta.Rev = time.Now().UnixMilli()
	rep.Meta.ScanMs = time.Since(start).Milliseconds()
	// 轮询间隔由服务端给：页面据此决定多久问一次，以及「实时」这个词是不是成立。
	rep.Meta.PollMs = h.opts.PollMs

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false) // 路径里的 & < > 原样输出，别写成 \u0026
	if err := enc.Encode(rep); err != nil {
		// 报告是纯结构体，编码失败只可能是内存/写入问题；照实记下来，别静默发半截
		if h.opts.Log != nil {
			h.opts.Log("[web] 编码 /api/analysis 失败：%v", err)
		}
	}
	body := buf.Bytes()
	gz := gzipBytes(body)
	h.key, h.body, h.gz, h.gen, h.ms = key, body, gz, rep.Meta.Rev, rep.Meta.ScanMs
	return payload{body: body, gz: gz}
}

func gzipBytes(b []byte) []byte {
	var out bytes.Buffer
	out.Grow(len(b) / 4)
	zw, err := gzip.NewWriterLevel(&out, gzip.BestSpeed)
	if err != nil {
		return nil
	}
	if _, err := zw.Write(b); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return out.Bytes()
}

// fingerprint 用「数据目录里每个文件的路径 + 大小 + 修改时刻」拼出一个指纹。
//
// 只读元数据、不读内容：3300 份文件实测 ~30ms。刻意**不含**目录本身的 mtime ——
// 重写一份日文件用的是「.tmp + rename」，它会顺手改掉目录的 mtime，
// 但目录 mtime 也会被「某个和统计无关的文件被删掉」改掉，两边都不精确；
// 逐文件比对才是「这份快照还能不能用」的真正判据。
//
// 读不了就返回空串：空串永远不命中缓存，于是行为退化回「每次都重新扫」——
// 缓存出问题时宁可慢，也不能给出说不清来源的数字。
func fingerprint(root string) string {
	entries, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	h := fnv.New64a()
	var buf []byte
	for _, e := range entries {
		if e.IsDir() {
			// 目录只记名字，**不记它自己的大小和修改时刻**：Windows 更新目录 mtime
			// 是延迟的（写完文件过一会儿才落定），实测同一个空转的目录连读两次
			// 能给出两个不同的 mtime —— 拿它当判据就会永远不命中缓存。
			// 目录里的内容下面逐文件比，那才是真正决定统计结果的东西。
			_, _ = h.Write(append(append(buf[:0], "dir:"...), e.Name()...))
			_, _ = h.Write([]byte{'\n'})
			if strings.HasPrefix(e.Name(), "_") {
				continue // 下划线开头的不算数据目录（和 store.Scan 一致）
			}
			sub, err := os.ReadDir(filepath.Join(root, e.Name()))
			if err != nil {
				return ""
			}
			for _, f := range sub {
				fi, err := f.Info()
				if err != nil {
					return ""
				}
				buf = appendMeta(buf[:0], e.Name()+"/"+f.Name(), fi.Size(), fi.ModTime().UnixNano())
				_, _ = h.Write(buf)
			}
			continue
		}
		info, err := e.Info()
		if err != nil {
			return ""
		}
		// 根目录下的普通文件（_symbols.json 之类）会影响标的显示名，要算进去
		buf = appendMeta(buf[:0], e.Name(), info.Size(), info.ModTime().UnixNano())
		_, _ = h.Write(buf)
	}
	return strconv.FormatUint(h.Sum64(), 16)
}

func appendMeta(dst []byte, name string, size, mtime int64) []byte {
	dst = append(dst, name...)
	dst = append(dst, '|')
	dst = strconv.AppendInt(dst, size, 10)
	dst = append(dst, '|')
	dst = strconv.AppendInt(dst, mtime, 10)
	return append(dst, '\n')
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
	h := newSnapshotter(opts)
	s := &Server{
		URL:   fmt.Sprintf("http://127.0.0.1:%d/", addr.Port),
		Port:  addr.Port,
		Moved: moved,
	}
	s.httpSrv = &http.Server{
		Handler:           h.handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.ln = ln
	// 抢在浏览器发第一个请求之前把扫描跑掉（见 snapshotter.prewarm）
	go h.prewarm()
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
