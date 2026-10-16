// Package rawlog 把收到的原始 WebSocket 帧原样留档成 NDJSON。
//
// 留档的意义不是「备份数据」——K 线已经落在 CSV 里了；它是为了**事后复盘协议**：
// 抓包里到底出现了哪些方法、某个字段的真实语义是什么。
// 这个项目里对协议字段的几次误判（bar_close_time 不是收盘时间、
// symbol_resolved 其实存在）都是靠翻抓包推翻的，所以这份留档值得留着。
//
// 目录结构：<rawDir>/<标的>/<yyyy-MM-ddTHH-mm-ss>.ndjson
// 每条记录一行 JSON，格式与旧版 Node 实现保持兼容，方便用老习惯读。
package rawlog

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Writer struct {
	path   string
	f      *os.File
	bw     *bufio.Writer
	mu     sync.Mutex
	closed bool
	count  int64
}

// Record 是留档记录的统一形状。字段用 omitempty，因为大多数帧没有 url（见下方注释）。
type Record struct {
	Kind      string `json:"k"`
	App       string `json:"app,omitempty"`
	At        int64  `json:"at,omitempty"`
	EventAt   int64  `json:"eventAt,omitempty"`
	Event     string `json:"event,omitempty"`
	Dir       string `json:"dir,omitempty"`
	RequestID string `json:"requestId,omitempty"`
	// URL 常常是空的：Chrome 不会补发 Network.enable 之前就已建立的 WebSocket 的
	// webSocketCreated 事件，帧照样收得到，但帧上就带不出连接地址了。
	URL      string `json:"url,omitempty"`
	Opcode   int    `json:"opcode,omitempty"`
	Len      int    `json:"len,omitempty"`
	Text     string `json:"text,omitempty"`
	B64      string `json:"b64,omitempty"` // 二进制帧按 base64 保真存，避免解码期丢信息
	TargetID string `json:"targetId,omitempty"`
	Title    string `json:"title,omitempty"`
	Symbol   string `json:"symbol,omitempty"`
	Port     int    `json:"port,omitempty"`
	Source   string `json:"source,omitempty"`
}

func Dir(root, folder string) string { return filepath.Join(root, folder) }

// New 在 <root>/<folder>/ 下开一个新的留档文件。
func New(root, folder string, now time.Time) (*Writer, error) {
	dir := Dir(root, folder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("创建留档目录 %s 失败: %w", dir, err)
	}
	name := now.Format("2006-01-02T15-04-05") + ".ndjson"
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("创建留档文件 %s 失败: %w", path, err)
	}
	return &Writer{path: path, f: f, bw: bufio.NewWriterSize(f, 64*1024)}, nil
}

func (w *Writer) Path() string { return w.path }

func (w *Writer) Count() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

// Write 追加一条记录。不在这里做 flush —— 帧可能每秒来几十条，
// 每条都刷盘等于把磁盘当内存用；Flush 由上层按秒调用。
func (w *Writer) Write(rec Record) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return os.ErrClosed
	}
	buf, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if _, err := w.bw.Write(append(buf, '\n')); err != nil {
		return err
	}
	w.count++
	return nil
}

func (w *Writer) Flush() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	return w.bw.Flush()
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.bw.Flush(); err != nil {
		w.f.Close()
		return err
	}
	return w.f.Close()
}
