package replay

// 测试用的数据目录：按真实落盘格式写 CSV（time 明文 + 表头 + 5 分钟一根），
// 这样测试覆盖的是「真的能读回生产数据」这条路径，而不是自己造的结构体。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const (
	testSymbol = "TEST:ABC"
	testFolder = "TEST_ABC"
)

// writeTestData 在临时目录里造 days 天数据，每天从 09:00 开始、每 5 分钟一根，共 25 根。
func writeTestData(t *testing.T, days int) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, testFolder)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("建目录失败：%v", err)
	}
	idx := map[string]any{
		"version": 1,
		"symbols": map[string]any{testSymbol: map[string]any{
			"folder":    testFolder,
			"tick":      0.25,
			"tickValue": 0.5, // 像 MNQ：一跳 0.25 点 = 0.5 美元
		}},
	}
	buf, _ := json.MarshalIndent(idx, "", "  ")
	if err := os.WriteFile(filepath.Join(root, "_symbols.json"), buf, 0o644); err != nil {
		t.Fatalf("写索引失败：%v", err)
	}

	base := time.Date(2026, 1, 5, 0, 0, 0, 0, time.Local) // 周一
	for d := 0; d < days; d++ {
		day := base.AddDate(0, 0, d)
		name := filepath.Join(dir, day.Format("2006-01-02")+".csv")
		f, err := os.Create(name)
		if err != nil {
			t.Fatalf("建 CSV 失败：%v", err)
		}
		fmt.Fprintln(f, "time,open,high,low,close,volume")
		for i := 0; i < 25; i++ {
			ts := day.Add(time.Duration(9*60+i*5) * time.Minute)
			open := 100 + float64(d*10) + float64(i)*0.5
			high := open + 1.5
			low := open - 1.5
			close := open + 0.25
			fmt.Fprintf(f, "%s,%s,%s,%s,%s,%d\n",
				ts.Format("2006-01-02 15:04"),
				num(open), num(high), num(low), num(close), 100+i)
		}
		f.Close()
	}
	return root
}

// num 与 store 里的写法一致：最短表示，不带多余的 0。
func num(f float64) string { return fmt.Sprintf("%v", f) }

// openTestInstance 用固定锚点打开一个实例，测试里最常用。
func openTestInstance(t *testing.T, root, anchor string, history, forward int) *Instance {
	t.Helper()
	ds := NewDataSource(root, time.Local)
	inst, err := ds.Open(testSymbol, anchor, history, forward, nil)
	if err != nil {
		t.Fatalf("打开实例失败：%v", err)
	}
	inst.ID = "ins-test"
	inst.Operator = "human"
	return inst
}

// newTestHub 起一个带临时数据目录的 Hub。
func newTestHub(t *testing.T, days int) (*Hub, string) {
	t.Helper()
	root := writeTestData(t, days)
	hub := NewHub(NewDataSource(root, time.Local), "test", t.Logf)
	return hub, root
}

// fakeClient 是一条假连接：收到的消息进 channel，测试直接读。
type fakeClient struct {
	t     *testing.T
	msg   chan map[string]any
	err   chan error
	conn  *Client
	fails int
}

func newFakeClient(t *testing.T, hub *Hub) *fakeClient {
	t.Helper()
	fc := &fakeClient{
		t:   t,
		msg: make(chan map[string]any, 256),
		err: make(chan error, 16),
	}
	fc.conn = hub.Register(func(b []byte) error {
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		select {
		case fc.msg <- m:
		default:
			return fmt.Errorf("测试客户端的消息队列满了")
		}
		return nil
	}, func() {})
	return fc
}

// send 发一条上行消息。
func (fc *fakeClient) send(hub *Hub, payload map[string]any) {
	fc.t.Helper()
	b, err := json.Marshal(payload)
	if err != nil {
		fc.t.Fatalf("序列化失败：%v", err)
	}
	hub.Handle(fc.conn, b)
}

// next 取下一条消息；超时就算失败（测试卡住要能看到是哪一步）。
func (fc *fakeClient) next() map[string]any {
	fc.t.Helper()
	select {
	case m := <-fc.msg:
		return m
	case <-time.After(2 * time.Second):
		fc.t.Fatalf("%s 等消息超时", fc.conn.Name)
		return nil
	}
}

// waitFor 一直取消息直到 type == want（跳过前面的广播）。
func (fc *fakeClient) waitFor(want string) map[string]any {
	fc.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case m := <-fc.msg:
			if m["type"] == want {
				return m
			}
		case <-deadline:
			fc.t.Fatalf("%s 没等到类型为 %s 的消息", fc.conn.Name, want)
			return nil
		}
	}
}

// waitAction 一直取消息直到某条广播的 action 匹配。
func (fc *fakeClient) waitAction(action string) map[string]any {
	fc.t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-fc.msg:
			if m["type"] == MsgEvent && m["action"] == action {
				return m
			}
		case <-deadline:
			fc.t.Fatalf("%s 没等到动作 %s", fc.conn.Name, action)
			return nil
		}
	}
}

// drain 把已有消息读空（用于断言「不该收到」之前清场）。
func (fc *fakeClient) drain() {
	for {
		select {
		case <-fc.msg:
		default:
			return
		}
	}
}

// hasType 判断队列里有没有某种消息，不消费。
func (fc *fakeClient) hasType(want string) bool {
	for {
		select {
		case m := <-fc.msg:
			if m["type"] == want {
				return true
			}
		default:
			return false
		}
	}
}
