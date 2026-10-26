package replay

// 交易记录（v0.11.0 叫「录像」，v0.18.0 起改成**自动**的交易记录）。
//
// 记下来的**不是视频**，而是「开仓前的那一屏 + 之后每一条操作广播」：
// 一次复盘里真正有价值的不是像素，而是「第几步、谁、做了什么」。视频既占地方
// 又搜不动，而事件流可以逐帧跳、可以单步、还能拿去做回归测试。
//
// 五条刻意的设计：
//
//  1. **没有开关**（v0.18.0）。录制不再是一件用户要记得去做的事：仓位从空仓变成
//     有仓位的那一刻自动开始，仓位回到空仓的那一刻自动结束。界面上没有「录制中」，
//     因为它不是一种状态，而是交易本来就有的副产品 —— 见 trade.go 的触发点说明。
//  2. **复用现有的操作记录**。录制的挂点在 hub.broadcastInstanceLocked 上 ——
//     那条路径是所有状态变化的唯一出口（真人点按钮、机器人、多人广播都走它）。
//     所以「机器人操作也能被记」不是额外功能，而是**在正确的位置只挂一次**的自然结果；
//     也正因为如此，记下来的内容和「操作记录」Tab 里看到的是同一批事件。
//  3. **一步 = 一次操作**（v0.15.0）。[下一根]、画一个图形、挂一张单、录一段音，
//     各自占一步 —— 每一个用户手势本来就只产生一条广播，所以这个映射是 1:1 的，
//     不需要另加埋点。为什么要这样：v0.14 及以前「一步 = 一根 K 线」，一根 K 线上
//     画两个图形、挂一张单、说一段话全塞在同一步里，回放时那一步**一次性**把
//     一整根 K 线的变化全都摆出来，看不出用户真实的操作顺序。
//  4. **第 1 步 = 开仓前的那一屏**。Base 是开仓成交**之前**的盘面（空仓），
//     所以第 2 步就是开仓那一笔成交 —— 回放能看到「怎么进的场」。
//     步数由服务端数，前端只显示，避免「界面写 12 步、文件里只有 11 段」。
//  5. **语音按 K 线时间挂载、自己占一步**。Time 用的是那根 K 线的明文时间
//     （"2026-10-16 09:45"），和标注、挂单、成交的时间口径完全一致；音频文件单独存，
//     不塞进 JSON。语音那一步不改盘面（Events 为空），回放到它画面不动、语音响 ——
//     这正是真实发生的事。
//
// v0.16.0 的一条补充：**语音不是交易记录的子功能**（见 voice.go）。语音讲解挂在实例上，
// 随时能录；交易期间录的那些会在这里**额外抄一份**进记录（连同它的那一步）。
// 所以本文件仍然拥有「语音步」的全部规则，但它不再拥有「语音」这件事本身：
// 额度（每根 9 条）、清单、清空范围都以 voice.go 为准，这里只是沿用同一个数。
//
// 落盘结构：
//
//	<recordingsDir>/recordings.json                索引（列表用，省得每次读全部记录）
//	<recordingsDir>/<记录 id>/recording.json       开仓前的盘面 + 每一步的事件 + 统计
//	<recordingsDir>/<记录 id>/audio-1.wav          语音（16kHz 单声道 PCM）
//
// 索引丢了也不要紧：Open 时会扫一遍目录重建，记录文件本身才是唯一事实。
//
// **格式不兼容**：Steps 的语义从「一根 K 线一段」变成「一个操作一段」，所以
// Recording.Format 从 1 升到 2，旧记录既不迁移也不混读（需求原话：旧的不用留）。
// scan 会跳过 Format != 2 的目录并在日志里说一句，Load 会明确报「旧格式」。
// v0.18.0 **加字段不加版本**：TradeStats / InstanceID 都是可缺省的附加信息，
// format=2 的老文件照样能读，缺的统计在读取时按事件流反推（见 deriveStats）。

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"tradingview-j/internal/strategy"
)

const (
	// RecordingFormat 是当前记录格式。1 = 一步一根 K 线（v0.11 – v0.14），
	// 2 = 一步一个操作（v0.15.0 起）。两种语义不兼容，见文件头注释。
	RecordingFormat = 2

	// maxAudiosPerBar 是**同一根 K 线**上最多挂几段语音（需求写死 9 条）。
	//
	// 注意是「每根 K 线」不是「每条记录」：整条记录的语音数量不设上限，这条限制的
	// 用处只有一个 —— 防止用户在**同一根 K 线**上反复重录，把那根 K 线撑成几十条，
	// 回放时听不完。换到下一根 K 线就重新有 9 条额度。
	//
	// 额度归 voice.go（maxClipsPerBar）管，记录器只是沿用同一个数。这里留个别名，
	// 是因为记录器自己也要挡一道（它可能被别处直接调用），而且历史注释和测试
	// 都按这个名字读。
	maxAudiosPerBar = maxClipsPerBar

	// maxAudioBytes 是单段语音的上限。16kHz / 16bit / 单声道 = 32KB/s，
	// 2MB 大约 64 秒 —— 一根 K 线上的一句讲解足够了。
	//
	// 这个数不是随便定的：语音走 WebSocket（上行单帧上限 4MB，见 ws.go 的
	// maxFramePayload），base64 会膨胀三分之一。2MB → 2.67MB，留足余量；
	// 再大就会在**发送端**被帧上限掐断，表现成「录音一松手连接就断」，极难排查。
	maxAudioBytes = 2 << 20

	// maxAudioBase64 是 base64 字符串的长度上限（解码前先挡一道，
	// 免得一条畸形消息把内存吃掉）。
	maxAudioBase64 = maxAudioBytes/3*4 + 16

	recordingFileName   = "recording.json"
	recordingsIndexName = "recordings.json"
	recordingsVersion   = 2
)

// StepContext 是「这一步发生在盘面的哪个位置」——由广播方（hub）在调用 observe 时
// 从实例上取。记录器自己不持有实例：它只在 h.mu 下面被调用，拿一份快照更安全，
// 也不会在实例被删掉之后留一个悬空指针。
type StepContext struct {
	Time   string // 当前那根 K 线的明文时间（空 = 还没有 K 线）
	Cursor int    // 当前游标
}

// ---------------------------------------------------------------------------
// 记录缓冲：跟着实例走
// ---------------------------------------------------------------------------

// tradeRecorder 是「这一笔交易正在记的那一份」。
//
// 它**不带自己的锁**：所有方法都只在 hub.mu 里被调用（广播、步进、语音
// 都在那把锁下面），所以记录顺序天然等于广播顺序 —— 这正是回放能严格复现
// 实时画面的前提。唯一在锁外做的是落盘（见 Save / saveTrade）。
type tradeRecorder struct {
	rec      *Recording
	audios   []pendingAudio
	audioSeq int
	// stats 边走边算的交易统计（见 trade.go）。草稿也要带上它 ——
	// 进程崩掉之后列表里那一行至少得是「赚了多少」而不是空白。
	stats statsTracker

	// cur 是「最近一步发生时的盘面位置」。observe 每开一步就更新它，
	// addAudio 靠它决定语音挂在哪根 K 线上 —— 语音不是广播驱动的那一步
	// （它由 addAudio 自己开段），所以不能像别的动作那样从参数里拿。
	cur StepContext

	// dir 是这条记录的目录（`<recordings>/<id>`，开始记录那一刻定下来）。
	// 空 = 没配记录目录，这时语音只留在内存里，收尾时 Save 会报错。
	//
	// 语音在**录的当下就写进 dir**，不等收尾：语音浮窗的预览播放走的是
	// /api/recordings/<id>/audio/<file> 这条 HTTP 路由，它从磁盘读 —— 如果
	// 攒到收尾才写，交易进行中根本没法听自己刚录的那条。顺带崩溃也不丢语音。
	dir  string
	logf func(format string, args ...any)

	// dirty 表示「自上次检查点以来又录进了东西」。
	//
	// 交易可能持续几十分钟甚至过夜，只在收尾时落盘的话，一次崩溃就全丢了。
	// stateLoop 每两秒看一眼这个标记，脏了就把当前这份写成草稿（见 checkpointTrades）。
	dirty bool
}

// pendingAudio 是一段还没落盘的语音：摘要进 JSON，字节等收尾时一起写文件。
type pendingAudio struct {
	clip AudioClip
	data []byte
}

