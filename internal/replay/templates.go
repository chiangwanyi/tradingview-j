package replay

// 标注模板：每个绘图工具（趋势线/射线/矩形/箭头线/斐波那契）下面挂一组样式预设，
// 存在 <presetDir>/replay-templates.json。
//
// 五个刻意的决定：
//
//  1. **模板是全局的，不属于某个实例**。模板描述的是「我习惯用什么颜色的线」，
//     跟画在哪个标的、哪个时间点无关；跟着实例走的话，开个新实例就得重建一遍。
//  2. **不和实例存档放在同一个目录**（v0.18.0 起）。实例存档是「一份练习的进度」，
//     可能整批删掉、也可能换 stateDir 并存好几套；模板是长期习惯，两者寿命不同。
//     早先它和实例存档同目录，删实例目录时容易连模板一起清掉。
//  3. **不跟实例存档同一个写盘节奏**。实例存档每两秒可能重写一次（步进、下单、标注），
//     模板一个月也改不了一次 —— 混在一起等于每次步进都在重写模板（见 persist.go）。
//  4. **一改就全量广播**。模板总共几十条，全量发送让前端不用维护增量合并；
//     这和挂单/成交全量发送是同一个理由。
//  5. **谁能改都行，不校验操作权**。这是台机器上的单机工具，模板是「本机偏好」，
//     不属于某个实例的操作 —— 操作权管的是「别两个人同时改同一张盘」。

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	templatesFileName = "replay-templates.json"
	templatesVersion  = 1
	// maxTemplatesPerKind 是单个工具的模板上限。列表是全量广播的，
	// 攒到几百条会开始拖慢每一次操作（以及每一次连上来的第一屏）。
	maxTemplatesPerKind = 20
)

// templateFile 是模板文件的整体形状。
type templateFile struct {
	Version   int        `json:"version"`
	NextID    int        `json:"nextId"` // 下一个模板编号；不存它会和旧模板撞号（tpl-3 撞 tpl-3）
	Templates []Template `json:"templates"`
}

// ---------------- 出厂模板 ----------------

// defaultTemplates 是第一次运行（还没有模板文件）时的预设：每个工具两条，
// 一实线一虚线、颜色不同。目的是**打开就能画**，不用先建模板。
func defaultTemplates() []Template {
	type preset struct {
		kind  string
		name  string
		color string
		dash  string
		width float64
	}
	presets := []preset{
		{AnnTrend, "金色实线", "#f59e0b", DashSolid, 1.6},
		{AnnTrend, "蓝色虚线", "#0ea5e9", DashDashed, 1.6},
		{AnnRay, "蓝色射线", "#0ea5e9", DashSolid, 1.6},
		{AnnRay, "金色虚线", "#f59e0b", DashDashed, 1.6},
		{AnnArrow, "粉色箭头", "#ec4899", DashSolid, 1.8},
		{AnnArrow, "绿色虚线箭头", "#22c55e", DashDashed, 1.8},
		{AnnRect, "金框", "#f59e0b", DashSolid, 1.4},
		{AnnRect, "蓝框", "#0ea5e9", DashSolid, 1.4},
		{AnnFib, "四色实线", "", DashSolid, 1.2},
		{AnnFib, "四色虚线", "", DashDashed, 1.2},
	}
	out := make([]Template, 0, len(presets))
	for i, p := range presets {
		style := defaultStyle(p.kind)
		if p.color != "" {
			style.Color = p.color
			if p.kind == AnnRect {
				style.Fill = p.color
			}
		}
		style.Dash = p.dash
		style.Width = p.width
		out = append(out, Template{
			ID:    fmt.Sprintf("tpl-%d", i+1),
			Kind:  p.kind,
			Name:  p.name,
			Style: style,
		})
	}
	return out
}

// ---------------- 存储 ----------------

// EnablePresets 指定「全局样式模板」目录并读入标注模板。
//
// 和 EnablePersistence 分开调用，是因为两者管的是不同的东西：
// 实例存档 = 一份练习的进度（可能被整批删掉，也可能指到别处并存好几套），
// 模板 = 长期习惯（换了标的、删了实例都还该在）。
// dir 为空 = 只留内存里的出厂模板，改了也不落盘（单元测试走这条路）。
//
// 必须在 EnablePersistence 之后调用：老位置的模板要从 statePath 推出来搬一次。
func (h *Hub) EnablePresets(dir string) error {
	dir = strings.TrimSpace(dir)

	h.mu.Lock()
	defer h.mu.Unlock()

	if dir == "" {
		// tplPath 留空 → writeTemplatesLocked 直接返回，改动只在本次进程有效
		h.tplPath = ""
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.tplPath = ""
		return fmt.Errorf("创建模板目录 %s 失败：%w", dir, err)
	}
	path := filepath.Join(dir, templatesFileName)
	h.migrateLegacyTemplatesLocked(path)
	h.loadTemplatesLocked(path)
	return nil
}

