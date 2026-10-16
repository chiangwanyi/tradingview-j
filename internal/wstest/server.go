// Package wstest 是测试专用的小工具：一个手写的 WebSocket 服务端。
//
// 为什么要自己写而不是引现成的：整个项目保持零第三方依赖，
// 而且这里需要能精确构造「大帧」「分片」「掩码」这些边界，用现成库反而不好塞。
// 本包只被 _test.go 引用，不会进最终产物。
package wstest

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"testing"
)

const wsGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	OpText         byte = 0x1
	OpBinary       byte = 0x2
	OpContinuation byte = 0x0
	OpClose        byte = 0x8
	OpPing         byte = 0x9
	OpPong         byte = 0xA
)

type Server struct {
	URL string
	ln  net.Listener
	// Response 允许测试改握手响应（用来验证客户端对非 101 的处理）
	StatusLine string
	done       chan struct{}
}

// Start 起一个只接受一条连接的服务端。handler 收到的是握手完成后的连接。
func Start(t testing.TB, handler func(conn net.Conn, br *bufio.Reader) error) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("监听失败: %v", err)
	}
	s := &Server{
		URL:  "ws://" + ln.Addr().String() + "/devtools/browser/test",
		ln:   ln,
		done: make(chan struct{}),
	}
	go func() {
		defer close(s.done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		if err := s.handshake(conn, br); err != nil {
			t.Logf("握手失败: %v", err)
			return
		}
		if handler != nil {
			if err := handler(conn, br); err != nil {
				t.Logf("handler 返回错误: %v", err)
			}
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		<-s.done
	})
	return s
}

func (s *Server) handshake(conn net.Conn, br *bufio.Reader) error {
	tp := textproto.NewReader(br)
	if _, err := tp.ReadLine(); err != nil {
		return err
	}
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return err
	}
	if s.StatusLine != "" {
		_, err := io.WriteString(conn, s.StatusLine)
		return err
	}
	key := hdr.Get("Sec-WebSocket-Key")
	if key == "" {
		return fmt.Errorf("客户端没送 Sec-WebSocket-Key")
	}
	h := sha1.New()
	io.WriteString(h, key+wsGUID)
	accept := base64.StdEncoding.EncodeToString(h.Sum(nil))
	resp := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + accept + "\r\n\r\n"
	_, err = io.WriteString(conn, resp)
	return err
}

// WriteFrame 服务端 -> 客户端。按 RFC 不掩码。
func WriteFrame(w io.Writer, fin bool, op byte, payload []byte) error {
	b0 := op
	if fin {
		b0 |= 0x80
	}
	head := []byte{b0}
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
	if _, err := w.Write(head); err != nil {
		return err
	}
	_, err := w.Write(payload)
	return err
}

// ReadFrame 读客户端帧并解掩码。客户端帧必须掩码，这里会校验。
func ReadFrame(r io.Reader) (op byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	op = hdr[0] & 0x0f
	if hdr[1]&0x80 == 0 {
		return 0, nil, fmt.Errorf("客户端帧没有掩码，违反 RFC 6455")
	}
	n := int64(hdr[1] & 0x7f)
	switch n {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(r, ext[:]); err != nil {
			return 0, nil, err
		}
		n = int64(binary.BigEndian.Uint64(ext[:]))
	}
	var mask [4]byte
	if _, err := io.ReadFull(r, mask[:]); err != nil {
		return 0, nil, err
	}
	buf := make([]byte, n)
	if _, err := io.ReadFull(r, buf); err != nil {
		return 0, nil, err
	}
	for i := range buf {
		buf[i] ^= mask[i%4]
	}
	return op, buf, nil
}

func WriteText(w io.Writer, s string) error { return WriteFrame(w, true, OpText, []byte(s)) }
func WriteClose(w io.Writer) error          { return WriteFrame(w, true, OpClose, nil) }
