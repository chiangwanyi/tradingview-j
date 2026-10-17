package replay

// 前端产物的「配得上」检查：不打开浏览器，只保证 dist/index.html 里引用的每一个
// 带哈希的资源，都能被复盘服务的静态路由取到，而且类型、缓存头都对。
//
// 为什么值得单独测：Go 不能 //go:embed 到 `../web/dist`，所以**页面和 exe 是两次构建**，
// 中间唯一的约定就是 index.html 里写的那几个文件名。掉文件、忘了 `npm run build`、
// 手工删过 `dist/assets` 里的东西 —— 表现全是「页面白屏」，而 Go 这一侧看不出任何异常
// （接口全 200，WS 也连得上）。这条用例把那个约定钉住。

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestFrontendDistIsServed(t *testing.T) {
	dist := filepath.Join("..", "..", "web", "dist")
	if !DistExists(dist) {
		t.Skip("web/dist 还没构建（cd web && npm run build），跳过产物检查")
	}

	hub := NewHub(NewDataSource(t.TempDir(), time.Local), "test", nil)
	srv := httptest.NewServer(NewHandler(hub, dist))
	defer srv.Close()

	// 1. 首页能取到，且是应用外壳（Vite 的 index.html 里有一个 #app 挂载点）
	page := getText(t, srv.URL+"/")
	if !strings.Contains(page, `id="app"`) {
		t.Fatalf("首页里没有 #app 挂载点，可能不是 Vite 产物：\n%s", head(page))
	}

	// 2. 首页引用的每个 /assets/ 资源都必须真的存在
	ref := regexp.MustCompile(`(?:src|href)="(/assets/[^"]+)"`)
	matches := ref.FindAllStringSubmatch(page, -1)
	if len(matches) == 0 {
		t.Fatalf("首页没有引用任何 /assets/ 资源，构建产物不完整：\n%s", head(page))
	}
	for _, m := range matches {
		path := m[1]
		resp := get(t, srv.URL+path)
		body := readAll(t, resp)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("%s 取不到（%d）：页面会白屏", path, resp.StatusCode)
		}
		if len(body) == 0 {
			t.Fatalf("%s 是空文件", path)
		}
		// Vite 产物文件名带内容哈希 → 允许长缓存
		if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
			t.Fatalf("%s 的 Cache-Control 应当是长缓存，实际是 %q", path, cc)
		}
		ct := resp.Header.Get("Content-Type")
		switch {
		case strings.HasSuffix(path, ".js") && !strings.Contains(ct, "javascript"):
			t.Fatalf("%s 的 Content-Type 不对：%q", path, ct)
		case strings.HasSuffix(path, ".css") && !strings.Contains(ct, "css"):
			t.Fatalf("%s 的 Content-Type 不对：%q", path, ct)
		}
		t.Logf("%s  OK  %d 字节  %s", path, len(body), ct)
	}

	// 3. 未知路径回退到 index.html（SPA 习惯），且不许被长缓存
	spa := get(t, srv.URL+"/some/deep/route")
	if spa.StatusCode != http.StatusOK {
		t.Fatalf("未知路径没有回退到首页：%d", spa.StatusCode)
	}
	if cc := spa.Header.Get("Cache-Control"); strings.Contains(cc, "immutable") {
		t.Fatalf("回退的 index.html 不该长缓存（否则改完前端刷新也看不到）：%q", cc)
	}

	// 4. 健康检查在，且是纯文本
	h := get(t, srv.URL+"/healthz")
	if body := readAll(t, h); string(body) != "ok" {
		t.Fatalf("/healthz 应当回 ok，实际 %q", string(body))
	}
}

// TestFrontendDistMissingGivesGuidance 没有 dist 时不能白屏，要给出「怎么构建」。
func TestFrontendDistMissingGivesGuidance(t *testing.T) {
	hub := NewHub(NewDataSource(t.TempDir(), time.Local), "test", nil)
	srv := httptest.NewServer(NewHandler(hub, filepath.Join(t.TempDir(), "不存在")))
	defer srv.Close()

	resp := get(t, srv.URL+"/")
	body := readAll(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("没有 dist 时首页也应当回 200 的说明页，实际 %d", resp.StatusCode)
	}
	text := string(body)
	for _, want := range []string{"前端还没构建", "npm run dev", "npm run build"} {
		if !strings.Contains(text, want) {
			t.Fatalf("说明页里缺少 %q：\n%s", want, head(text))
		}
	}
	// 说明页是排错用的，绝不能被缓存住
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Fatalf("说明页应当 no-store，实际 %q", cc)
	}
}

func get(t *testing.T, url string) *http.Response {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("请求 %s 失败：%v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func getText(t *testing.T, url string) string {
	t.Helper()
	return string(readAll(t, get(t, url)))
}

func readAll(t *testing.T, resp *http.Response) []byte {
	t.Helper()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读响应体失败：%v", err)
	}
	return b
}

func head(s string) string {
	if len(s) > 400 {
		return s[:400] + "…"
	}
	return s
}