// newTradeRecorder 以「开仓之前的那一屏」为第 1 步开一段记录。
//
// base 由 hub 在开仓那一笔成交**之前**抓下来（见 captureTradeBaseLocked）：
// 第 2 步就是开仓成交，所以回放从空仓开始演，入场那一下看得见。
// dir 是这条记录的目录（由 RecordingStore.MarkLive 建好）；logf 用来报告
// 「语音写不进磁盘」这类不影响记录继续的问题。
func newTradeRecorder(inst *Instance, base RecordingBase, by, id, dir string, logf func(format string, args ...any)) *tradeRecorder {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if base.Bars == nil {
		base.Bars = []Bar{}
	}
	if base.Annotations == nil {
		base.Annotations = []Annotation{}
	}
	if base.Orders == nil {
		base.Orders = []Order{}
	}
	if base.Trades == nil {
		base.Trades = []Trade{}
	}
	if base.ClosedLots == nil {
		base.ClosedLots = []ClosedLot{}
	}
	start := ""
	if len(base.Bars) > 0 {
		start = base.Bars[len(base.Bars)-1].Time
	}
	now := time.Now()
	source, sourceID, sourceName := tradeSource(inst, by)
	rec := &Recording{
		RecordingSummary: RecordingSummary{
			ID:         id,
			Symbol:     inst.Symbol,
			Folder:     inst.Folder,
			Anchor:     start,
			InstanceID: inst.ID,
			CreatedBy:  by,
			CreatedAt:  now.Unix(),
			Source:     source,
			SourceID:   sourceID,
			SourceName: sourceName,
		},
		Format:    RecordingFormat,
		Tick:      inst.Tick,
		TickValue: inst.TickValue,
		Base:      base,
		// Events / Audios 都**显式给空切片**，不能留 nil：Go 的 nil slice 序列化成
		// `null`，而前端在步进条上直接读 `s.events.length`。第 1 步本来就没有事件，
		// 留 nil 会让「打开回放」当场抛 TypeError（实测 2026-10：整个播放器渲染不出来，
		// 但顶栏已经进了回放态，看着像卡死）。协议里数组一律是数组，不是 null。
		Steps: []RecordingStep{{
			Step:   1,
			At:     now.UnixMilli(),
			Kind:   StepBase,
			By:     by,
			Time:   start,
			Cursor: base.Cursor,
			Events: []RecordingEvent{},
		}},
		Audios: []AudioClip{},
	}
	r := &tradeRecorder{
		rec:  rec,
		cur:  StepContext{Time: start, Cursor: base.Cursor},
		dir:  dir,
		logf: logf,
	}
	// 开仓**之前**录的那些讲解：这一刻盘面上已经有了，就该和 base 里的图形、挂单
	// 一样出现在回放里。它们不占步（Step = 0）—— 那时候还没有「第几步」这回事。
	if inst != nil {
		v := inst.voiceNotes()
		adopted, pend := adoptPreOpenVoices(v.all(), v.dir, dir, baseBarTimes(base), 0, logf)
		for _, c := range adopted {
			rec.Audios = append(rec.Audios, c)
		}
		r.audios = append(r.audios, pend...)
		r.audioSeq = len(adopted) // 之后录的那几条从 au(n+1) 接着编号
	}
	r.stats = newStatsTracker(inst.pointValue())
	r.dirty = true
	return r
}

// tradeSource 决定这一笔的**操作来源**（v0.21.0），在开仓那一刻算一次。
//
// 判据是 `by`（广播里的操作者）：机器人动的每一步都写 "bot"（见 bot.go），
// 真人写的是客户端名字。所以「开仓那一手是谁下的」就是这一笔的来源 ——
// 机器人接管时下的单，回放里也全是机器人下的，两者本来就是同一件事。
//
// 机器人再往下细一层看 `inst.botStrategy`：空 = 随机演示机器人（stub.go 那条
// 「只会乱点」的链路），非空 = 注册表里的策略。策略名从注册表现查，查不到就退回 id ——
// 显示一个 id 也比显示空白强，至少能看出「跑的不是随机演示」。
func tradeSource(inst *Instance, by string) (source, sourceID, sourceName string) {
	if by != SourceBot {
		return SourceHuman, "", strings.TrimSpace(by)
	}
	id := ""
	if inst != nil {
		id = strings.TrimSpace(inst.botStrategy)
	}
	if id == "" {
		return SourceBot, SourceDemoBot, SourceDemoName
	}
	for _, info := range strategy.List() {
		if info.ID == id {
			return SourceBot, id, info.Name
		}
	}
	return SourceBot, id, id
}

// normalizeSource 给**旧记录**补上操作来源（v0.20.0 及以前没有这三个字段）。
//
// 反推只能靠 CreatedBy：那一代机器人动的每一步都写 "bot"，真人写客户端名字，
// 所以 CreatedBy == "bot" 就是机器人 —— 策略无从得知，按「随机演示」记。
// 这一步刻意放在**读取**侧（scan / Load）而不是写入侧：已经在盘上的文件不该
// 因为升级被改写一遍，而前端拿到的口径必须一致。
func (sum *RecordingSummary) normalizeSource() {
	if sum == nil {
		return
	}
	if sum.Source == "" {
		if strings.TrimSpace(sum.CreatedBy) == SourceBot {
			sum.Source, sum.SourceID, sum.SourceName = SourceBot, SourceDemoBot, SourceDemoName
		} else {
			sum.Source, sum.SourceName = SourceHuman, strings.TrimSpace(sum.CreatedBy)
		}
	}
	if sum.Source == SourceBot && sum.SourceName == "" {
		sum.SourceName = SourceDemoName
	}
}

// ---------------------------------------------------------------------------
// 自定义标签（v0.21.0）
// ---------------------------------------------------------------------------

const (
	// maxTagsPerRecord 是一笔交易最多几个标签。上限存在的意义和语音每根 9 条一样：
	// 标签是**记号**，不是分类树。一个人给一笔交易打十几个标签时，它们已经不再
	// 提供任何信息，只会让列表和日历挤成一团。
	maxTagsPerRecord = 8
	// maxTagNameRunes 按**字符**数算（不是字节）：中文标签四个字就该能表达清楚。
	maxTagNameRunes = 12
)

