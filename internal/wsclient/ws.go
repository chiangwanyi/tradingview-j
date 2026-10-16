// Package wsclient 是一个「只够用」的 WebSocket 客户端（RFC 6455 子集）。
//
// 为什么自己写而不是引第三方库：整个项目要保持零第三方依赖（Go 标准库不含 WS 客户端），
// 而这里需要的只有一件事 —— 作为 CDP 客户端连上 Chrome 的调试端口，发文本帧、收文本帧。
// 扩展、压缩、重定向、cookie 一律不需要。
//
// 两个必须做对的点，都是被真实数据逼出来的：
//
//  1. **大帧。** Chrome 会把一整段图表历史塞进一个 WS 帧，实测单帧 247KB。
//     读帧要支持 16/64 位长度与分片重组，并且要有上限保护 —— 没有上限的话，
//     一个畸形的长度头就能把进程的内存吃光。
//  2. **掩码。** 客户端发给服务端的帧必须掩码，服务端发来的必须不掩码。
//     写错方向 Chrome 会直接断开（协议错误），而且报错信息不会告诉你原因。
package wsclient

import (
	"bufio"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"
)

// 帧 opcode（RFC 6455 §5.2）
const (
	OpContinuation byte = 0x0
	OpText         byte = 0x1
	OpBinary       byte = 0x2
	OpClose        byte = 0x8
	OpPing         byte = 0x9
	OpPong         byte = 0xA
)

var (
	// ErrClosed 在收到对端 Close 帧或底层连接断开时返回
	ErrClosed = errors.New("wsclient: 连接已关闭")
	// ErrTooLarge 在单帧超过 MaxPayload 时返回
	ErrTooLarge = errors.New("wsclient: 单帧超过上限")
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Options 只保留调用方真正会调的旋钮。
type Options struct {
	Timeout    time.Duration // 握手超时
	MaxPayload int64         // 单帧上限（字节）；<=0 时用 16MB
}

type Conn struct {
	conn       net.Conn
	br         *bufio.Reader
	writeMu    sync.Mutex
	maxPayload int64
	closed     bool
	closeMu    sync.Mutex
}

// Dial 完成握手并返回可用连接。rawURL 形如 ws://127.0.0.1:9222/devtools/browser/<uuid>
func Dial(rawURL string, opts Options) (*Conn, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("解析 WebSocket 地址失败: %w", err)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "ws" && scheme != "wss" {
		return nil, fmt.Errorf("不支持的协议 %q（只支持 ws/wss）", u.Scheme)
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 25 * time.Second
	}
	maxPayload := opts.MaxPayload
	if maxPayload <= 0 {
		maxPayload = 16 << 20
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" {
		if scheme == "wss" {
			port = "443"
		} else {
			port = "80"
		}
	}
	addr := net.JoinHostPort(host, port)

	dialer := &net.Dialer{Timeout: timeout}
	var nc net.Conn
	if scheme == "wss" {
		nc, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		nc, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return nil, fmt.Errorf("连接 %s 失败: %w", addr, err)
	}

	key := make([]byte, 16)
	if _, err := rand.Read(key); err != nil {
		nc.Close()
		return nil, err
	}
	keyB64 := base64.StdEncoding.EncodeToString(key)

	path := u.RequestURI()
	if path == "" {
		path = "/"
	}
	// 不主动带 Origin：CDP 端点在「无 Origin」时不会做来源校验，
	// 带上反而可能被 Chrome 拒绝（需要 --remote-allow-origins）。
	req := strings.Join([]string{
		"GET " + path + " HTTP/1.1",
		"Host: " + u.Host,
		"Upgrade: websocket",
		"Connection: Upgrade",
		"Sec-WebSocket-Key: " + keyB64,
		"Sec-WebSocket-Version: 13",
		"",
		"",
	}, "\r\n")

	nc.SetDeadline(time.Now().Add(timeout))
	defer nc.SetDeadline(time.Time{})

	if _, err := io.WriteString(nc, req); err != nil {
		nc.Close()
		return nil, fmt.Errorf("发送握手请求失败: %w", err)
	}

	br := bufio.NewReaderSize(nc, 64*1024)
	tp := textproto.NewReader(br)
	statusLine, err := tp.ReadLine()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("读取握手响应失败: %w", err)
	}
	headers, err := tp.ReadMIMEHeader()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("读取握手响应头失败: %w", err)
	}
	if !strings.Contains(statusLine, "101") {
		nc.Close()
		return nil, fmt.Errorf("握手被拒绝: %s", statusLine)
	}
	if got := headers.Get("Sec-WebSocket-Accept"); got != acceptKey(keyB64) {
		nc.Close()
		return nil, fmt.Errorf("Sec-WebSocket-Accept 校验失败: %q", got)
	}
	if ext := headers.Get("Sec-WebSocket-Extensions"); ext != "" {
		// 我们没请求任何扩展；对端若强加，帧的负载需要按扩展解码，
		// 那样读出来的就不是 JSON 了。提前报错，好过后面解出垃圾。
		nc.Close()
		return nil, fmt.Errorf("对端启用了未协商的扩展 %q", ext)
	}

	return &Conn{conn: nc, br: br, maxPayload: maxPayload}, nil
}

