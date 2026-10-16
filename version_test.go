package main

// 版本号机制的回归测试。
//
// 存在的理由：版本号现在有**四个落点** —— 根目录 VERSION、main.go 的兜底字面量、
// build.cmd 里的注入与产物命名、winres/winres.json 里的 exe 属性模板。
// 它们任何一处漂了，症状都是「看不出来」的：exe 文件名写着 0.2.0、
// 横幅却打 0.1.0，或者属性页干脆是空的 —— 而这些只有用户装到别的机器上才发现。
// 所以这里把「只有一个来源」这件事钉死：改版本号只需要动 VERSION。
//
// 同理，build.cmd 必须是 CRLF（cmd.exe 按字节偏移回读脚本，LF 会吃掉行首字符），
// 这一条也在下面一起验。

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readFileT(t *testing.T, name string) string {
	t.Helper()
	buf, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("读 %s 失败：%v", name, err)
	}
	return string(buf)
}

// versionLine 取出 VERSION 里那一行版本号，并顺手确认它**只有一行** ——
// build.cmd 用的是 set /p（只读第一行），多余的行会被静默忽略，
// 那时候「改了 VERSION 却没生效」就会变成一个查不出原因的现象。
func versionLine(t *testing.T) string {
	t.Helper()
	var lines []string
	for _, l := range strings.Split(readFileT(t, "VERSION"), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			lines = append(lines, l)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("VERSION 必须只有一行有效内容（set /p 只读第一行），现在有 %d 行：%q", len(lines), lines)
	}
	return lines[0]
}

func TestVersionFileIsSingleSemverLine(t *testing.T) {
	ver := versionLine(t)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(ver) {
		t.Fatalf("VERSION = %q，要写成三段数字，例如 0.1.0", ver)
	}
	// Windows 的 VERSIONINFO 每段是 16 位，超了 go-winres 会当成非法版本
	for _, part := range strings.Split(ver, ".") {
		if len(part) > 5 {
			t.Fatalf("VERSION 的 %q 段太长，Windows 文件属性的每段上限是 65535", part)
		}
	}
}

// main.go 里的字面量必须跟 VERSION 一致：它是 go build / IDE 直接跑时的兜底值。
func TestGoFallbackVersionMatchesVersionFile(t *testing.T) {
	if want := versionLine(t); version != want {
		t.Fatalf("main.go 的 version = %q，VERSION 文件里是 %q；"+
			"升版本请改 VERSION（build.cmd 会注入），并同步这个兜底值", version, want)
	}
}

// build.cmd 的**编码是承重的**，不是风格问题：
//
//   - cmd.exe 回读批处理脚本时按**字节偏移**定位每一行，而多字节字符会让这个偏移和它按
//     代码页解码出来的字符对不上 —— 症状是**一行的尾段被单独当命令执行**。
//     实测（2026-10-16）：旧脚本 237 个非 ASCII 字节、跑得好好的；改版后涨到 1071 个，
//     跑到一半就开始报 `'。' is not recognized as an internal or external command`，
//     连带那几行 echo 整个消失。所以在构建脚本里写中文不只是不好看，是**会炸**。
//   - 裸 LF 又会让每行行首被吃掉（cmd 假设每个换行占 2 字节）。
//
// 两条都钉死：纯 ASCII + 全 CRLF。中文输出留给程序自己打。
func TestBuildCmdIsAsciiAndCRLF(t *testing.T) {
	buf, err := os.ReadFile("build.cmd")
	if err != nil {
		t.Fatalf("读 build.cmd 失败：%v", err)
	}
	for i, b := range buf {
		if b >= 0x80 {
			line := bytes.Count(buf[:i], []byte("\n")) + 1
			t.Fatalf("build.cmd 第 %d 行有非 ASCII 字节 0x%02x —— 脚本里不能出现中文，"+
				"cmd.exe 会把行尾当新命令执行（日志用英文写，中文留给程序自己打）", line, b)
		}
	}
	if n := bytes.Count(buf, []byte("\n")) - bytes.Count(buf, []byte("\r\n")); n != 0 {
		t.Fatalf("build.cmd 里有 %d 个裸 LF —— 行尾必须是 CRLF，"+
			"否则 cmd.exe 会吃掉每行开头若干字符", n)
	}
}

// build.cmd 是「让版本号真的进到产物里」的唯一执行者，所以它的四件事都要在：
// 从 VERSION 读、用 -X main.version 注入、产物名带 %VER%、产物**落在根目录**。
func TestBuildCmdInjectsAndNamesVersion(t *testing.T) {
	cmd := readFileT(t, "build.cmd")

	for _, want := range []string{
		"<VERSION",                   // 唯一的版本来源
		"-X main.version=",           // 注入到二进制里
		`-o "tv-capture-v%VER%.exe"`, // 产物名带版本号，且就地生成
		"--file-version",             // 写进 exe 的文件属性
		"--product-version",
	} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("build.cmd 里找不到 %q —— 版本号机制被改动了一半？", want)
		}
	}
}

// 产物**不许**再被挪进子目录（用户明确要求：exe 就放在根目录）。
// 这不只是摆放位置问题：outDir / rawDir 是相对 **exe 所在目录** 展开的，
// 把 exe 放进 dist\ 就等于让它去读 dist\data\ —— 而那份数据在根目录，
// 表现成「双击 dist 里的 exe，菜单 2 / 3 说没有数据」。
//
// 只看**会被执行的命令行**：rem 注释里提一句历史（「以前放在 dist」）是好事，
// 不该被这条断言误伤。
func TestBuildCmdKeepsExeInRootDir(t *testing.T) {
	var code []string
	for _, line := range strings.Split(readFileT(t, "build.cmd"), "\n") {
		if t := strings.TrimSpace(line); t == "rem" || strings.HasPrefix(t, "rem ") {
			continue
		}
		code = append(code, line)
	}
	body := strings.ToLower(strings.Join(code, "\n"))

	if strings.Contains(body, "dist") {
		t.Fatal("build.cmd 的命令里又出现了 dist —— 产物应当就地生成在根目录，" +
			"exe 换个目录就等于换一份数据目录（outDir 相对 exe 所在目录展开）")
	}
	if strings.Contains(body, "copy ") || strings.Contains(body, "mkdir ") {
		t.Fatal("build.cmd 里出现了 copy/mkdir —— 就地构建不需要搬运任何文件，" +
			"根目录的 config.json 就是程序要读的那份")
	}
}

// exe 属性模板里**不许**再写一份版本号：go-winres 的 --file-version/--product-version
// 会覆盖它，留着只会变成一个永远不生效、又看起来像真值的数字。
func TestWinresTemplateHasNoVersionLiteral(t *testing.T) {
	tpl := readFileT(t, filepath.Join("winres", "winres.json"))
	for _, key := range []string{`"file_version"`, `"product_version"`, `"FileVersion"`, `"ProductVersion"`} {
		if strings.Contains(tpl, key) {
			t.Fatalf("winres/winres.json 里出现了 %s —— 版本号只从 VERSION 来，"+
				"这个键写死在这里就会跟实际构建的版本号说的不一样", key)
		}
	}
}