// normalizeTags 清洗一组用户标签：去空白、去重（同名不区分大小写）、限长、
// 颜色必须是 #rrggbb。**不排序** —— 用户打标签的顺序本身就是信息。
//
// 名字撞了时**后一个赢**：界面上的「点一下」是覆盖语义（同一个词换颜色），
// 前端因此可以只发「新的一组」，不必先查旧的那组里有没有同名词。
func normalizeTags(in []RecordTag) []RecordTag {
	if len(in) == 0 {
		return nil
	}
	out := make([]RecordTag, 0, len(in))
	seen := make(map[string]int, len(in))
	for _, t := range in {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		if n := []rune(name); len(n) > maxTagNameRunes {
			name = string(n[:maxTagNameRunes])
		}
		color := normalizeTagColor(t.Color)
		key := tagKey(name)
		if i, ok := seen[key]; ok {
			out[i] = RecordTag{Name: name, Color: color}
			continue
		}
		if len(out) >= maxTagsPerRecord {
			continue
		}
		seen[key] = len(out)
		out = append(out, RecordTag{Name: name, Color: color})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeTagColor 只认 #rrggbb。写错了给一个中性灰，而不是拒掉整次操作 ——
// 用户打标签是为了记事，不该因为颜色字符串少一位就把整件事拦住。
func normalizeTagColor(c string) string {
	c = strings.TrimSpace(c)
	if len(c) == 7 && c[0] == '#' {
		ok := true
		for i := 1; i < 7; i++ {
			b := c[i]
			if !(b >= '0' && b <= '9' || b >= 'a' && b <= 'f' || b >= 'A' && b <= 'F') {
				ok = false
				break
			}
		}
		if ok {
			return strings.ToLower(c)
		}
	}
	return defaultTagColor
}

// defaultTagColor 是「用户没挑颜色」时用的中性灰（写错颜色也落在这里）。
const defaultTagColor = "#64748b"

// tagKey 是标签的去重键（同名不区分大小写）。前端统计用量时按同一个口径数。
func tagKey(name string) string { return strings.ToLower(strings.TrimSpace(name)) }

// recordableAction 决定哪些广播会被记进交易记录。
//
// 只记**改盘面**的动作：步进、标注、挂单/成交。操作权、成员变化、机器人启停、
// 语音都不在这里 —— 语音会占一步，但那一步是由 addAudio 自己开的
// （见那里的注释），不走这条「一条广播 → 一步」的路。
//
// 注意 order_filled 两个来源都要记：市价单（下单即成交）和被触发的挂单，
// 回放端靠它把持仓、成交和盈亏更新到当时的样子。
func recordableAction(action string) bool {
	switch action {
	case ActBarAdvanced,
		ActAnnotationAdded, ActAnnotationMoved, ActAnnotationStyled, ActAnnotationDelete, ActAnnotationClear,
		ActOrderPlaced, ActOrderFilled, ActOrderMoved, ActOrderCancelled,
		// 一键退仓是一次真实操作（可能既撤单又平仓），回放时要占一步 ——
		// 否则「平仓那一下」在记录里凭空消失，那正是最该被看见的一步。
		ActFlatAll:
		return true
	}
	return false
}

// observe 把一条广播记成**新的一步**。
//
// v0.15.0 起不再有「开新段」和「追加到当前段」的区别：一条放行的广播就是一步。
// 这样「第 N 步」和「用户第 N 次动手」严格对应 —— 一根 K 线上画两个图形是两步，
// 先画图再录语音再画图也是四步，回放能一步一个动作地走。
//
// 为什么 1:1 是安全的：每个用户手势本来就只产生一条广播（画图在定稿时才发、
// 拖挂单松手才提交、点【下一根】时撮合结果就带在同一条 bar_advanced 的 Fills 里），
// 所以不存在「动作做了一半就被切成两步」的中间态。
//
// 统计（盈亏、最大不利/有利波动）在这里顺手算掉：payload 此刻还是**类型化的结构体**，
// 比落盘后从 JSON 里再解析一遍可靠得多（见 trade.go 的 statsTracker）。
//
// ctx 是「这条广播发生**之后**的盘面位置」（游标、当前 K 线时间），由 hub 从实例上取。
// 不能拿 r.cur：那是上一步的位置，bar_advanced 之后已经过时了。
func (r *tradeRecorder) observe(action, by string, payload any, ctx StepContext) {
	if r == nil || r.rec == nil {
		return
	}
	if !recordableAction(action) {
		return
	}
	r.stats.note(action, payload)

	ev := RecordingEvent{At: time.Now().Unix(), By: by, Action: action}
	if payload != nil {
		// 序列化失败（理论上不可能：payload 都是一个马上要广播出去的结构体）
		// 就记一条没有 payload 的事件 —— 比整段记录失败强。
		if raw, err := json.Marshal(payload); err == nil {
			ev.Payload = raw
		}
	}
	r.push(RecordingStep{
		At:     time.Now().UnixMilli(),
		Kind:   action,
		By:     by,
		Time:   ctx.Time,
		Cursor: ctx.Cursor,
		Events: []RecordingEvent{ev},
	})
}

// push 追加一步并维护步号 / cur。所有开步的地方都走它 —— 步号一旦算错，
// 「跳到第 N 步」就会演错，而且很难从界面上看出来。
func (r *tradeRecorder) push(st RecordingStep) {
	st.Step = len(r.rec.Steps) + 1
	if st.Time == "" {
		st.Time = r.cur.Time
	}
	r.rec.Steps = append(r.rec.Steps, st)
	// 步骤推进本身也算变化：检查点据此决定这一轮要不要写盘。
	r.dirty = true
	r.cur = StepContext{Time: st.Time, Cursor: st.Cursor}
}

// clipsOfBar 列出某一根 K 线上的语音（按录制顺序）。
//
// 语音的额度、列表、清空范围都按**这根 K 线**算 ——
// 一步一根 K 线的时候，「按步」和「按 K 线」是同一个东西，现在不是了。
func (r *tradeRecorder) clipsOfBar(barTime string) []AudioClip {
	out := make([]AudioClip, 0, 2)
	for _, a := range r.audios {
		if a.clip.Time == barTime {
			out = append(out, a.clip)
		}
	}
	return out
}

// baseBarTimes 是「Base 那一屏上有哪几根 K 线」的时间集合。
//
// 开仓前语音只认这个范围：base 之后那几根上的讲解不在这一屏快照里，
// 抄进来就会变成「回放里还没走到就先把话说了」。
func baseBarTimes(base RecordingBase) map[string]bool {
	out := make(map[string]bool, len(base.Bars))
	for _, b := range base.Bars {
		out[b.Time] = true
	}
	return out
}

// adoptPreOpenVoices 把「已经挂在盘面上、还没属于任何一笔交易记录」的语音抄进一条记录。
//
// 为什么必须抄：语音归实例（voice.go），录的时候 inst.trade 还是 nil —— 那时候只会写进
// 实例那一份。而回放的 Base 是**开仓之前那一屏**，里面已经有当时画的图形、挂的单子；
// 同一时刻盘面上的讲解却没跟着进来，回放里就成了「这根 K 线看得见、图形也在，唯独
// 讲解没了」。用户看到的正是这个（B116 在轴上、录音条上却没有它）。
//
// 三条规则：
//
//	只抄 baseTimes 里那几根 —— 之外的不在这一屏快照里（见 baseBarTimes）
//	Step 固定 0            —— 它们不对应任何一步，回放到某一步不会自动响，
//	                          只摆在录音条上等着被点
//	文件另存 audio-N.wav   —— 记录得能单独拷走，不能指到实例目录里去
//
// srcDir 是实例的音频目录（空 = 那份录音没落盘，读不到字节，只能跳过并记一行日志）；
// dstDir 是记录目录（空 = 只留内存，等 Save 时一起写）。
func adoptPreOpenVoices(clips []AudioClip, srcDir, dstDir string, baseTimes map[string]bool, seq int, logf func(string, ...any)) ([]AudioClip, []pendingAudio) {
	if len(clips) == 0 {
		return nil, nil
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if srcDir == "" {
		logf("[replay] 盘面上有 %d 条语音没有落盘，交易记录里抄不到它们（没配 state 目录）", len(clips))
		return nil, nil
	}
	out := make([]AudioClip, 0, len(clips))
	pend := make([]pendingAudio, 0, len(clips))
	for _, c := range clips {
		if !baseTimes[c.Time] {
			continue
		}
		data, err := os.ReadFile(filepath.Join(srcDir, c.File))
		if err != nil {
			logf("[replay] 开仓前的语音 %s 读不出来，记录里少了这一条：%v", c.File, err)
			continue
		}
		seq++
		dur := c.DurationMs
		if dur <= 0 {
			dur = int(float64(len(data)-44) / 32.0) // 16kHz / 16bit / 单声道
		}
		nc := AudioClip{
			ID:         fmt.Sprintf("au%d", seq),
			Step:       0,
			Time:       c.Time,
			DurationMs: dur,
			File:       fmt.Sprintf("audio-%d.wav", seq),
			Size:       int64(len(data)),
			CreatedAt:  c.CreatedAt,
			By:         c.By,
		}
		if dstDir != "" {
			if err := os.WriteFile(filepath.Join(dstDir, nc.File), data, 0o644); err != nil {
				logf("[replay] 开仓前的语音 %s 写不进记录目录（收尾时会重试）：%v", nc.File, err)
			}
		}
		out = append(out, nc)
		pend = append(pend, pendingAudio{clip: nc, data: data})
	}
	return out, pend
}

// maxAudioNo 从记录里已有的语音推出「下一个编号从哪儿开始」（id 是 au<N>）。
//
// 和 voice.go 的 maxClipSeq 同一个道理：按条数推会重号 —— 中间删过几条之后，
// 条数小于历史最大编号，两条语音就会抢同一个 audio-N.wav。
func maxAudioNo(clips []AudioClip) int {
	max := 0
	for _, c := range clips {
		n, err := strconv.Atoi(strings.TrimPrefix(c.ID, "au"))
		if err == nil && n > max {
			max = n
		}
	}
	return max
}

// addAudio 把一段 WAV 挂到**当前这根 K 线**上，并且**为它开一步**。
//
// 为什么语音要自己开一步（而不是像别的动作那样由 observe 开）：语音不是「一条广播
// 顺带产生的效果」，它本身就是一次操作 —— 用户按下空格、讲了一段、再按一下。
// 回放到这一步，画面不动、语音响，正好是当时发生的事。所以这里先建步、再广播，
// 广播带着已经包含这一步的盘面状态出去。
//
// 校验顺序是「先便宜后贵」：条数、长度、文件头。文件头必须是 RIFF/WAVE ——
// 这段音频之后会用 audio/wav 发给浏览器，收一个任意二进制进来等于开了一个
// 内容不明的本地文件服务。
//
// 条数上限按**这根 K 线**数：换到下一根 K 线，额度重新从 0 开始。
func (r *tradeRecorder) addAudio(data []byte, durationMs int, by string) (AudioClip, error) {
	if r == nil || r.rec == nil {
		return AudioClip{}, errors.New("这一笔交易还没有开始记录")
	}
	barTime := r.cur.Time
	if n := len(r.clipsOfBar(barTime)); n >= maxAudiosPerBar {
		return AudioClip{}, fmt.Errorf("一根 K 线最多 %d 段录音，这根已经录满了；点【下一根】可以接着录", maxAudiosPerBar)
	}
	// 长度 / 字节数 / 文件头的校验和盘面语音共用一份（见 voice.go 的 checkAudioClip），
	// 三处口径必须一样 —— 否则「复盘时能录、回放延伸时不能录」这种差异根本查不出来。
	durationMs, err := checkAudioClip(data, durationMs)
	if err != nil {
		return AudioClip{}, err
	}
	r.audioSeq++
	clip := AudioClip{
		ID:         fmt.Sprintf("au%d", r.audioSeq),
		Step:       len(r.rec.Steps) + 1, // 这一步马上就建，步号先占住
		Time:       barTime,
		DurationMs: durationMs,
		File:       fmt.Sprintf("audio-%d.wav", r.audioSeq),
		Size:       int64(len(data)),
		CreatedAt:  time.Now().Unix(),
		By:         by,
	}
	// 立刻写盘：语音浮窗的预览播放要走 HTTP，而 HTTP 只认磁盘。写失败不打断记录 ——
	// 内存里那份还在，收尾时 Save 会再补一次。
	if r.dir != "" {
		if err := os.WriteFile(filepath.Join(r.dir, clip.File), data, 0o644); err != nil {
			r.logf("[replay] 语音 %s 没能即时写盘（收尾时会重试）：%v", clip.File, err)
		}
	}
	r.audios = append(r.audios, pendingAudio{clip: clip, data: data})
	r.rec.Audios = append(r.rec.Audios, clip)
	audio := clip
	r.push(RecordingStep{
		At:     time.Now().UnixMilli(),
		Kind:   ActRecordingAudio,
		By:     by,
		Time:   barTime,
		Cursor: r.cur.Cursor,
		Audio:  &audio,
		Events: []RecordingEvent{},
	})
	return clip, nil
}

// clearAudioOfBar 删掉某一根 K 线上的全部语音，返回被删掉的清单。
//
// 范围是**一根 K 线**（需求原话是「一键清除当前 K 线的音频」），不是「当前这一步」——
// 一根 K 线上可能已经分几步录了好几段，说错了要重来不该只清最后一段。
//
// 每段语音都占一步，所以删语音 = 删步：交易还在进行时记录还没落定、外部也没引用过
// 步号，这里直接把那些步摘掉并把后面的步号**重排**。不这么做的话，回放时那几步
// 会变成「什么都不发生」的空档，用户会以为是卡了。
func (r *tradeRecorder) clearAudioOfBar(barTime string) []AudioClip {
	if r == nil || r.rec == nil {
		return []AudioClip{}
	}
	removed := r.clipsOfBar(barTime)
	if len(removed) == 0 {
		return []AudioClip{}
	}
	gone := make(map[string]bool, len(removed))
	for _, c := range removed {
		gone[c.ID] = true
		if r.dir != "" {
			if err := os.Remove(filepath.Join(r.dir, c.File)); err != nil && !os.IsNotExist(err) {
				r.logf("[replay] 语音 %s 删不掉：%v", c.File, err)
			}
		}
	}
	kept := r.audios[:0]
	for _, a := range r.audios {
		if gone[a.clip.ID] {
			continue
		}
		kept = append(kept, a)
	}
	r.audios = kept

	left := r.rec.Audios[:0]
	for _, a := range r.rec.Audios {
		if gone[a.ID] {
			continue
		}
		left = append(left, a)
	}
	r.rec.Audios = left

	// 摘掉对应的那几步（Kind = recording_audio 且 Audio 被删了）。
	steps := r.rec.Steps[:0]
	for _, st := range r.rec.Steps {
		if st.Kind == ActRecordingAudio && st.Audio != nil && gone[st.Audio.ID] {
			continue
		}
		steps = append(steps, st)
	}
	r.rec.Steps = steps
	r.renumber()
	r.dirty = true
	return removed
}

// renumber 在摘掉若干步之后把步号重新排成 1..N，并让 AudioClip.Step 跟着走。
//
// 三处必须同时改，漏一处就会「步进条上是 7 步、语音说自己在第 9 步」：
//
//	Steps[i].Step        步号本身
//	Steps[i].Audio.Step  语音步里那份 Audio 的副本
//	rec.Audios 里的正本   （以及 r.audios 里的待落盘副本）
func (r *tradeRecorder) renumber() {
	for i := range r.rec.Steps {
		st := &r.rec.Steps[i]
		st.Step = i + 1
		if st.Audio == nil {
			continue
		}
		st.Audio.Step = st.Step
		id := st.Audio.ID
		for j := range r.rec.Audios {
			if r.rec.Audios[j].ID == id {
				r.rec.Audios[j] = *st.Audio
			}
		}
		for j := range r.audios {
			if r.audios[j].clip.ID == id {
				r.audios[j].clip = *st.Audio
			}
		}
	}
}

// finish 收尾：封上结束时间，返回记录本体（语音字节另外交给 Save）。
//
// closed = 这一笔是否正常平完。false 只有三种来源：实例被删、程序退出、
// 进程崩溃留下的草稿 —— 记录本身仍然完整，只是这笔交易当时还没结束。
func (r *tradeRecorder) finish(closed bool) *Recording {
	rec := r.rec
	rec.EndedAt = time.Now().Unix()
	rec.Stats.Closed = closed
	r.takeStats()
	rec.refresh()
	return rec
}

// takeStats 把「边走边算」的统计交给记录本体（见 trade.go 的 statsTracker）。
//
// 必须在 refresh 之前调：refresh 只补 Steps（步数从 Steps 现算），其余字段
// （盈亏、MFE/MAE、进出场价）都在 tracker 里，不搬过来列表上就全是 0。
// 草稿也搬：崩掉之后列表里那一行至少得是「赚了多少」而不是空白。
func (r *tradeRecorder) takeStats() {
	closed := r.rec.Stats.Closed
	r.rec.Stats = r.stats.snapshot()
	r.rec.Stats.Closed = closed
}

// draft 取出「当前这份」用于检查点落盘：返回摘要和一个**独立可安全序列化**的副本。
//
// 调用方（checkpointTrades）在锁内调用并在锁内就 json.Marshal 完 —— 副本里的
// Steps / Base 切片仍是共享的，只有立即序列化才安全。这样每两秒的检查点
// 不会因为「复制几百根 K 线」而拖慢步进。
func (r *tradeRecorder) draft() (RecordingSummary, *Recording) {
	rec := r.rec
	// 草稿没有「结束时间」这个概念：EndedAt 记的是最后一次写盘的时刻，
	// 列表上据此显示「最后更新」。真正收尾时会被 finish 覆盖。
	rec.EndedAt = time.Now().Unix()
	r.takeStats()
	rec.refresh()
	cp := *rec
	return rec.RecordingSummary, &cp
}

// refresh 重新数一遍摘要字段（步数、操作数、根数、名字、统计里的步数）。
//
// 草稿和收尾都走它：列表里的「12 步 / 3 根」必须和文件里真正有几段是同一个数，
// 所以这些计数**从 Steps 现算**，不接受外部传进来的值。
func (rec *Recording) refresh() {
	recorded, bars := 0, 1
	for _, st := range rec.Steps {
		recorded += len(st.Events)
		if st.Kind == ActBarAdvanced || st.Kind == ActBarExtended {
			bars++
		}
	}
	rec.StepCount = len(rec.Steps)
	rec.AudioCount = len(rec.Audios)
	rec.EventCount = recorded
	// Bars 是「推进了几根 K 线」，**不是**步数：一根 K 线可能十几步，
	// 列表里要让人看得出「这段有多长」。
	//
	// v0.27.0 起把回放末尾「延伸加载」的 K 线也算进来：它们本来就是盘面上多走的根数，
	// 不算的话顶栏会写「第 9/6 根」—— 而步进条上明明有 9 格换根。
	rec.Bars = bars
	rec.Stats.Steps = len(rec.Steps)
	if n := len(rec.Steps); n > 0 {
		rec.EndTime = rec.Steps[n-1].Time
	}
	rec.Name = tradeName(rec)
}

// tradeName 给一条记录起一个「一眼能认出是哪一笔」的名字：
// 标的 + 方向手数 + 盈亏 + 进出时间。列表里不点开也知道这是哪一次交易。
func tradeName(rec *Recording) string {
	s := rec.Stats
	side := "—"
	switch {
	case s.Side == "long":
		side = "多"
	case s.Side == "short":
		side = "空"
	}
	entry := s.EntryTime
	if entry == "" {
		entry = rec.Anchor
	}
	tail := "持仓中"
	if s.Closed {
		tail = s.ExitTime
	}
	if tail == "" {
		tail = "—"
	}
	pnl := "—"
	if s.Fills > 0 {
		pnl = fmt.Sprintf("%+.2f", s.RealizedPnl)
	}
	return fmt.Sprintf("%s · %s%.0f 手 · %s · %s → %s", rec.Symbol, side, s.Quantity, pnl, entry, tail)
}

// ---------------------------------------------------------------------------
// 记录目录
// ---------------------------------------------------------------------------

// recordingTagsName 是标签文件的名字：**和 recording.json 分开存**。
//
// 为什么不把 Tags 写进 recording.json（它也带这个字段）：那个文件里是几百根 K 线
// 加每一步的事件，几百 KB 到几 MB。改一个标签就要重写一遍整条记录，既慢又多了
// 一次「写大文件写到一半崩掉」的机会 —— 而标签是几百字节的东西。分开之后：
//
//	tags.json 是标签的唯一事实，改标签只重写它
//	recording.json 里那份 Tags 只在 Save 时顺手带上（旧文件里没有也不影响）
//	scan / Load 都以 tags.json 为准（见 scanLockedTags）
//
// 代价是一个记录目录里多一个小文件，换来的是「打标签永远碰不到记录本体」。
const recordingTagsName = "tags.json"

// recordingTagsFile 是 tags.json 的结构（带版本，方便以后扩字段）。
type recordingTagsFile struct {
	Version int         `json:"version"`
	SavedAt int64       `json:"savedAt"`
	Tags    []RecordTag `json:"tags"`
}

// RecordingStore 是交易记录目录的读写入口。
//
// 列表走内存索引（每次 start/stop/delete 更新），回放才去读整份文件 ——
// 一条记录的 recording.json 里带着几百根 K 线，列表页没必要每次都读。
type RecordingStore struct {
	mu    sync.Mutex
	dir   string
	items []RecordingSummary
	// live 是「已经开始、还没提交」的记录 id。
	//
	// 进行中语音已经写进磁盘了（预览播放走 HTTP，HTTP 只认磁盘），
	// 而那一刻草稿可能还没写，所以不在索引里。AudioFile 得认这几个 id，
	// 否则「交易进行中点一下听听」会 404。
	live map[string]bool
	logf func(format string, args ...any)
}

type recordingsIndex struct {
	Version int                `json:"version"`
	SavedAt int64              `json:"savedAt"`
	Items   []RecordingSummary `json:"items"`
}

// OpenRecordingStore 打开（必要时创建）记录目录，并重建索引。
//
// dir 为空 = 不落盘（单元测试里常见）。这时记录仍然能在内存里跑完，
// 只是 Save 会返回一句明确的错误，而不是静默什么都没有。
func OpenRecordingStore(dir string, logf func(format string, args ...any)) (*RecordingStore, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	s := &RecordingStore{dir: strings.TrimSpace(dir), logf: logf, live: map[string]bool{}}
	if s.dir == "" {
		return s, nil
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return s, fmt.Errorf("创建交易记录目录 %s 失败：%w", s.dir, err)
	}
	s.items = s.scan()
	s.writeIndex()
	return s, nil
}

// Enabled 表示记录会不会落盘。
func (s *RecordingStore) Enabled() bool { return s != nil && s.dir != "" }

// Dir 是记录目录（空 = 没配置）。
func (s *RecordingStore) Dir() string {
	if s == nil {
		return ""
	}
	return s.dir
}

// List 返回记录列表（新的在前）。
func (s *RecordingStore) List() []RecordingSummary {
	if s == nil {
		return []RecordingSummary{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]RecordingSummary, len(s.items))
	copy(out, s.items)
	return out
}

// NewID 生成一个不会撞车的记录 id。
//
// 用「日期-时刻」而不是随机串：用户在文件夹里一眼能看出这笔是几点开的，
// 排序也天然正确。同一秒里连开两笔（几乎不可能，但测试会）就加 -2、-3。
func (s *RecordingStore) NewID(now time.Time) string {
	base := "rec-" + now.Format("20060102-150405")
	if s == nil {
		return base
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	taken := map[string]bool{}
	for _, it := range s.items {
		taken[it.ID] = true
	}
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		id := fmt.Sprintf("%s-%d", base, i)
		if !taken[id] {
			return id
		}
	}
}

// MarkLive 为「刚开始记录的这条」建好目录并登记成 live，返回目录路径。
//
// 记录的第一件事就是调它：语音要在录的当下写进这个目录，预览播放才拿得到。
// 目录建不出来不拦住记录（语音还能留在内存里），只是预览会取不到。
func (s *RecordingStore) MarkLive(id string) (string, error) {
	if s == nil || s.dir == "" || id == "" {
		return "", nil
	}
	folder := filepath.Join(s.dir, recordingFolder(id))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return "", fmt.Errorf("创建记录目录 %s 失败：%w", folder, err)
	}
	s.mu.Lock()
	if s.live == nil {
		s.live = map[string]bool{}
	}
	s.live[id] = true
	s.mu.Unlock()
	return folder, nil
}

// UnmarkLive 取消登记（记录保存完成，或者被放弃）。
func (s *RecordingStore) UnmarkLive(id string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	delete(s.live, id)
	s.mu.Unlock()
}

func (s *RecordingStore) isLive(id string) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.live[id]
}

// Drop 丢掉一条没保存成的记录目录（连语音一起）。
//
// 没有 recording.json 的文件夹在列表里看不见（scan 会跳过），留着只会变成
// 谁也认不出来的垃圾，所以保存失败就删掉 —— 内容本来也只在内存里，删掉的
// 不是「已经记好的东西」。
func (s *RecordingStore) Drop(id string) {
	if s == nil || s.dir == "" || id == "" {
		return
	}
	s.UnmarkLive(id)
	if err := os.RemoveAll(filepath.Join(s.dir, recordingFolder(id))); err != nil {
		s.logf("[replay] 记录目录 %s 清不掉：%v", id, err)
	}
}

// Save 把一条记好的记录写进 <dir>/<id>/（**按 id 覆盖**，不是追加）。
//
// 覆盖这一点很重要：交易进行中每两秒会写一次草稿（SaveDraft），收尾时同一条
// 记录会再写一次正式版 —— 列表里必须始终只有一行，而不是一行草稿加一行成品。
//
// 顺序：先写语音、再写 recording.json、最后更新索引。recording.json 是「这一条
// 记录的提交点」—— 它出现就代表这条记录完整可读；语音还没写完时它不会出现。
func (s *RecordingStore) Save(rec *Recording, audios []pendingAudio) (RecordingSummary, error) {
	if s == nil || s.dir == "" {
		return RecordingSummary{}, errors.New("没有配置交易记录目录（config.json 的 replayRecordingDir）")
	}
	if rec == nil {
		return RecordingSummary{}, errors.New("没有记录可保存")
	}
	folder := filepath.Join(s.dir, recordingFolder(rec.ID))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return RecordingSummary{}, err
	}
	// 标签是事后打的，只存在 tags.json 里（见 recordingTagsName）：收尾时手里这份
	// rec 是开仓那一刻建的，里面没有它们。不补这一步，每保存一次就把用户打过的标签
	// 从列表上抹一次 —— 而且看起来像是「标签没存住」。
	if len(rec.Tags) == 0 {
		rec.Tags = s.tagsOf(rec.ID)
	}
	keep := map[string]bool{}
	var size int64
	// 「该留哪些」以 **rec.Audios** 为准，而不是以 audios 参数为准。
	//
	// 这个区别很要紧：audios 参数只是「这次要写的字节」，有些调用方手里没有
	// 全部字节 —— 补抄开仓前语音（backfillPreOpenVoices）只带新增那几条，
	// 回放延伸（extend.go）一条都不带。按参数算「该留」就会把磁盘上本来好好的
	// 语音文件当残留删掉，表现是「只是打开了一下记录，语音全没了」。
	for _, a := range rec.Audios {
		keep[filepath.Base(a.File)] = true
	}
	for _, a := range audios {
		name := filepath.Base(a.clip.File)
		keep[name] = true
		path := filepath.Join(folder, name)
		// 记录中已经写过一次（预览播放要用），存在就不必重写；只有当时写失败
		// 的那种才在这里补上。
		if fi, err := os.Stat(path); err == nil && fi.Size() == a.clip.Size {
			size += fi.Size()
			continue
		}
		if err := os.WriteFile(path, a.data, 0o644); err != nil {
			return RecordingSummary{}, fmt.Errorf("写语音 %s 失败：%w", a.clip.File, err)
		}
		size += int64(len(a.data))
	}
	// 清掉没人引用的 audio-*.wav：记录中被「清空本根」删过的，或者上一次记录
	// 留下的残留 —— 它们会让「目录占用」和列表里的语音数对不上。
	if entries, err := os.ReadDir(folder); err == nil {
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || keep[n] || !strings.HasPrefix(n, "audio-") || !strings.HasSuffix(n, ".wav") {
				continue
			}
			if err := os.Remove(filepath.Join(folder, n)); err != nil {
				s.logf("[replay] 多余的语音 %s 删不掉：%v", n, err)
			}
		}
	}
	if err := writeJSONAtomic(filepath.Join(folder, recordingFileName), rec); err != nil {
		return RecordingSummary{}, fmt.Errorf("写 %s 失败：%w", recordingFileName, err)
	}
	if fi, err := os.Stat(filepath.Join(folder, recordingFileName)); err == nil {
		size += fi.Size()
	}
	sum := rec.RecordingSummary
	sum.Size = size
	sum.normalizeSource()

	s.mu.Lock()
	s.upsertLocked(sum)
	s.writeIndexLocked()
	delete(s.live, rec.ID) // 已提交：从此列表里能看见，AudioFile 走索引那道门
	s.mu.Unlock()
	return sum, nil
}