// migrateLegacyTemplatesLocked 把老位置（<stateDir>/replay-templates.json）的模板搬到新的模板目录。
//
// v0.18.0 之前模板和实例存档挤在同一个目录里。升级后第一件事不该是「模板全没了」，
// 所以这里静默搬一次；搬不动也只留一句日志，绝不因为模板影响服务起来。
// 调用方持有 h.mu。
func (h *Hub) migrateLegacyTemplatesLocked(newPath string) {
	if h.statePath == "" {
		return // 没启用实例存档，也就没有老位置可言
	}
	legacy := filepath.Join(filepath.Dir(h.statePath), templatesFileName)
	if legacy == newPath {
		return
	}
	if _, err := os.Stat(newPath); err == nil {
		// 新位置已经有文件了：**绝不覆盖** —— 那可能是用户在新目录里改过的。
		return
	}
	raw, err := os.ReadFile(legacy)
	if err != nil {
		return // 没有老文件是最常见的情况（新装）
	}
	// 用「写新 + 删旧」而不是 rename：模板目录可能被配到另一个盘上。
	if err := os.WriteFile(newPath, raw, 0o644); err != nil {
		h.log("[replay] 老模板文件 %s 搬不到 %s（%v），本次先用出厂模板，原文件保留不动", legacy, newPath, err)
		return
	}
	h.log("[replay] 模板已从 %s 搬到 %s（实例存档和模板现在分开存放）", legacy, newPath)
	if err := os.Remove(legacy); err != nil {
		h.log("[replay] 老模板文件 %s 删不掉（%v），留着不影响使用", legacy, err)
	}
}

// loadTemplatesLocked 读模板文件。调用方持有 h.mu。
//
// 读不了（坏 JSON、版本不对）**不删原文件**，只是这次用出厂模板 —— 和实例存档一个态度：
// 用户可能还想手工抢救它。
func (h *Hub) loadTemplatesLocked(path string) {
	h.tplPath = path
	h.templates = defaultTemplates()
	h.tplSeq = len(h.templates)
	if path == "" {
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			h.log("[replay] 模板文件 %s 读不了（%v），本次用出厂模板；原文件保留不动", path, err)
		}
		return
	}
	var f templateFile
	if err := json.Unmarshal(raw, &f); err != nil {
		h.log("[replay] 模板文件 %s 不是合法 JSON（%v），本次用出厂模板；原文件保留不动", path, err)
		return
	}
	if f.Version != templatesVersion {
		h.log("[replay] 模板文件版本是 %d，本程序只认 %d，本次用出厂模板", f.Version, templatesVersion)
		return
	}
	out := make([]Template, 0, len(f.Templates))
	for _, t := range f.Templates {
		if clean, err := sanitizeTemplate(t); err == nil {
			out = append(out, clean)
		}
	}
	if len(out) == 0 {
		return
	}
	h.templates = out
	h.tplSeq = nextTemplateSeq(out)
	if f.NextID > h.tplSeq {
		h.tplSeq = f.NextID
	}
}

// nextTemplateSeq 从现有 id（tpl-7）里算出最大编号，避免手改过的文件撞号。
func nextTemplateSeq(list []Template) int {
	max := 0
	for _, t := range list {
		if n, err := strconv.Atoi(strings.TrimPrefix(t.ID, "tpl-")); err == nil && n > max {
			max = n
		}
	}
	return max
}

// writeTemplatesLocked 整份写盘（tmp + rename）。没有配置状态目录就什么也不做。
func (h *Hub) writeTemplatesLocked() error {
	if h.tplPath == "" {
		return nil
	}
	f := templateFile{Version: templatesVersion, NextID: h.tplSeq, Templates: h.templateCopiesLocked()}
	buf, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化失败：%w", err)
	}
	tmp := h.tplPath + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, h.tplPath)
}

