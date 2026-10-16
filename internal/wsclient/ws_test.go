package wsclient

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"tradingview-j/internal/wstest"
)

func dial(t *testing.T, url string, opts Options) *Conn {
	t.Helper()
	if opts.Timeout == 0 {
		opts.Timeout = 3 * time.Second
	}
	c, err := Dial(url, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestDialAndReadLargeFrame(t *testing.T) {
	const size = 300 * 1024 // 比实测最大帧（247KB）再大一点，逼出 64 位长度分支
	big := bytes.Repeat([]byte("A"), size)
	big[0], big[size-1] = '{', '}'

	ts := wstest.Start(t, func(conn net.Conn, br *bufio.Reader) error {
		if err := wstest.WriteFrame(conn, true, wstest.OpText, big); err != nil {
			return err
		}
		op, payload, err := wstest.ReadFrame(br)
		if err != nil {
			return err
		}
		if op != wstest.OpText || string(payload) != `{"id":1}` {
			return fmt.Errorf("收到的客户端帧不对: op=%d payload=%q", op, payload)
		}
		return wstest.WriteClose(conn)
	})

	c := dial(t, ts.URL, Options{MaxPayload: 8 << 20})
	op, payload, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != OpText {
		t.Fatalf("opcode = %d, 期望 %d", op, OpText)
	}
	if len(payload) != size {
		t.Fatalf("负载长度 = %d, 期望 %d（64 位长度或缓冲有问题）", len(payload), size)
	}
	if payload[0] != '{' || payload[size-1] != '}' {
		t.Fatal("负载首尾被破坏")
	}
	if err := c.WriteText([]byte(`{"id":1}`)); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if _, _, err := c.ReadMessage(); err != ErrClosed {
		t.Fatalf("收到 Close 后应返回 ErrClosed，实际 %v", err)
	}
}

func TestFragmentedMessageAndPing(t *testing.T) {
	ts := wstest.Start(t, func(conn net.Conn, br *bufio.Reader) error {
		// 先 ping：客户端必须回 pong，而且不能把它当成应用消息抛给上层
		if err := wstest.WriteFrame(conn, true, wstest.OpPing, []byte("hb")); err != nil {
			return err
		}
		op, payload, err := wstest.ReadFrame(br)
		if err != nil {
			return err
		}
		if op != wstest.OpPong || string(payload) != "hb" {
			return fmt.Errorf("没收到正确的 pong: op=%d payload=%q", op, payload)
		}
		// 再发一个被切成两段的消息
		if err := wstest.WriteFrame(conn, false, wstest.OpText, []byte(`{"m":"du",`)); err != nil {
			return err
		}
		return wstest.WriteFrame(conn, true, wstest.OpContinuation, []byte(`"p":[]}`))
	})

	c := dial(t, ts.URL, Options{})
	op, payload, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if op != OpText || string(payload) != `{"m":"du","p":[]}` {
		t.Fatalf("分片重组结果 = %q (op=%d)", payload, op)
	}
}

func TestFrameTooLargeIsRejected(t *testing.T) {
	ts := wstest.Start(t, func(conn net.Conn, br *bufio.Reader) error {
		return wstest.WriteFrame(conn, true, wstest.OpText, bytes.Repeat([]byte("x"), 4096))
	})
	c := dial(t, ts.URL, Options{MaxPayload: 1024})
	if _, _, err := c.ReadMessage(); err == nil {
		t.Fatal("超过上限的帧应当报错，而不是继续分配内存")
	}
}

func TestHandshakeRejectsNon101(t *testing.T) {
	ts := wstest.Start(t, nil)
	ts.StatusLine = "HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"

	_, err := Dial(ts.URL, Options{Timeout: 3 * time.Second})
	if err == nil {
		t.Fatal("非 101 响应应当报错")
	}
	if !strings.Contains(err.Error(), "404") {
		t.Fatalf("错误信息里应当带上状态码，实际: %v", err)
	}
}