// SaveDraft 写一份「进行中」的检查点：只写 recording.json（语音早就落盘了），
// 然后把它按 id 覆盖进索引。
//
// data 是调用方在**锁内**序列化好的字节（见 checkpointTrades）：这样序列化
// 不可能和正在进行的步进抢同一份切片，而 IO 仍然留在锁外。
//
// 草稿和正式记录在列表里长得一样（同一行、同一个 id），区别只在 Stats.Closed ——
// 交易还没平完时它是 false，界面上显示「持仓中」。
func (s *RecordingStore) SaveDraft(sum RecordingSummary, data []byte) error {
	if s == nil || s.dir == "" || sum.ID == "" {
		return errors.New("没有配置交易记录目录")
	}
	folder := filepath.Join(s.dir, recordingFolder(sum.ID))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return err
	}
	path := filepath.Join(folder, recordingFileName)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return err
	}
	// 草稿也一样：标签在 tags.json 里，草稿的摘要里没有（见 Save 的同段注释）
	if len(sum.Tags) == 0 {
		sum.Tags = s.tagsOf(sum.ID)
	}
	sum.normalizeSource()
	sum.Size = folderSize(folder)
	s.mu.Lock()
	s.upsertLocked(sum)
	s.writeIndexLocked()
	s.mu.Unlock()
	return nil
}

// UpdateRecording 把**已经落过盘**的一条记录整份重写一遍（recording.json + 索引），
// 但**不碰音频目录**。
//
// 它和 Save 的区别只有一件事，而这件事很要紧：Save 会按 audios 参数清理
// 「没人引用的 audio-*.wav」。回放延伸（v0.27.0）手里没有音频字节（那些早就写在
// 记录目录里了），拿 Save 去保存就等于让清理逻辑把所有语音文件全删掉 ——
// 表现是「只是在回放里加载了一根 K 线，这条记录的语音全没了」。
//
// 所以两条路分工：
//
//	Save             交易收尾/检查点：写语音字节 + 清多余文件 + 写 JSON
//	UpdateRecording  事后修改结构（延伸的 K 线、回放里录的语音）：只写 JSON
//
// 语音文件由调用方自己写（见 WriteAudio）—— 写入是「多一个文件」，不需要清理。
func (s *RecordingStore) UpdateRecording(rec *Recording) (RecordingSummary, error) {
	if s == nil || s.dir == "" {
		return RecordingSummary{}, errors.New("没有配置交易记录目录（config.json 的 replayRecordingDir）")
	}
	if rec == nil || strings.TrimSpace(rec.ID) == "" {
		return RecordingSummary{}, errors.New("没有记录可保存")
	}
	folder := filepath.Join(s.dir, recordingFolder(rec.ID))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return RecordingSummary{}, err
	}
	// 标签只存在 tags.json 里（见 recordingTagsName），手里这份记录里没有它们
	if len(rec.Tags) == 0 {
		rec.Tags = s.tagsOf(rec.ID)
	}
	if err := writeJSONAtomic(filepath.Join(folder, recordingFileName), rec); err != nil {
		return RecordingSummary{}, fmt.Errorf("写 %s 失败：%w", recordingFileName, err)
	}
	sum := rec.RecordingSummary
	sum.normalizeSource()
	sum.Size = folderSize(folder)

	s.mu.Lock()
	s.upsertLocked(sum)
	s.writeIndexLocked()
	s.mu.Unlock()
	return sum, nil
}