// templateCopiesLocked 复制一份给编码器用：按工具分组、组内按创建顺序，
// 前端拿到的顺序就是界面上显示的顺序。
func (h *Hub) templateCopiesLocked() []Template {
	out := make([]Template, 0, len(h.templates))
	for _, k := range drawKinds {
		var group []Template
		for _, t := range h.templates {
			if t.Kind == k {
				group = append(group, t)
			}
		}
		// 只按创建时间排（出厂模板的 CreatedAt 是 0，所以永远在最前面），
		// 时间相同时保持原顺序 —— 别按 id 字符串排：tpl-10 会跑到 tpl-2 前面。
		sort.SliceStable(group, func(i, j int) bool {
			return group[i].CreatedAt < group[j].CreatedAt
		})
		out = append(out, group...)
	}
	return out
}

// ---------------- 校验与增删 ----------------

// sanitizeTemplate 把模板夹进合法范围。名字空就用类型名，样式缺失的字段补出厂值。
func sanitizeTemplate(t Template) (Template, error) {
	if !isDrawKind(t.Kind) {
		return Template{}, fmt.Errorf("模板类型只能是 trend / ray / rect / arrow / fib，收到 %q", t.Kind)
	}
	name := truncateRunes(t.Name, maxNameRunes)
	if name == "" {
		name = kindLabel(t.Kind)
	}
	return Template{
		ID:        strings.TrimSpace(t.ID),
		Kind:      t.Kind,
		Name:      name,
		Style:     sanitizeStyle(t.Kind, t.Style),
		CreatedAt: t.CreatedAt,
	}, nil
}

// upsertTemplateLocked：ID 为空 = 新建，否则就地覆盖。调用方持有 h.mu。
func (h *Hub) upsertTemplateLocked(in Template) (Template, error) {
	t, err := sanitizeTemplate(in)
	if err != nil {
		return Template{}, err
	}
	if t.ID != "" {
		for i := range h.templates {
			if h.templates[i].ID == t.ID {
				t.CreatedAt = h.templates[i].CreatedAt // 创建时间不动：它决定列表顺序
				h.templates[i] = t
				return t, nil
			}
		}
		return Template{}, fmt.Errorf("没有这个模板：%s（可能已被别人删掉）", t.ID)
	}
	n := 0
	for _, old := range h.templates {
		if old.Kind == t.Kind {
			n++
		}
	}
	if n >= maxTemplatesPerKind {
		return Template{}, fmt.Errorf("%s的模板已经有 %d 个了，先删掉几个再新建", kindLabel(t.Kind), maxTemplatesPerKind)
	}
	h.tplSeq++
	t.ID = fmt.Sprintf("tpl-%d", h.tplSeq)
	t.CreatedAt = time.Now().Unix()
	h.templates = append(h.templates, t)
	return t, nil
}

// deleteTemplateLocked 删掉一条模板。已经画在图上的图形不受影响（样式是快照）。
func (h *Hub) deleteTemplateLocked(id string) error {
	for i, t := range h.templates {
		if t.ID != id {
			continue
		}
		h.templates = append(h.templates[:i], h.templates[i+1:]...)
		return nil
	}
	return fmt.Errorf("没有这个模板：%s（可能已被别人删掉）", id)
}

// ---------------- 消息处理 ----------------

func (h *Hub) handleListTemplates(c *Client) {
	h.reply(c, TemplatesMsg{Type: MsgTemplates, Templates: h.templateCopiesLocked()})
}

func (h *Hub) handleSaveTemplate(c *Client, req Request) {
	if req.Template == nil {
		h.fail(c, req, "缺少 template")
		return
	}
	t, err := h.upsertTemplateLocked(*req.Template)
	if err != nil {
		h.fail(c, req, err.Error())
		return
	}
	h.templatesChangedLocked(t.ID)
}

func (h *Hub) handleDeleteTemplate(c *Client, req Request) {
	id := strings.TrimSpace(req.TemplateID)
	if id == "" {
		h.fail(c, req, "缺少 templateId")
		return
	}
	if err := h.deleteTemplateLocked(id); err != nil {
		h.fail(c, req, err.Error())
		return
	}
	h.templatesChangedLocked("")
}

// templatesChangedLocked 存盘 + 广播整份列表（所有人都要立刻看到新模板）。
func (h *Hub) templatesChangedLocked(savedID string) {
	if err := h.writeTemplatesLocked(); err != nil {
		h.log("[replay] 模板没存下来：%v（本次改动只在这个进程里有效）", err)
	}
	data, err := json.Marshal(TemplatesMsg{
		Type:        MsgTemplates,
		Templates:   h.templateCopiesLocked(),
		LastSavedID: savedID,
	})
	if err != nil {
		h.log("[replay] 序列化模板失败：%v", err)
		return
	}
	for _, c := range h.clients {
		h.writeLocked(c, data)
	}
}
