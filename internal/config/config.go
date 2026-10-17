// Package config 负责加载/生成运行配置。
//
// 为什么是 config.json 而不是命令行参数：这个程序的使用方式是「双击 exe」，
// 双击时根本没有机会传参。所以配置只能落在文件里，而且必须能被双击场景找到 ——
// 基准目录取 exe 所在目录（而不是当前工作目录，双击时后者可能是任意的），
// 找不到 exe 目录时才退回当前工作目录。
//
// 所有相对路径（outDir / rawDir）都相对基准目录展开，理由同上：
// 双击运行时 cwd 不可靠。
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

const FileName = "config.json"

// Config 的全部字段都可写进 config.json。零值会在 normalize 里被默认值补齐，
// 所以配置文件里少写几个键不会出事。
type Config struct {
	Comment string `json:"_comment"`

	// ---- 接入 ----
	MatchURL string `json:"matchUrl"` // 要挂到哪个标签页上（URL/标题子串匹配）
	Symbol   string `json:"symbol"`   // 强制指定标的；留空则从页面 URL / symbol_resolved 识别
	CDPPort  int    `json:"cdpPort"`  // 0 = 自动发现（DevToolsActivePort + 常见端口探测）

	// ---- 数据落盘 ----
	IntervalSeconds int64  `json:"intervalSeconds"` // 只收这个周期的 K 线，目前只考虑 5 分钟 = 300
	OutDir          string `json:"outDir"`          // <outDir>/<标的>/<yyyy-mm-dd>.csv
	RawDir          string `json:"rawDir"`          // <rawDir>/<标的>/<时间戳>.ndjson
	SaveRawFrames   bool   `json:"saveRawFrames"`   // 是否留档原始 WS 帧

	// ---- CSV ----
	CSVHeader bool `json:"csvHeader"` // 是否写表头行 time,open,high,low,close,volume

	// ---- 网页版分析 ----
	// 「3 网页版分析」起的那个本地服务监听哪个端口。0 = 让系统随便给一个空闲端口。
	// 给一个固定值是刻意的：端口固定了地址就能收藏，不然每次都要回终端抄一遍。
	WebPort int `json:"webPort"`

	// ---- 复盘（菜单 4）----
	// ReplayPort 是复盘服务的端口（HTTP 与 WebSocket 同一个端口）。
	ReplayPort int `json:"replayPort"`
	// ReplayDistDir 是前端构建产物（web/dist）相对 exe 目录的位置。
	// 开发期用 npm run dev 时不需要它；要用菜单 4 直接打开页面才需要先 npm run build。
	ReplayDistDir string `json:"replayDistDir"`
	// ReplayStateDir 是复盘实例的本地存档目录（replay-instances.json）。
	// 有了它，关掉服务再打开，实例、游标、标注、挂单、成交都还在。
	ReplayStateDir string `json:"replayStateDir"`

	// ---- 节奏（秒）----
	FlushSeconds      float64 `json:"flushSeconds"`      // CSV 落盘间隔
	AttachPollSeconds float64 `json:"attachPollSeconds"` // 轮询标签页的间隔
	RetrySeconds      float64 `json:"retrySeconds"`      // 接入失败后的重试间隔
	StatusSeconds     float64 `json:"statusSeconds"`     // 状态行打印间隔

	// ---- 限制 ----
	NetworkBufferMB int `json:"networkBufferMB"` // CDP 网络事件缓冲上限
	MaxFrameMB      int `json:"maxFrameMB"`      // 单帧上限，超了直接断开（防 OOM）

	// ---- 退出行为 ----
	PauseOnExit bool `json:"pauseOnExit"` // 出错退出时是否等回车（双击运行时能看清报错）

	// BaseDir 是配置文件的所在目录，不写回文件
	BaseDir string `json:"-"`
}

func Defaults() Config {
	return Config{
		Comment:           "配置说明见 README.md。程序不接受命令行参数，改配置就改这个文件。",
		MatchURL:          "tradingview.com/chart",
		Symbol:            "",
		CDPPort:           0,
		IntervalSeconds:   300,
		OutDir:            "data",
		RawDir:            "raw",
		SaveRawFrames:     true,
		CSVHeader:         true,
		WebPort:           8765,
		ReplayPort:        8790,
		ReplayDistDir:     "web/dist",
		ReplayStateDir:    "state",
		FlushSeconds:      1,
		AttachPollSeconds: 3,
		RetrySeconds:      5,
		StatusSeconds:     15,
		NetworkBufferMB:   200,
		MaxFrameMB:        16,
		PauseOnExit:       true,
	}
}

// LoadResult 记录本次加载的事实，供启动横幅如实汇报（而不是猜）。
type LoadResult struct {
	Path        string   // 实际使用的配置文件路径
	Created     bool     // 是否由本次运行生成
	UnknownKeys []string // 文件里有、但程序不认识的键（多半是拼错了）
	Notes       []string // normalize 期间的修正说明
}

func (c Config) Interval() time.Duration    { return time.Duration(c.IntervalSeconds) * time.Second }
func (c Config) FlushEvery() time.Duration  { return duration(c.FlushSeconds, 1) }
func (c Config) AttachPoll() time.Duration  { return duration(c.AttachPollSeconds, 3) }
func (c Config) RetryEvery() time.Duration  { return duration(c.RetrySeconds, 5) }
func (c Config) StatusEvery() time.Duration { return duration(c.StatusSeconds, 15) }

func duration(sec, def float64) time.Duration {
	if sec <= 0 {
		sec = def
	}
	return time.Duration(sec * float64(time.Second))
}

// OutPath / RawPath 把相对目录按基准目录展开
func (c Config) OutPath() string         { return resolve(c.BaseDir, c.OutDir) }
func (c Config) RawPath() string         { return resolve(c.BaseDir, c.RawDir) }
func (c Config) ReplayDistPath() string  { return resolve(c.BaseDir, c.ReplayDistDir) }
func (c Config) ReplayStatePath() string { return resolve(c.BaseDir, c.ReplayStateDir) }

func resolve(base, dir string) string {
	if dir == "" {
		dir = "."
	}
	if filepath.IsAbs(dir) {
		return filepath.Clean(dir)
	}
	return filepath.Join(base, dir)
}

// Load 读取 baseDir/config.json；不存在就写一份默认配置再继续。
// 「配置缺失不是错误」是刻意的 —— 双击场景下用户第一次跑不该看到报错。
func Load(baseDir string) (*Config, LoadResult, error) {
	res := LoadResult{Path: filepath.Join(baseDir, FileName)}

	raw, err := os.ReadFile(res.Path)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, res, fmt.Errorf("读取 %s 失败: %w", res.Path, err)
		}
		c := Defaults()
		c.BaseDir = baseDir
		if err := c.Save(); err != nil {
			return nil, res, err
		}
		res.Created = true
		return &c, res, nil
	}

	c := Defaults() // 先铺默认值：文件里缺的键保持默认
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, res, fmt.Errorf("%s 不是合法 JSON: %w", res.Path, err)
	}
	c.BaseDir = baseDir
	res.UnknownKeys = unknownKeys(raw, c)
	res.Notes = c.normalize()
	return &c, res, nil
}

// normalize 把明显不合理的值拉回安全范围，并如实说明改了什么。
// 静默纠正是最坏的选择：用户改了配置却看不出没生效，会去怀疑别的地方。
func (c *Config) normalize() []string {
	d := Defaults()
	var notes []string
	fix := func(name string, got, def any, bad bool) {
		if bad {
			notes = append(notes, fmt.Sprintf("%s=%v 不合理，已回退为 %v", name, got, def))
		}
	}

	// 布尔值没法区分「用户显式写了 false」和「没写」，所以不做回退，只补注释。
	if c.Comment == "" {
		c.Comment = d.Comment
	}
	if c.MatchURL == "" {
		c.MatchURL = d.MatchURL
	}
	if c.CDPPort < 0 || c.CDPPort > 65535 {
		fix("cdpPort", c.CDPPort, d.CDPPort, true)
		c.CDPPort = d.CDPPort
	}
	if c.IntervalSeconds <= 0 {
		fix("intervalSeconds", c.IntervalSeconds, d.IntervalSeconds, true)
		c.IntervalSeconds = d.IntervalSeconds
	}
	if c.OutDir == "" {
		c.OutDir = d.OutDir
	}
	if c.RawDir == "" {
		c.RawDir = d.RawDir
	}
	if c.FlushSeconds <= 0 {
		c.FlushSeconds = d.FlushSeconds
	}
	if c.AttachPollSeconds <= 0 {
		c.AttachPollSeconds = d.AttachPollSeconds
	}
	if c.RetrySeconds <= 0 {
		c.RetrySeconds = d.RetrySeconds
	}
	if c.StatusSeconds <= 0 {
		c.StatusSeconds = d.StatusSeconds
	}
	if c.NetworkBufferMB <= 0 {
		c.NetworkBufferMB = d.NetworkBufferMB
	}
	if c.MaxFrameMB <= 0 {
		c.MaxFrameMB = d.MaxFrameMB
	}
	// webPort / replayPort 允许 0（让系统随便给一个空闲端口），只挡住明显写错的值
	if c.WebPort < 0 || c.WebPort > 65535 {
		fix("webPort", c.WebPort, d.WebPort, true)
		c.WebPort = d.WebPort
	}
	if c.ReplayPort < 0 || c.ReplayPort > 65535 {
		fix("replayPort", c.ReplayPort, d.ReplayPort, true)
		c.ReplayPort = d.ReplayPort
	}
	if c.ReplayDistDir == "" {
		c.ReplayDistDir = d.ReplayDistDir
	}
	if c.ReplayStateDir == "" {
		c.ReplayStateDir = d.ReplayStateDir
	}
	return notes
}

func (c Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.Path()), 0o755); err != nil {
		return err
	}
	buf, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.Path(), append(buf, '\n'), 0o644)
}

func (c Config) Path() string { return filepath.Join(c.BaseDir, FileName) }

// unknownKeys 找出文件里有、结构体里没有的键。拼错的键会被静默忽略，
// 那是排查起来最费劲的一类问题，所以宁可多一行提醒。
func unknownKeys(raw []byte, cfg Config) []string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	known := map[string]bool{}
	t := reflectTags(cfg)
	for _, k := range t {
		known[k] = true
	}
	var out []string
	for k := range m {
		if !known[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func reflectTags(cfg Config) []string {
	buf, err := json.Marshal(cfg)
	if err != nil {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(buf, &m); err != nil {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