// WriteAudio 往一条**已存在**的记录目录里写一个语音文件（v0.27.0）。
//
// 调用方是回放里「在延伸出来的 K 线上录音」那条路：那时没有进行中的 tradeRecorder，
// 没有 pendingAudio，只有一段刚编码好的 WAV 和一个该放它的文件名。
//
// 文件名校验和 AudioFile 完全一致（audio-N.wav、不含路径分隔符）—— 这条目录里的
// 文件之后会以 audio/wav 发给浏览器，名字必须是这里认得的形状。
// 名字重复时**覆盖**：编号由调用方按记录里已有的最大编号+1 算，正常不会撞上；
// 真撞上了（比如两个页面同时录），保留后写的那一份比留下一个谁也听不到的空号强。
func (s *RecordingStore) WriteAudio(id, name string, data []byte) error {
	if s == nil || s.dir == "" {
		return errors.New("没有配置交易记录目录（config.json 的 replayRecordingDir）")
	}
	if !s.has(id) {
		return fmt.Errorf("没有这条记录：%s", id)
	}
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return errors.New("录音文件名不合法")
	}
	if !strings.HasPrefix(name, "audio-") || !strings.HasSuffix(name, ".wav") {
		return errors.New("录音文件名不合法")
	}
	folder := filepath.Join(s.dir, recordingFolder(id))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return fmt.Errorf("记录目录不可用：%w", err)
	}
	if err := os.WriteFile(filepath.Join(folder, name), data, 0o644); err != nil {
		return fmt.Errorf("写语音 %s 失败：%w", name, err)
	}
	return nil
}

// upsertLocked 按 id 插入或替换一行摘要。调用方持有 s.mu。
func (s *RecordingStore) upsertLocked(sum RecordingSummary) {
	for i := range s.items {
		if s.items[i].ID == sum.ID {
			// 标签只存在 tags.json 里，收尾/草稿的摘要里一般没有它们 ——
			// 重写这一行时不能把索引里已有的标签一起抹掉（否则每次保存都会闪一下
			// 「标签没了」，直到下一次 SetTags 才回来）。
			if len(sum.Tags) == 0 {
				sum.Tags = s.items[i].Tags
			}
			s.items[i] = sum
			sortSummaries(s.items)
			return
		}
	}
	s.items = append(s.items, sum)
	sortSummaries(s.items)
}

// Load 读一条记录的全部内容。
//
// 旧格式（format<2）明确报错而不是「尽力读」：它的 Steps 是「一根 K 线一段」，
// 交给新播放器会得到一个步进条很短、每步却蹦出一堆变化的怪物 —— 与其让人
// 以为是回放坏了，不如直说这条记录该删。
func (s *RecordingStore) Load(id string) (*Recording, error) {
	if s == nil || s.dir == "" {
		return nil, errors.New("没有配置交易记录目录")
	}
	if !s.has(id) {
		// 索引里没有，但磁盘上可能是旧格式（scan 跳过了它）：给一句能对上号的解释
		var probe Recording
		if err := readJSONFile(filepath.Join(s.dir, recordingFolder(id), recordingFileName), &probe); err == nil &&
			probe.Format != RecordingFormat {
			return nil, fmt.Errorf("记录 %s 是旧格式（按 K 线分步），当前版本不再兼容；删掉 %s 下对应目录即可",
				id, s.dir)
		}
		return nil, fmt.Errorf("没有这条记录：%s", id)
	}
	var rec Recording
	path := filepath.Join(s.dir, recordingFolder(id), recordingFileName)
	if err := readJSONFile(path, &rec); err != nil {
		return nil, fmt.Errorf("读 %s 失败：%w", path, err)
	}
	if rec.Format != RecordingFormat {
		return nil, fmt.Errorf("记录 %s 是旧格式（按 K 线分步），当前版本不再兼容；删掉 %s 下对应目录即可",
			id, s.dir)
	}
	// v0.18.0 之前落盘的文件没有 stats：按事件流反推一份，
	// 否则列表/详情里那一笔交易的盈亏会是 0 —— 看起来像算错了。
	if rec.Stats.Fills == 0 && len(rec.Steps) > 1 {
		rec.Stats = deriveStats(&rec)
	}
	if rec.Name == "" {
		rec.Name = tradeName(&rec)
	}
	// 标签以 tags.json 为准；没有这个文件就用 recording.json 里那份（Save 时写进去的）
	if tags, ok := readRecordingTags(filepath.Join(s.dir, recordingFolder(id))); ok {
		rec.Tags = tags
	}
	rec.normalizeSource()
	return &rec, nil
}

// Delete 删掉一条记录（连文件夹一起）。**与实例存档完全无关**：
// 记录在 <recordingsDir>，实例在 <stateDir>，删一边不碰另一边。
func (s *RecordingStore) Delete(id string) error {
	if s == nil || s.dir == "" {
		return errors.New("没有配置交易记录目录")
	}
	if !s.has(id) {
		return fmt.Errorf("没有这条记录：%s", id)
	}
	s.mu.Lock()
	folder := filepath.Join(s.dir, recordingFolder(id))
	kept := s.items[:0]
	for _, it := range s.items {
		if it.ID != id {
			kept = append(kept, it)
		}
	}
	s.items = kept
	s.writeIndexLocked()
	s.mu.Unlock()
	if err := os.RemoveAll(folder); err != nil {
		return fmt.Errorf("目录删不掉：%w", err)
	}
	return nil
}

// SetTags 把一笔交易的标签**整份替换**成 tags（空 = 清空），返回更新后的那一行摘要。
//
// 写的是 <dir>/<id>/tags.json，不碰 recording.json（见 recordingTagsName 的说明）。
// 顺序是「先落盘、再改索引」：索引随时可以按目录重建，tags.json 才是标签的本体；
// 反过来先改索引的话，写盘失败时列表上会出现一组重启后就没了的标签。
func (s *RecordingStore) SetTags(id string, tags []RecordTag) (RecordingSummary, error) {
	if s == nil || s.dir == "" {
		return RecordingSummary{}, errors.New("没有配置交易记录目录（config.json 的 replayRecordingDir）")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return RecordingSummary{}, errors.New("没有指定是哪一条记录")
	}
	if !s.has(id) {
		return RecordingSummary{}, fmt.Errorf("没有这条记录：%s", id)
	}
	clean := normalizeTags(tags)
	folder := filepath.Join(s.dir, recordingFolder(id))
	if err := os.MkdirAll(folder, 0o755); err != nil {
		return RecordingSummary{}, fmt.Errorf("记录目录不可用：%w", err)
	}
	if err := writeJSONAtomic(filepath.Join(folder, recordingTagsName), recordingTagsFile{
		Version: 1,
		SavedAt: time.Now().Unix(),
		Tags:    clean,
	}); err != nil {
		return RecordingSummary{}, fmt.Errorf("标签没存下来：%w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.items {
		if s.items[i].ID != id {
			continue
		}
		s.items[i].Tags = clean
		sum := s.items[i]
		s.writeIndexLocked()
		return sum, nil
	}
	// has() 之后到这里说明索引刚被并发改过（删除）：标签文件已经写了，但那条记录没了。
	// 回一句错，让前端知道这次点击没有落到任何一条记录上。
	return RecordingSummary{}, fmt.Errorf("没有这条记录：%s", id)
}

// tagsOf 取一条记录当前的标签（索引里有就用索引里的）。调用方不要再持锁。
func (s *RecordingStore) tagsOf(id string) []RecordTag {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == id {
			return it.Tags
		}
	}
	return nil
}

// readRecordingTags 从记录目录里读标签。第二个返回值是「这个文件存在吗」：
//
//	(false) 没有 tags.json —— 老记录、从没打过标签；调用方该保留手里的那份
//	(true)  有，但 Tags 可能是空的 —— 用户把标签全删了，这是一个**有效状态**
//
// 两种情况必须分得开，否则「清空标签」会在下一次扫描时被 recording.json 里那份
// 旧的 Tags 又还原回来，看起来像删不掉。读失败按「没有文件」处理：标签是附加信息，
// 一个坏掉的 tags.json 不该让这条记录从列表里消失。
func readRecordingTags(folder string) ([]RecordTag, bool) {
	var f recordingTagsFile
	if err := readJSONFile(filepath.Join(folder, recordingTagsName), &f); err != nil {
		return nil, false
	}
	return normalizeTags(f.Tags), true
}