func acceptKey(keyB64 string) string {
	h := sha1.New()
	io.WriteString(h, keyB64+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// ReadMessage 读出一条完整应用消息（自动重组分片、自动回 pong、自动忽略 pong）。
// 收到 Close 帧时回一个 Close 并返回 ErrClosed。
func (c *Conn) ReadMessage() (byte, []byte, error) {
	var msgOp byte
	var acc []byte

	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case OpPing:
			// 控制帧必须尽快回，否则对端可能判定连接失效
			_ = c.writeFrame(OpPong, payload)
			continue
		case OpPong:
			continue
		case OpClose:
			_ = c.writeFrame(OpClose, payload)
			c.Close()
			return 0, nil, ErrClosed
		case OpContinuation:
			if msgOp == 0 {
				return 0, nil, errors.New("wsclient: 收到无起始帧的续帧")
			}
			acc = append(acc, payload...)
			if int64(len(acc)) > c.maxPayload {
				return 0, nil, ErrTooLarge
			}
			if fin {
				return msgOp, acc, nil
			}
		case OpText, OpBinary:
			if fin {
				return op, payload, nil
			}
			msgOp = op
			acc = payload
		default:
			// 保留 opcode，按协议应忽略
			continue
		}
	}
}

func (c *Conn) readFrame() (bool, byte, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, 0, nil, ErrClosed
		}
		return false, 0, nil, err
	}
	fin := hdr[0]&0x80 != 0
	if hdr[0]&0x70 != 0 {
		// 没协商扩展，RSV 位必须为 0；非 0 说明有人在中间改过协议
		return false, 0, nil, errors.New("wsclient: RSV 位非 0（未协商扩展）")
	}
	op := hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	length := int64(hdr[1] & 0x7f)

	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		v := binary.BigEndian.Uint64(ext[:])
		if v > uint64(1)<<62 {
			return false, 0, nil, ErrTooLarge
		}
		length = int64(v)
	}
	if length < 0 || length > c.maxPayload {
		return false, 0, nil, fmt.Errorf("%w: 声明 %d 字节 > 上限 %d", ErrTooLarge, length, c.maxPayload)
	}

	var mask [4]byte
	if masked {
		if _, err := io.ReadFull(c.br, mask[:]); err != nil {
			return false, 0, nil, err
		}
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return false, 0, nil, err
		}
	}
	if masked {
		for i := range payload {
			payload[i] ^= mask[i%4]
		}
	}
	return fin, op, payload, nil
}

// WriteText 发一条文本消息。我们发的永远是小的 JSON，所以不做分片。
func (c *Conn) WriteText(b []byte) error { return c.writeFrame(OpText, b) }

func (c *Conn) writeFrame(op byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.conn.SetWriteDeadline(time.Now().Add(15 * time.Second)); err != nil {
		return err
	}

	var head []byte
	head = append(head, 0x80|op) // FIN=1，无扩展
	n := len(payload)
	switch {
	case n < 126:
		head = append(head, 0x80|byte(n))
	case n <= 0xFFFF:
		head = append(head, 0x80|126, byte(n>>8), byte(n))
	default:
		head = append(head, 0x80|127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		head = append(head, ext[:]...)
	}

	var mask [4]byte
	if _, err := rand.Read(mask[:]); err != nil {
		return err
	}
	head = append(head, mask[:]...)

	buf := make([]byte, 0, len(head)+n)
	buf = append(buf, head...)
	masked := make([]byte, n)
	for i := 0; i < n; i++ {
		masked[i] = payload[i] ^ mask[i%4]
	}
	buf = append(buf, masked...)

	_, err := c.conn.Write(buf)
	return err
}

func (c *Conn) Close() error {
	c.closeMu.Lock()
	defer c.closeMu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	return c.conn.Close()
}

// SetReadDeadline 暴露出来给上层做「读超时」判断（CDP 侧目前不用，留着便于排查）
func (c *Conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }
