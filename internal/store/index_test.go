package store

// _symbols.json 的写回：手写字段（tick / tickValue / commission）必须一位不差地带回去。
//
// 为什么专门钉这一条：saveIndexLocked 是「按结构整个重写」，它写出去的就是文件里的全部内容。
// 少列一个字段 → 用户配好的值在「出现一个新标的」的那一刻静默消失，没有任何报错；
// 症状要到复盘结算成本的时候才浮出来（小数位变了、手续费少了），那时已经找不到原因。
// 2026-10-29 就有过一次：commission 只写进 JSON、没进 symRec，一有新标的就被抹掉。

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const indexWithCommissions = `{
  "version": 1,
  "symbols": {
    "CME_MINI:MES1!": {"folder": "CME_MINI_MES1!", "tick": 0.25, "tickValue": 1.25, "commission": 0.5},
    "NYMEX:CL1!":     {"folder": "NYMEX_CL1!",     "tick": 0.01, "tickValue": 10,   "commission": 0}
  }
}
`

func TestIndexRewriteKeepsCommission(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, indexFile)
	if err := os.WriteFile(path, []byte(indexWithCommissions), 0o644); err != nil {
		t.Fatalf("写初始索引失败: %v", err)
	}

	s := newStore(t, root, true)
	// 出现一个索引里没有的新标的 —— 这正是 saveIndexLocked 被触发的那一刻
	if got := s.Folder("COMEX_MINI:MGC1!"); got != "COMEX_MINI_MGC1!" {
		t.Fatalf("新标的目录 = %q", got)
	}
	if err := s.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读回索引失败: %v", err)
	}

	var idx symbolIndex
	if err := json.Unmarshal(raw, &idx); err != nil {
		t.Fatalf("索引不再是合法 JSON: %v\n%s", err, raw)
	}
	mes := idx.Symbols["CME_MINI:MES1!"]
	if mes.Commission == nil {
		t.Fatalf("MES 的 commission 被写没了；现在的索引：\n%s", raw)
	}
	if *mes.Commission != 0.5 {
		t.Fatalf("MES 的 commission = %v，应当是 0.5", *mes.Commission)
	}
	if mes.Tick != 0.25 || mes.TickValue != 1.25 {
		t.Fatalf("MES 的 tick/tickValue 被改动了：%+v", mes)
	}

	// 0 和「没配」必须是两件事：CL 显式免手续费，0 得留在文件里
	cl := idx.Symbols["NYMEX:CL1!"]
	if cl.Commission == nil {
		t.Fatalf("CL 的 commission=0 被当成「没配」省掉了；现在的索引：\n%s", raw)
	}
	if *cl.Commission != 0 {
		t.Fatalf("CL 的 commission = %v，应当是 0", *cl.Commission)
	}
	// 新标的不带手续费，就不该凭空多出一个 commission 键
	if mgc := idx.Symbols["COMEX_MINI:MGC1!"]; mgc.Commission != nil {
		t.Fatalf("新标的不该凭空有手续费，实际 %v", *mgc.Commission)
	}

	// 再开一次：重复 Flush 不该把内容改花
	if err := s.Flush(); err != nil {
		t.Fatalf("第二次 Flush: %v", err)
	}
	again, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("第二次读回失败: %v", err)
	}
	if !strings.Contains(string(again), `"commission": 0.5`) {
		t.Fatalf("第二次写回后 0.5 不见了：\n%s", again)
	}
}