// AudioFile 返回某条记录里一段语音的磁盘路径。
//
// 两道门：id 必须在索引里**或者正在记录中**（记录时语音已经落地，用户要在
// 浮窗里预览自己刚录的那条），文件名必须是 audio-N.wav 且没有路径分隔符 ——
// 这个接口会把文件内容发到浏览器，不能让它读到目录外的东西。
func (s *RecordingStore) AudioFile(id, name string) (string, error) {
	if s == nil || s.dir == "" {
		return "", errors.New("没有配置交易记录目录")
	}
	if !s.has(id) && !s.isLive(id) {
		return "", fmt.Errorf("没有这条记录：%s", id)
	}
	if name == "" || name != filepath.Base(name) || strings.ContainsAny(name, `/\`) {
		return "", errors.New("录音文件名不合法")
	}
	if !strings.HasPrefix(name, "audio-") || !strings.HasSuffix(name, ".wav") {
		return "", errors.New("录音文件名不合法")
	}
	path := filepath.Join(s.dir, recordingFolder(id), name)
	if fi, err := os.Stat(path); err != nil || fi.IsDir() {
		return "", fmt.Errorf("录音不存在：%s", name)
	}
	return path, nil
}

func (s *RecordingStore) has(id string) bool {
	if id == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, it := range s.items {
		if it.ID == id {
			return true
		}
	}
	return false
}

// scan 只读地扫一遍目录，重建摘要列表（索引丢了也能恢复）。
//
// **只认当前格式**：Format 不是 RecordingFormat 的目录会被跳过 —— 旧格式的
// Steps 是「一根 K 线一段」，硬读出来会让步进条显示成另一回事（看着像回放坏了）。
// 跳过时给一句日志，用户知道那些目录可以删。
//
// 崩溃遗留的草稿也会被认出来（它有完整的 recording.json）：交易还没平完时
// Stats.Closed 是 false，列表里显示「持仓中」，不会当成「坏文件」丢掉。
func (s *RecordingStore) scan() []RecordingSummary {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		s.logf("[replay] 读交易记录目录 %s 失败：%v", s.dir, err)
		return nil
	}
	out := make([]RecordingSummary, 0, len(entries))
	stale := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		folder := filepath.Join(s.dir, e.Name())
		var rec Recording
		if err := readJSONFile(filepath.Join(folder, recordingFileName), &rec); err != nil {
			continue // 不是一条完整记录（或者正在写）：跳过，不打扰用户
		}
		if rec.Format != RecordingFormat {
			stale++
			continue
		}
		sum := rec.RecordingSummary
		if sum.ID == "" {
			sum.ID = e.Name()
		}
		sum.StepCount = len(rec.Steps)
		sum.AudioCount = len(rec.Audios)
		sum.EventCount = 0
		sum.Bars = 1
		for _, st := range rec.Steps {
			sum.EventCount += len(st.Events)
			if st.Kind == ActBarAdvanced || st.Kind == ActBarExtended {
				sum.Bars++
			}
		}
		if n := len(rec.Steps); n > 0 {
			sum.EndTime = rec.Steps[n-1].Time
		}
		if sum.Stats.Fills == 0 && len(rec.Steps) > 1 {
			sum.Stats = deriveStats(&rec)
		}
		if sum.Name == "" {
			sum.Name = tradeName(&rec)
		}
		// 标签以 tags.json 为准（recording.json 里那份可能是上一次 Save 时的快照）
		if tags, ok := readRecordingTags(folder); ok {
			sum.Tags = tags
		}
		sum.normalizeSource()
		sum.Size = folderSize(folder)
		out = append(out, sum)
	}
	if stale > 0 {
		s.logf("[replay] 跳过 %d 条旧格式记录（按 K 线分步，format<2）：版本升级后不再兼容，"+
			"需要的话直接删掉 %s 下对应目录即可", stale, s.dir)
	}
	sortSummaries(out)
	return out
}

func (s *RecordingStore) writeIndex() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeIndexLocked()
}

// writeIndexLocked 写索引。失败只打日志：记录文件本身才是事实，
// 下次启动 scan 会把列表重建出来。
func (s *RecordingStore) writeIndexLocked() {
	if s.dir == "" {
		return
	}
	idx := recordingsIndex{Version: recordingsVersion, SavedAt: time.Now().Unix(), Items: s.items}
	if err := writeJSONAtomic(filepath.Join(s.dir, recordingsIndexName), idx); err != nil {
		s.logf("[replay] 交易记录索引没写成：%v（下次启动会按目录重建）", err)
	}
}

// recordingFolder 把记录 id 变成安全的目录名。
func recordingFolder(id string) string { return instanceFolder(id) }

func sortSummaries(items []RecordingSummary) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].CreatedAt != items[j].CreatedAt {
			return items[i].CreatedAt > items[j].CreatedAt
		}
		return items[i].ID > items[j].ID
	})
}

func folderSize(dir string) int64 {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	var n int64
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !e.IsDir() {
			n += fi.Size()
		}
	}
	return n
}

// ---------------------------------------------------------------------------
// Hub 侧：开关、请求处理
// ---------------------------------------------------------------------------

// EnableRecordings 指定交易记录目录并读入已有列表。
//
// dir 为空 = 不落盘（单元测试）。返回的错误只影响「能不能保存」，
// 不影响服务起来 —— 目录不可写不该让整个复盘服务罢工。
func (h *Hub) EnableRecordings(dir string) error {
	st, err := OpenRecordingStore(dir, h.log)
	h.mu.Lock()
	h.records = st
	h.mu.Unlock()
	if err != nil {
		return err
	}
	if n := len(st.List()); n > 0 {
		h.log("[replay] 交易记录目录 %s 里有 %d 条记录", st.Dir(), n)
	}
	return nil
}

// Records 供 HTTP 层（/api/recordings/...）取语音路径。
func (h *Hub) Records() *RecordingStore {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.records
}

// handleRecordStart / handleRecordStop 是**已经取消的两个手动开关**。
//
// v0.18.0 起交易记录是自动的（开仓开始、平仓结束），没有「现在开始录」这个动作。
// 这两个消息仍然认，但只回一句明确的解释 —— 直接删掉会让老前端/老脚本收到
// 「不认识的消息类型」，那看起来像 bug，而不像「这个功能换了形态」。
func (h *Hub) handleRecordStart(c *Client, req Request) {
	h.fail(c, req, "交易记录已经改为自动：开仓时自动开始、平仓时自动结束，不需要手动开关")
}

func (h *Hub) handleRecordStop(c *Client, req Request) {
	h.fail(c, req, "交易记录已经改为自动：开仓时自动开始、平仓时自动结束，不需要手动开关")
}

// handleRecordAudio / handleRecordAudioDelete 是语音讲解的**旧入口别名**。
//
// v0.16.0 起语音有自己的入口（voice_add / voice_clear），v0.18.0 起连「必须在录制中」
// 这个附加条件也没有了（那个状态不存在）。所以这两个消息现在**就是**语音的两个入口：
// 不再单独维护一条录音路径，落盘、额度、清空范围全部走 voice.go 那一套。
func (h *Hub) handleRecordAudio(c *Client, req Request) {
	h.handleVoiceAdd(c, req)
}

func (h *Hub) handleRecordAudioDelete(c *Client, req Request) {
	h.handleVoiceClear(c, req)
}

func (h *Hub) handleListRecordings(c *Client) {
	h.reply(c, RecordingsMsg{Type: MsgRecordings, Dir: h.records.Dir(), Recordings: h.records.List()})
}

func (h *Hub) handleGetRecording(c *Client, req Request) {
	rec, err := h.records.Load(req.RecordingID)
	if err != nil {
		h.fail(c, req, err.Error())
		return
	}
	// 修复旧记录：那时候还没抄开仓前的语音（见 adoptPreOpenVoices），补一次并落盘
	h.backfillPreOpenVoices(rec)
	h.reply(c, RecordingMsg{Type: MsgRecording, Recording: rec})
}

// backfillPreOpenVoices 给**修复前录下的**记录补上开仓前那几根上的语音。
//
// 这些语音并没有丢：它们还在实例存档里（<stateDir>/instances/<id>/voice.json 和
// 同目录的 voice/），只是当时的记录没抄一份。用户在记录页打开这条记录时补一次，
// 只**追加**、不动任何一步，补完立刻重写 recording.json —— 下次打开不用再补。
//
// 两道保险：
//
//	只补「这根 K 线上记录里一条都没有」的。有几条的说明那是录制期间正常写进来的，
//	再抄一遍就会重复（一根 K 线上一鱼两吃，回放时听起来像说了两遍）
//	实例存档不在（记录被单独拷走、实例删了）就什么都不做 —— 记录本身仍然是完整的
func (h *Hub) backfillPreOpenVoices(rec *Recording) {
	if h == nil || h.records == nil || rec == nil || rec.InstanceID == "" || h.instanceDir == "" {
		return
	}
	if len(rec.Base.Bars) == 0 {
		return
	}
	// 语音清单优先问内存里的实例（它就是这份存档的主人）；实例不在内存里
	// （重启后没恢复、或者记录被拷到了另一台机器）再退回读 voice.json。
	// 光读盘会漏掉「刚录完、还没到落盘时机」的那几条。
	var clips []AudioClip
	srcDir := ""
	instDir := instanceDirPath(h.instanceDir, rec.InstanceID)
	if inst, ok := h.instances[rec.InstanceID]; ok && inst.voice != nil {
		clips = inst.voice.all()
		srcDir = inst.voice.dir
	} else {
		if err := readJSONFile(filepath.Join(instDir, voiceFileName), &clips); err != nil {
			return
		}
		srcDir = filepath.Join(instDir, voiceDirName)
	}
	if len(clips) == 0 || srcDir == "" {
		return
	}
	baseTimes := baseBarTimes(rec.Base)
	have := make(map[string]int, len(rec.Audios))
	for _, a := range rec.Audios {
		have[a.Time]++
	}
	cand := make([]AudioClip, 0, len(clips))
	for _, c := range clips {
		if baseTimes[c.Time] && have[c.Time] == 0 {
			cand = append(cand, c)
		}
	}
	if len(cand) == 0 {
		return
	}
	folder := filepath.Join(h.records.Dir(), recordingFolder(rec.ID))
	adopted, pend := adoptPreOpenVoices(cand, srcDir, folder,
		baseTimes, maxAudioNo(rec.Audios), h.log)
	if len(adopted) == 0 {
		return
	}
	rec.Audios = append(rec.Audios, adopted...)
	rec.AudioCount = len(rec.Audios)
	if _, err := h.records.Save(rec, pend); err != nil {
		// 补不进磁盘也不该让人打不开这条记录：内存里这一份仍然是补好的
		h.log("[replay] 记录 %s 补了 %d 条开仓前的语音，但没能落盘：%v", rec.ID, len(adopted), err)
		return
	}
	h.log("[replay] 记录 %s 补进 %d 条开仓前的语音（实例存档里还在）", rec.ID, len(adopted))
}

func (h *Hub) handleDeleteRecording(c *Client, req Request) {
	if err := h.records.Delete(req.RecordingID); err != nil {
		h.fail(c, req, err.Error())
		return
	}
	h.log("[replay] 交易记录 %s 已删除", req.RecordingID)
	h.reply(c, RecordingsMsg{Type: MsgRecordings, Dir: h.records.Dir(), Recordings: h.records.List()})
}

// handleSetRecordingTags 整份替换一笔交易的标签（v0.21.0）。
//
// 广播而不是只回给发起的那个连接：同一个人可能开着两个标签页（复盘页 + 记录页），
// 一边打了标签，另一边列表里的标签也得立刻跟着变。整份列表一起发最省事 ——
// 它本来就不大（一行一个摘要），而且前端拿到的永远是一份自洽的快照。
func (h *Hub) handleSetRecordingTags(c *Client, req Request) {
	if h.records == nil || !h.records.Enabled() {
		h.fail(c, req, "没有配置交易记录目录（config.json 的 replayRecordingDir），标签存不下来")
		return
	}
	sum, err := h.records.SetTags(req.RecordingID, req.Tags)
	if err != nil {
		h.fail(c, req, err.Error())
		return
	}
	if len(sum.Tags) == 0 {
		h.log("[replay] 交易记录 %s 的标签已清空", sum.ID)
	} else {
		names := make([]string, len(sum.Tags))
		for i, t := range sum.Tags {
			names[i] = t.Name
		}
		h.log("[replay] 交易记录 %s 的标签：%s", sum.ID, strings.Join(names, "、"))
	}
	h.broadcastRecordingsLocked()
}

// broadcastRecordingsLocked 把整份记录列表推给所有连接（标签变化后用）。
//
// 调用方必须已经持有 h.mu（这个名字里的 Locked 就是这个意思，和
// broadcastInstancesLocked 同一套约定）：Handle 的分发全程在锁内，
// 这里再抢一次锁会当场死锁。
func (h *Hub) broadcastRecordingsLocked() {
	if h.records == nil {
		return
	}
	data, err := json.Marshal(RecordingsMsg{
		Type:       MsgRecordings,
		Dir:        h.records.Dir(),
		Recordings: h.records.List(),
	})
	if err != nil {
		h.log("[replay] 交易记录列表序列化失败：%v", err)
		return
	}
	for _, c := range h.clients {
		h.writeLocked(c, data)
	}
}
