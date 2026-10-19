package replay

// 真起一个服务、跑完整流程、再**真的重启**验恢复。默认跳过，只有设了 REPLAY_LIVE 才跑：
//
//	cd web && npm run build                 # 需要真实前端产物
//	REPLAY_LIVE=1 go test ./internal/replay/ -run TestLiveRestart -v
//
// 它和 persist_test.go 的分工：
//   - persist_test.go：假 hub + 临时数据目录，只测「存档 -> 读档」这一层，秒级、离线；
//   - 这个用例：真实 data/ 里的 CSV、真实的 web/dist、真实的 8799 端口，走的是
//     完整 HTTP + WebSocket + 机器人，最后真的把服务关掉重开。
//
// 端口用的是 srv.Port（Start 发现被占会自动换），所以 8799 也有人占也能跑。
//
// 所以它不放进 `go test ./...` 的默认集合：要占端口、要几秒、还依赖 dist 已构建。
// 但「重启后实例还在」这件事只有它能在真进程里证明 —— 每次改存档相关代码建议跑一次。

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tradingview-j/internal/wsclient"
)

// livePort 单独挑一个，避开 config.json 里正式用的 replayPort（8790）：
// 平时菜单 4 开着的服务就占着 8790，测试要是也用 8790，Start 会换端口（srv.Moved），
// 而写死端口的冒烟就会连到那个旧服务上去，把测试实例塞进人家的存档里。
const livePort = 8799

func liveOpts(stateDir string) Options {
	return Options{
		DataRoot: filepath.Join("..", "..", "data"),
		DistDir:  filepath.Join("..", "..", "web", "dist"),
		StateDir: stateDir,
		Version:  "0.4.0",
		Location: time.Local,
		Log:      func(f string, a ...any) { os.Stdout.WriteString("    [srv] " + fmt.Sprintf(f, a...) + "\n") },
	}
}

func TestLiveRestartKeepsInstances(t *testing.T) {
	if os.Getenv("REPLAY_LIVE") == "" {
		t.Skip("设 REPLAY_LIVE=1 才跑：会占用 8799 端口，需要真实 data/ 与 web/dist")
	}
	stateDir := t.TempDir()

	srv, err := Start(liveOpts(stateDir), livePort)
	if err != nil {
		t.Fatalf("启动服务失败：%v", err)
	}
	t.Logf("服务在 %s", srv.URL)
	// 端口被占时 Start 会自动换一个（srv.Moved），所以后面一律用 srv.Port 拼地址，
	// 不能写死 livePort —— 否则冒烟会连到「占着 8790 的那个别的服务」上去。
	if srv.Moved {
		t.Logf("注意：%d 被占用，已换到 %d", livePort, srv.Port)
	}

	// 1. 静态页面确实托管出来了（dist 是构建产物，很容易忘了 npm run build）
	for _, p := range []string{"/", "/index.html"} {
		resp, err := http.Get(srv.URL + p)
		if err != nil {
			t.Fatalf("GET %s 失败：%v", p, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("GET %s 返回 %d", p, resp.StatusCode)
		}
	}

	// 2. 完整冒烟（标的/建实例/步进/标注/挂单/机器人）—— 复用现成的那个用例
	t.Setenv("REPLAY_WS", fmt.Sprintf("ws://127.0.0.1:%d/ws", srv.Port))
	TestSmokeAgainstRunningServer(t)

	// 3. 关掉服务（这一步会把存档刷到盘上）
	if err := srv.Close(); err != nil {
		t.Fatalf("关闭失败：%v", err)
	}
	statePath := filepath.Join(stateDir, stateFileName)
	info, err := os.Stat(statePath)
	if err != nil {
		t.Fatalf("存档没写出来：%v", err)
	}
	t.Logf("存档索引 %s，%d 字节", statePath, info.Size())
	// v2：索引只记「有哪些实例」，每个实例的数据在 instances/<id>/ 下分开存
	if _, err := os.Stat(filepath.Join(stateDir, "instances")); err != nil {
		t.Fatalf("实例目录没建出来：%v", err)
	}

	// 4. 重开：实例应该还在，且标注/挂单也回来了
	srv2, err := Start(liveOpts(stateDir), livePort)
	if err != nil {
		t.Fatalf("二次启动失败：%v", err)
	}
	defer srv2.Close()

	conn, err := wsclient.Dial(fmt.Sprintf("ws://127.0.0.1:%d/ws", srv2.Port), wsclient.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("重连失败：%v", err)
	}
	defer conn.Close()
	if w := readMsg(t, conn); w["type"] != MsgWelcome {
		t.Fatalf("没收到 welcome：%v", w)
	}

	send(t, conn, map[string]any{"type": "list_instances"})
	list := readMsg(t, conn)
	arr, _ := list["instances"].([]any)
	if len(arr) == 0 {
		t.Fatalf("重启后实例没了：%v", list)
	}
	first := arr[0].(map[string]any)
	id := first["id"].(string)
	t.Logf("重启后实例仍在：%s %v，第 %v 根", id, first["symbol"], first["cursor"])

	send(t, conn, map[string]any{"type": "join_instance", "instanceId": id})
	// join 会先广播一条 control_changed（谁拿到了操作权），之后才是快照
	var state map[string]any
	for i := 0; i < 10; i++ {
		m := readMsg(t, conn)
		if m["type"] == MsgInstanceState {
			state = m
			break
		}
	}
	if state == nil {
		t.Fatal("join 之后没等到 instance_state")
	}
	inst := state["instance"].(map[string]any)
	anns, _ := inst["annotations"].([]any)
	orders, _ := inst["orders"].([]any)
	bars, _ := inst["bars"].([]any)
	t.Logf("恢复：%d 根 K 线 / %d 个标注 / %d 张挂单 / tick=%v",
		len(bars), len(anns), len(orders), inst["tick"])
	if len(anns) == 0 || len(orders) == 0 || len(bars) == 0 {
		t.Fatalf("恢复不完整：bars=%d anns=%d orders=%d", len(bars), len(anns), len(orders))
	}
	// tick 是「价格轴最小跳动」，快照里必须有；丢了前端价格轴会退回两位小数
	if tick, _ := inst["tick"].(float64); tick <= 0 {
		t.Fatalf("tick 没有带回来：%v", inst["tick"])
	}

	// 连标注的价格也应该是两位小数（csv 里 89.97000000000001 那种噪音不许漏到前端）
	if b0, ok := bars[0].(map[string]any); ok {
		if c, _ := b0["close"].(float64); c*100 != float64(int64(c*100+0.5)) {
			t.Fatalf("收盘价不是两位小数：%v", b0["close"])
		}
	}

	// 5. 重启后还能继续步进（说明游标与日文件索引都重新接上了）
	send(t, conn, map[string]any{"type": "step", "instanceId": id})
	ev := readAction(t, conn, ActBarAdvanced)
	if ev["payload"].(map[string]any)["bar"] == nil {
		t.Fatalf("重启后步进没有新 K 线：%v", ev)
	}
	t.Log("live 冒烟通过：静态页面、建实例、步进、标注、挂单、机器人、重启恢复、继续步进")
}
