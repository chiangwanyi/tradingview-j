# vendor —— 内嵌进 exe 的两个前端库

零构建方案：没有 npm、没有打包步骤，这两个文件由 `//go:embed static` 直接内嵌进
`tv-capture.exe`，所以发布产物仍然只有一个 exe。

| 文件 | 版本 | 来源 |
|---|---|---|
| `vue.global.prod.js` | **3.5.13**（文件头有 `vue v3.5.13`） | https://unpkg.com/vue@3.5.13/dist/vue.global.prod.js |
| `tailwind.js` | **4.3.3**（能搜到 `var pr="4.3.3"`） | https://unpkg.com/@tailwindcss/browser@4/dist/index.global.js |

## 重新下载

Windows 上别用 `curl`（走 schannel，容易报 `SSL/TLS connection failed`），用 Python 的 urllib：

```bash
python -c "
import urllib.request, os
d = r'internal\webreport\static\vendor'
for name, url in {
    'vue.global.prod.js': 'https://unpkg.com/vue@3.5.13/dist/vue.global.prod.js',
    'tailwind.js':        'https://unpkg.com/@tailwindcss/browser@4/dist/index.global.js',
}.items():
    req = urllib.request.Request(url, headers={'User-Agent': 'Mozilla/5.0'})
    open(os.path.join(d, name), 'wb').write(urllib.request.urlopen(req, timeout=60).read())
"
```

- `@tailwindcss/browser@4` 用主版本号让 unpkg 解析到最新 4.x；钉死完整版本号的路径可能 404。
- 下完**核对版本**（版本串写在文件里），再跑一遍 `go test ./...` —— 内嵌资源变了，
  `server_test.go` 里「静态文件能取到且非空」的断言会第一时间发现拷贝漏了。
