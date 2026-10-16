package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMissingConfigIsGeneratedNotFatal(t *testing.T) {
	dir := t.TempDir()
	cfg, res, err := Load(dir)
	if err != nil {
		t.Fatalf("配置不存在不该报错: %v", err)
	}
	if !res.Created {
		t.Fatal("应当报告「本次生成了配置」")
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("没有生成配置文件: %v", err)
	}
	if cfg.IntervalSeconds != 300 {
		t.Fatalf("默认周期 = %d，期望 300（只考虑 5 分钟）", cfg.IntervalSeconds)
	}
	if cfg.SaveRawFrames != true || !cfg.CSVHeader {
		t.Fatalf("默认开关不对: SaveRawFrames=%v CSVHeader=%v", cfg.SaveRawFrames, cfg.CSVHeader)
	}
}

// TestGeneratedConfigIsComplete 兼作「配置项清单」：任何一项消失都会红。
func TestGeneratedConfigIsComplete(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Load(dir); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("生成的 %s:\n%s", FileName, raw)

	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("生成的配置不是合法 JSON: %v", err)
	}
	want := []string{
		"_comment", "matchUrl", "symbol", "cdpPort",
		"intervalSeconds", "outDir", "rawDir", "saveRawFrames", "csvHeader",
		"flushSeconds", "attachPollSeconds", "retrySeconds", "statusSeconds",
		"networkBufferMB", "maxFrameMB", "pauseOnExit",
	}
	for _, k := range want {
		if _, ok := m[k]; !ok {
			t.Fatalf("生成的配置缺少 %q", k)
		}
	}
	if _, ok := m["BaseDir"]; ok {
		t.Fatal("BaseDir 是运行期字段，不该写进文件")
	}
}

func TestPartialConfigMergesWithDefaults(t *testing.T) {
	dir := t.TempDir()
	content := `{"symbol":"CME_MINI:MNQ1!","outDir":"我的数据","打错了的键":1}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, res, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.Created {
		t.Fatal("配置已存在，不该报告 Created")
	}
	if cfg.Symbol != "CME_MINI:MNQ1!" || cfg.OutDir != "我的数据" {
		t.Fatalf("用户写的值没生效: %+v", cfg)
	}
	if cfg.IntervalSeconds != 300 || cfg.FlushSeconds != 1 {
		t.Fatalf("缺失的键没有被默认值补齐: %+v", cfg)
	}
	if len(res.UnknownKeys) != 1 || res.UnknownKeys[0] != "打错了的键" {
		t.Fatalf("没有认出多余的键: %v（拼错的键会被静默忽略，必须报出来）", res.UnknownKeys)
	}
}

func TestBadJSONIsAnError(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{不是 JSON"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(dir); err == nil {
		t.Fatal("配置损坏时应当报错，而不是悄悄用默认值跑起来")
	}
}

func TestUnreasonableValuesFallBackWithNote(t *testing.T) {
	dir := t.TempDir()
	content := `{"intervalSeconds":-5,"cdpPort":70000,"flushSeconds":-1}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, res, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.IntervalSeconds != 300 || cfg.CDPPort != 0 || cfg.FlushSeconds != 1 {
		t.Fatalf("非法值没有被拉回默认: %+v", cfg)
	}
	if len(res.Notes) == 0 {
		t.Fatal("回退必须留下说明，否则用户看不出自己写的值没生效")
	}
}

func TestRelativeDirsResolveAgainstConfigDir(t *testing.T) {
	dir := t.TempDir()
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "data"); cfg.OutPath() != want {
		t.Fatalf("OutPath = %q, 期望 %q（双击运行时工作目录不可靠，必须以配置目录为基准）", cfg.OutPath(), want)
	}
	if want := filepath.Join(dir, "raw"); cfg.RawPath() != want {
		t.Fatalf("RawPath = %q, 期望 %q", cfg.RawPath(), want)
	}

	abs := t.TempDir()
	cfg.OutDir = abs
	if cfg.OutPath() != abs {
		t.Fatalf("绝对路径不该被改写: %q", cfg.OutPath())
	}
}

func TestRoundTripPreservesValues(t *testing.T) {
	dir := t.TempDir()
	cfg, _, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Symbol = "NASDAQ:AAPL"
	cfg.OutDir = "out"
	cfg.IntervalSeconds = 300
	cfg.MaxFrameMB = 8
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	again, res, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Symbol != "NASDAQ:AAPL" || again.OutDir != "out" || again.MaxFrameMB != 8 {
		t.Fatalf("往返后值变了: %+v", again)
	}
	if len(res.UnknownKeys) != 0 {
		t.Fatalf("自己生成的配置不该有多余键: %v", res.UnknownKeys)
	}
	if again.Comment == "" || !strings.Contains(again.Comment, "README") {
		t.Fatalf("默认注释丢了: %q", again.Comment)
	}
}

func TestIntervalsHaveSenSibleDefaults(t *testing.T) {
	// 零值场景（例如用户把节流项全删了）不能变成 0 间隔忙等
	var c Config
	c.normalize()
	if c.FlushEvery() <= 0 || c.AttachPoll() <= 0 || c.RetryEvery() <= 0 || c.StatusEvery() <= 0 {
		t.Fatalf("间隔出现非正值: flush=%v attach=%v retry=%v status=%v",
			c.FlushEvery(), c.AttachPoll(), c.RetryEvery(), c.StatusEvery())
	}
}
