package replay

// 服务端 WebSocket（RFC 6455 子集），**手写**。
//
// 为什么不用第三方库：这个仓库的承诺是「一个 exe、零第三方依赖」（go.mod 里没有 require）。
// 我们需要的只有「浏览器 <-> Go 之间收发 JSON 文本帧」，扩展、压缩、子协议一律不需要。
// 客户端那一半已经手写在 internal/wsclient 里（给 CDP 用），这里是另一半。
//
// 与浏览器对接必须做对的几点：
//   - 握手要回 101 + 正确的 Sec-WebSocket-Accept，否则浏览器直接判失败；
//   - 浏览器发来的帧**一定带掩码**，服务端发出去的**一定不能带**（写反了浏览器会断）；
//   - 单帧要支持 16/64 位长度并设上限，否则一个畸形长度头就能吃掉内存；
//   - 必须回 pong：浏览器不会自己发 ping，但会回 pong，服务端靠它判断连接还活着。

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// 帧 opcode（RFC 6455 §5.2）
const (
	opContinuation byte = 0x0
	opText         byte = 0x1
	opBinary       byte = 0x2
	opClose        byte = 0x8
	opPing         byte = 0x9
	opPong         byte = 0xA
)

const (
	maxFramePayload = 4 << 20 // 4MB：一条快照几百根 K 线也就几十 KB，4MB 足够宽松
	readIdleTimeout = 90 * time.Second
	writeTimeout    = 5 * time.Second
	pingInterval    = 25 * time.Second
)

var errWSClosed = errors.New("replay: 连接已关闭")

type wsConn struct {
	conn       net.Conn
	br         *bufio.Reader
	writeMu    sync.Mutex
	maxPayload int64
	closeOnce  sync.Once
}

// upgrade 把一条 HTTP 请求升级成 WebSocket。
// 失败时已经写过 HTTP 错误响应，调用方直接 return 即可。
func upgrade(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if r.Method != http.MethodGet {
		http.Error(w, "websocket 只支持 GET", http.StatusMethodNotAllowed)
		return nil, errors.New("非 GET")
	}
	if !headerHasToken(r.Header, "Connection", "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		http.Error(w, "这不是一个 websocket 握手请求", http.StatusBadRequest)
		return nil, errors.New("缺少 Upgrade 头")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		http.Error(w, "只支持 WebSocket 版本 13", http.StatusBadRequest)
		return nil, errors.New("版本不对")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	if key == "" {
		http.Error(w, "缺少 Sec-WebSocket-Key", http.StatusBadRequest)
		return nil, errors.New("缺少 key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "服务器不支持连接升级", http.StatusInternalServerError)
		return nil, errors.New("不支持 Hijack")
	}
	netConn, bufrw, err := hj.Hijack()
	if err != nil {
		return nil, fmt.Errorf("接管连接失败：%w", err)
	}
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + acceptKey(key) + "\r\n\r\n"
	_ = netConn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if _, err := io.WriteString(netConn, resp); err != nil {
		netConn.Close()
		return nil, fmt.Errorf("写握手响应失败：%w", err)
	}
	_ = netConn.SetWriteDeadline(time.Time{})
	return &wsConn{conn: netConn, br: bufrw.Reader, maxPayload: maxFramePayload}, nil
}

func acceptKey(key string) string {
	h := sha1.New()
	io.WriteString(h, key+wsGUID)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// ReadMessage 读出一条完整应用消息（自动重组分片、自动回 pong）。
func (c *wsConn) ReadMessage() (byte, []byte, error) {
	var msgOp byte
	var acc []byte
	for {
		fin, op, payload, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case opPing:
			_ = c.writeFrame(opPong, payload)
			continue
		case opPong:
			continue
		case opClose:
			_ = c.writeFrame(opClose, payload)
			c.Close()
			return 0, nil, errWSClosed
		case opContinuation:
			if msgOp == 0 {
				return 0, nil, errors.New("收到没有起始帧的续帧")
			}
			acc = append(acc, payload...)
			if int64(len(acc)) > c.maxPayload {
				return 0, nil, errors.New("消息超过上限")
			}
			if fin {
				return msgOp, acc, nil
			}
		case opText, opBinary:
			if fin {
				return op, payload, nil
			}
			msgOp = op
			acc = payload
		default:
			continue // 保留 opcode，按协议忽略
		}
	}
}

func (c *wsConn) readFrame() (bool, byte, []byte, error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return false, 0, nil, errWSClosed
		}
		return false, 0, nil, err
	}
	fin := hdr[0]&0x80 != 0
	if hdr[0]&0x70 != 0 {
		return false, 0, nil, errors.New("RSV 位非 0（没有协商任何扩展）")
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
		if v > uint64(c.maxPayload) {
			return false, 0, nil, errors.New("帧长度超过上限")
		}
		length = int64(v)
	}
	if length < 0 || length > c.maxPayload {
		return false, 0, nil, fmt.Errorf("帧长度 %d 超过上限 %d", length, c.maxPayload)
	}
	// RFC 6455 §5.1：客户端发来的帧必须掩码。不强制的话，普通网页里的
	// 跨站请求也能连上（浏览器无法为 WS 设置掩码），等于给了一个绕过同源的口子。
	if !masked {
		return false, 0, nil, errors.New("客户端帧没有掩码")
	}
	var mask [4]byte
	if _, err := io.ReadFull(c.br, mask[:]); err != nil {
		return false, 0, nil, err
	}
	payload := make([]byte, length)
	if length > 0 {
		if _, err := io.ReadFull(c.br, payload); err != nil {
			return false, 0, nil, err
		}
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return fin, op, payload, nil
}

// WriteText 发一条文本消息。
func (c *wsConn) WriteText(b []byte) error { return c.writeFrame(opText, b) }

func (c *wsConn) WritePing() error { return c.writeFrame(opPing, nil) }

// writeFrame 写一帧。服务端的帧**不加掩码**（加了浏览器会按协议错误断开）。
func (c *wsConn) writeFrame(op byte, payload []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	_ = c.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	head := make([]byte, 0, 10)
	head = append(head, 0x80|op)
	n := len(payload)
	switch {
	case n < 126:
		head = append(head, byte(n))
	case n <= 0xFFFF:
		head = append(head, 126, byte(n>>8), byte(n))
	default:
		head = append(head, 127)
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(n))
		head = append(head, ext[:]...)
	}
	if _, err := c.conn.Write(head); err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	_, err := c.conn.Write(payload)
	return err
}

func (c *wsConn) Close() error {
	var err error
	c.closeOnce.Do(func() {
		_ = c.writeFrame(opClose, nil)
		err = c.conn.Close()
	})
	return err
}

func (c *wsConn) RemoteAddr() string { return c.conn.RemoteAddr().String() }

// Serve 把一条已升级的连接接到 Hub 上：注册 -> 心跳 -> 读循环 -> 注销。
func (h *Hub) Serve(conn *wsConn) {
	c := h.Register(conn.WriteText, func() { _ = conn.Close() })
	defer h.Unregister(c)

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(pingInterval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				if err := conn.WritePing(); err != nil {
					return
				}
			}
		}
	}()
	defer func() {
		close(done)
		wg.Wait()
		_ = conn.Close()
	}()

	for {
		_ = conn.conn.SetReadDeadline(time.Now().Add(readIdleTimeout))
		op, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		if op == opText {
			h.Handle(c, payload)
		}
	}
}
