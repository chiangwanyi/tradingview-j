package webreport

// 前端那半边也有「各写一份」的风险：app.js 里的 num / bytes / gap 是 Go 侧
// comma / humanBytes / gapText 的第二份实现。统计量不会漂移（只有 Go 在算），
// 但格式化会 —— 表现是「终端写 1时5分、网页写 65分」。
//
// 所以让 node 把 app.js 的纯函数区跑一遍断言（脚本在 testdata/format.test.cjs，
// 期望值照抄 report_test.go 的那张表）。没有 node 就跳过：
// 这是本机的交叉校验，不该变成所有人都必须装 node 的理由。

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFrontendFormattingMatchesGo(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("PATH 里没有 node，跳过前端格式化函数的交叉校验")
	}
	script, err := filepath.Abs(filepath.Join("testdata", "format.test.cjs"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(node, script).CombinedOutput()
	if err != nil {
		t.Fatalf("前端纯函数断言没过：%v\n%s", err, out)
	}
	if !strings.Contains(string(out), "OK") {
		t.Fatalf("node 没跑到最后：\n%s", out)
	}
}
