/* ------------------------------------------------------------------
   TradingView 抓取工作台 —— 前端逻辑

   零构建：Vue 3 的 in-DOM 模板放 index.html，逻辑全在这里，
   样式走 Tailwind 浏览器版 + style.css。

   写 createApp 之前的那一段是纯函数，不依赖 DOM / Vue，
   可以直接在 node 里单测（test/ui-selftest.mjs 就是按这个约定切出来跑的）。
   ------------------------------------------------------------------ */
const { createApp } = Vue;

const WD_CN = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

/* ================= 格式化 ================= */

function fmtInt(n) {
  if (n == null || Number.isNaN(n)) return '—';
  return Number(n).toLocaleString('zh-CN');
}

function fmtBytes(n) {
  if (!n) return '0 B';
  if (n < 1024) return n + ' B';
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB';
  return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB';
}

function pad(n) {
  return String(n).padStart(2, '0');
}

/** 毫秒 -> "1小时23分" */
function fmtDur(ms) {
  if (!ms || ms < 0) return '0 秒';
  const s = Math.floor(ms / 1000);
  if (s < 60) return s + ' 秒';
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} 分 ${pad(s % 60)} 秒`;
  const h = Math.floor(m / 60);
  return `${h} 小时 ${pad(m % 60)} 分`;
}

/** 秒 -> "3.2 小时" */
function fmtDurShort(sec) {
  if (sec == null) return '—';
  if (sec < 60) return `${Math.round(sec)} 秒`;
  if (sec < 3600) return `${Math.round(sec / 60)} 分`;
  if (sec < 86400) return `${(sec / 3600).toFixed(1)} 小时`;
  return `${(sec / 86400).toFixed(1)} 天`;
}

/** 本地时间 HH:MM:SS */
function fmtClock(ts) {
  if (!ts) return '';
  const d = new Date(ts);
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** unix 秒 -> UTC "YYYY-MM-DD HH:MM"（行情时间一律按 UTC 讲，避免时区歧义） */
function fmtTs(t) {
  if (!t) return '—';
  return new Date(t * 1000).toISOString().replace('T', ' ').slice(0, 16);
}

function fmtPct(v) {
  if (v == null) return '—';
  return (v * 100).toFixed(1) + '%';
}

/* ================= 月历 ================= */

/**
 * 生成一个月的网格（周一为一周之首），补齐前后空位。
 * @param {string} month 'YYYY-MM'
 * @param {Map<string, object>} byIso day.iso -> 当天记录
 * @param {{fromIso:string,toIso:string}|null} range 有数据的日期范围
 * @returns {Array<object|null>} 长度是 7 的倍数
 */
function buildMonthGrid(month, byIso, range) {
  const [y, m] = String(month || '').split('-').map(Number);
  if (!y || !m) return [];
  const first = new Date(Date.UTC(y, m - 1, 1));
  const daysInMonth = new Date(Date.UTC(y, m, 0)).getUTCDate();
  // getUTCDay(): 0=周日。转成"周一=0"的排法
  const lead = (first.getUTCDay() + 6) % 7;

  const cells = [];
  for (let i = 0; i < lead; i++) cells.push(null);

  for (let d = 1; d <= daysInMonth; d++) {
    const iso = `${y}-${pad(m)}-${pad(d)}`;
    const rec = byIso.get(iso);
    if (rec) {
      cells.push({ ...rec, d });
    } else {
      const inRange = range && iso >= range.fromIso && iso <= range.toIso;
      cells.push({
        iso,
        d,
        n: 0,
        expected: 0,
        missing: 0,
        filledSlots: 0,
        missingSlots: 0,
        states: '',
        status: inRange ? 'absent' : 'out-of-range',
      });
    }
  }
  while (cells.length % 7) cells.push(null);
  return cells;
}

function statusText(s) {
  switch (s) {
    case 'full': return '完整';
    case 'gap': return '有缺口';
    case 'gap-big': return '缺口较大';
    case 'unreferenced-data': return '有数据（无参照）';
    case 'unknown': return '无参照';
    case 'absent': return '无数据';
    case 'out-of-range': return '不在范围内';
    default: return s || '—';
  }
}

function dayStatusText(c) {
  if (!c) return '';
  if (c.status === 'out-of-range') return '不在范围';
  if (c.status === 'absent') return '未抓取';
  if (c.status === 'unknown') return '无参照';
  if (c.n === 0) return '整日无数据';
  if (c.status === 'full') return '完整';
  if (c.status === 'unreferenced-data') return '无参照';
  return `缺 ${c.missing} 段`;
}

/** 当天"实有 / 应有"的百分比（用来画格子底部的小进度条） */
function dayRatio(c) {
  if (!c) return 0;
  if (!c.expected) return c.n > 0 ? 100 : 0;
  return Math.max(0, Math.min(100, Math.round((c.expected - c.missing) / c.expected * 100)));
}

/* ================= 时间轴矩阵布局（纯几何，可单测） ================= */

const MATRIX_DEFAULTS = { rowH: 9, histH: 46, gutter: 92, bottom: 20, gapY: 8, gapX: 8 };

/**
 * 算出画布尺寸与各区域位置。
 * bucketW 会被钳在 [1.4, 6]：太窄看不清，太宽一天拉得特别长。
 * 超出可用宽度就让容器横向滚动，不做压缩 —— 压缩会把格子压成亚像素，反而失真。
 */
function computeMatrixLayout({ rows, slotsPerDay, availW, opts = {} }) {
  const o = { ...MATRIX_DEFAULTS, ...opts };
  const usable = Math.max(120, availW - o.gutter - o.gapX);
  const bucketW = Math.max(1.4, Math.min(6, usable / Math.max(1, slotsPerDay)));
  const width = Math.ceil(o.gutter + slotsPerDay * bucketW + o.gapX);
  const matrixTop = o.histH + o.gapY;
  const height = Math.ceil(matrixTop + rows * o.rowH + o.bottom);
  return { ...o, bucketW, width, height, matrixTop, slotsPerDay, rows };
}

/** 画布坐标 -> 命中的格 / 直方图柱；没命中返回 null */
function matrixHitTest(x, y, layout) {
  if (!layout) return null;
  if (x < layout.gutter) return null;
  const slot = Math.floor((x - layout.gutter) / layout.bucketW);
  if (slot < 0 || slot >= layout.slotsPerDay) return null;
  if (y >= layout.matrixTop) {
    const row = Math.floor((y - layout.matrixTop) / layout.rowH);
    if (row < 0 || row >= layout.rows) return null;
    return { kind: 'cell', row, slot };
  }
  if (y >= 0 && y <= layout.histH) return { kind: 'hist', slot };
  return null;
}

/** 状态字符 -> 语义（与 src/calendar.mjs 保持一致） */
function slotState(ch) {
  switch (ch) {
    case '1': return { k: 'present', text: '有数据', expected: true };
    case '2': return { k: 'missing', text: '应有但缺', expected: true };
    case '3': return { k: 'extra', text: '有数据（无参照）', expected: false };
    default: return { k: 'off', text: '非交易时段', expected: false };
  }
}

/* ---------- 应用 ---------- */

createApp({
  data() {
    return {
      tab: 'control',
      theme: 'light',

      // SSE 连接状态
      sse: { cls: '', dot: '', text: '连接中…' },
      es: null,

      booted: false,
      state: {},
      session: { state: 'idle', counters: {}, settings: {}, stepLog: [] },
      settings: {},
      defaults: {},
      logs: [],
      autoScroll: true,

      // 设置表单（编辑中的副本）
      form: {},

      savingSettings: false,
      doctor: null,
      doctorLoading: false,

      // 品种
      symbols: [],
      registry: { symbols: [] },
      symbolsLoading: false,
      selectedSlug: '',

      // 覆盖视图
      cal: null,
      calLoading: false,
      windowDays: 0,
      calMonth: '',
      focusDay: null,
      tip: null,

      // 数据管理
      rawFiles: [],
      ingesting: false,
      ingestMsg: '',
      exporting: '',
      migrating: false,

      // 时间轴（每日覆盖条带）暂时隐藏 —— 目前看意义不大，先停用。
      // 关掉的是「画」这一环：canvas 不渲染，drawMatrix 因拿不到 ref 直接返回，
      // window.__tv.layout() 也就一直是 null。算覆盖日历、缺口、断档的逻辑一概不动，
      // 因为那些仍由月历 / 单日明细 / 缺口与断档表在用。
      showTimeline: false,

      _layout: null,
      _pendingCal: null,
    };
  },

  computed: {
    wdCN() { return WD_CN; },

    st() {
      const m = {
        idle: { cls: '', dot: '', text: '空闲' },
        connecting: { cls: 'badge-warn', dot: 'dot-warn', text: '连接中…' },
        running: { cls: 'badge-ok', dot: 'dot-accent', text: '运行中' },
        stopping: { cls: 'badge-warn', dot: 'dot-warn', text: '停止中…' },
        error: { cls: 'badge-bad', dot: 'dot-bad', text: '出错' },
      };
      return m[this.session.state] || m.idle;
    },

    statTiles() {
      const c = this.session.counters || {};
      return [
        { label: '本会话新增', value: fmtInt(this.session.addedBars), sub: `当前库内 ${fmtInt(this.session.barsNow)} 根`, color: this.session.addedBars ? 'var(--color-ok)' : null },
        { label: '收到帧', value: fmtInt(c.recv), sub: `发出 ${fmtInt(c.sent)}` },
        { label: '历史数据请求', value: fmtInt(c.moreDataRequests), sub: '页面发出的 request_more_data' },
        { label: '自动回拉步数', value: `${this.session.steps || 0} / ${this.session.settings?.maxSteps ?? '—'}`, sub: this.session.autoPull ? '进行中' : (this.session.autoPullStopReason ? this.autoStopText : '未开启') },
      ];
    },

    autoStopText() {
      const m = {
        budget: '已达步数上限',
        boundary: '已到可拉取边界',
        'hard-cap': '已达会话时限',
        'no-symbol': '未识别品种',
      };
      return m[this.session.autoPullStopReason] || '';
    },

    selectedSymbol() {
      return this.symbols.find((s) => s.slug === this.selectedSlug) || null;
    },

    // 运行时看会话的真实状态，没运行时看表单里的默认值
    autoPullOn() {
      return this.session.running ? Boolean(this.session.autoPull) : Boolean(this.form.autoPull);
    },

    registryLine() {
      const n = this.symbols.length;
      const total = this.symbols.reduce((a, s) => a + (s.bars || 0), 0);
      return n ? `${n} 个品种库 · 合计 ${fmtInt(total)} 根 K 线` : '还没有品种库';
    },

    chartTargetCount() {
      return (this.doctor?.targets || []).filter((t) => t.isChart).length;
    },

    coverageColor() {
      const r = this.cal?.summary?.coverageRatio;
      if (r == null) return 'var(--color-muted)';
      if (r >= 0.995) return 'var(--color-ok)';
      if (r >= 0.9) return 'var(--color-warn)';
      return 'var(--color-bad)';
    },

    calGrid() {
      if (!this.cal || !this.cal.days) return [];
      const byIso = new Map(this.cal.days.map((d) => [d.iso, d]));
      return buildMonthGrid(this.calMonth, byIso, this.cal.range);
    },

    calMonthLabel() {
      if (!this.calMonth) return '—';
      const [y, m] = this.calMonth.split('-');
      return `${y} 年 ${Number(m)} 月`;
    },

    ingestedCount() {
      return this.rawFiles.filter((f) => !f.pending).length;
    },

    fieldDefs() {
      return [
        { k: 'dx', label: '每步拖动像素', type: 'number', min: 200, max: 4000, step: 100, hint: '一步跨多远。太小要拖很多次；太大可能一步跨过缓存窗口，反而"看着没反应"' },
        { k: 'maxSteps', label: '最多步数', type: 'number', min: 1, max: 200, step: 1, hint: '本次会话自动回拉的步数上限，防止挂机跑一整夜' },
        { k: 'stopAfter', label: '连续无新增即停', type: 'number', min: 1, max: 20, step: 1, hint: '连续几步没有新 K 线，就认为到可拉取边界了' },
        { k: 'betweenSec', label: '每步间隔（秒）', type: 'number', min: 0.5, max: 60, step: 0.5, hint: '最直接的风控缓释手段，建议 ≥ 2 秒' },
        { k: 'quietSec', label: '数据静默判定（秒）', type: 'number', min: 0.5, max: 15, step: 0.5, hint: '页面安静这么久就认为这一轮数据到齐了' },
        { k: 'maxMinutes', label: '会话硬上限（分钟）', type: 'number', min: 1, max: 240, step: 5, hint: '超过就停下自动回拉（抓取本身继续）' },
        { k: 'symbol', label: '指定品种（留空自动识别）', type: 'text', runningLock: true, hint: '留空则从图表页 URL 的 ?symbol= 取' },
        { k: 'match', label: '匹配标签页', type: 'text', runningLock: true, hint: '默认抓 tradingview.com/chart 的页面' },
      ];
    },

    boolDefs() {
      return [
        { k: 'restoreView', label: '结束后恢复视图', hint: '把图表拨回原来的位置' },
        { k: 'hookWs', label: '安装页面内记录器', hint: '装了才能主动发帧（调试用）' },
        { k: 'reloadOnStart', label: '启动时刷新页面', hint: '会重建 WebSocket，但当前视图会丢' },
      ];
    },

    windowOptions() {
      return [
        { v: 0, t: '全部' },
        { v: 30, t: '30 天' },
        { v: 90, t: '90 天' },
        { v: 180, t: '180 天' },
      ];
    },
  },

  watch: {
    logs() {
      if (!this.autoScroll) return;
      this.$nextTick(() => {
        const el = this.$refs.logBox;
        if (el) el.scrollTop = el.scrollHeight;
      });
    },
  },

  methods: {
    // ---------- 格式化包装（模板里直接用） ----------
    fmtInt, fmtBytes, fmtDur, fmtDurShort, fmtClock, fmtTs, fmtPct,
    statusText, dayStatusText, dayRatio,

    wdCNOf(i) { return WD_CN[i] || ''; },

    cssVar(name, fallback = '') {
      const v = getComputedStyle(document.documentElement).getPropertyValue(name).trim();
      return v || fallback;
    },

    /**
     * canvas 要用的调色板。
     *
     * 必须带兜底：`getComputedStyle` 读一个**没被样式表输出**的自定义属性时静默返回空串，
     * 而 `ctx.fillStyle = ''` 也是静默忽略 —— 于是整段图形被跳过，画布上什么都不缺、就是空的，
     * 排查时非常难受（浅色模式下"有数据"的格子整片消失，就是这么来的）。
     * 所以这里：先取变量，取不到就用兜底色，同时报一条 console.error ——
     * e2e 里"控制台没有 error"那条断言会立刻把它揪出来。
     */
    canvasPalette() {
      const LIGHT = {
        accent: '#185fa5', bad: '#a32d2d', line: '#e3e1da', line2: '#cbc8bd',
        muted: '#6c695f', faint: '#9a978d', ok: '#0f6e56',
      };
      const DARK = {
        accent: '#6fb3f2', bad: '#ef8f8f', line: '#35342f', line2: '#47463f',
        muted: '#a29e93', faint: '#7a776d', ok: '#5cc9a5',
      };
      const fallback = document.documentElement.classList.contains('dark') ? DARK : LIGHT;
      const out = {};
      const missing = [];
      for (const k of Object.keys(LIGHT)) {
        const v = this.cssVar(`--color-${k}`);
        if (!v) missing.push(k);
        out[k] = v || fallback[k];
      }
      if (missing.length) {
        console.error(`主题变量缺失，已用兜底色代替：${missing.join('、')}（检查 @theme 是不是漏了 static）`);
      }
      return out;
    },

    // ---------- 启动 ----------
    async boot() {
      this.theme = document.documentElement.classList.contains('dark') ? 'dark' : 'light';
      await Promise.all([this.loadState(), this.loadSymbols(), this.loadRaw()]);
      this.connectSse();
      this.booted = true;
      requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(() => {
        const el = document.getElementById('app');
        if (el) el.classList.add('ready');
      }, 60)));
    },

    async loadState() {
      const r = await fetch('/api/state');
      const d = await r.json();
      this.state = d;
      this.applySession(d.session);
      this.settings = d.settings || {};
      this.defaults = d.defaults || {};
      this.form = { ...this.settings };
      if (!this.logs.length) this.logs = d.logs || [];
    },

    applySession(s) {
      if (!s) return;
      const wasRunning = this.session.running;
      this.session = s;
      // 刻意「不」把 s.settings 同步回 form：
      // state 事件每 2 秒来一次，同步会把用户正在改的输入框内容冲掉。
      // form 只在初始加载时对齐一次，之后由用户和「保存为默认」主导。
      if (wasRunning && !s.running) {
        // 会话结束：库和数据都变了，刷一遍
        this.loadSymbols();
        this.loadRaw();
        if (this.tab === 'coverage') this.loadCalendar();
      }
    },

    pushLog(l) {
      this.logs.push(l);
      if (this.logs.length > 600) this.logs.splice(0, this.logs.length - 600);
    },

    // ---------- SSE ----------
    connectSse() {
      if (this.es) {
        try { this.es.close(); } catch {}
      }
      const es = new EventSource('/api/events');
      this.es = es;

      es.addEventListener('hello', (e) => {
        const d = JSON.parse(e.data);
        this.applySession(d.session);
        if (d.logs && d.logs.length) {
          this.logs = d.logs;
        }
        this.setSseOk();
      });

      es.addEventListener('state', (e) => this.applySession(JSON.parse(e.data)));
      es.addEventListener('log', (e) => this.pushLog(JSON.parse(e.data)));

      es.addEventListener('step', (e) => {
        const d = JSON.parse(e.data);
        this.pushLog({ at: Date.now(), level: 'ok', text: `第 ${d.step} 步完成，累计 ${d.total} 根` });
        // 每步都刷新太重，节流一下
        clearTimeout(this._pendingCal);
        this._pendingCal = setTimeout(() => {
          if (this.tab === 'coverage') this.loadCalendar();
        }, 1200);
      });

      es.addEventListener('ingested', () => {
        this.loadSymbols();
        this.loadRaw();
        if (this.tab === 'coverage') this.loadCalendar();
      });

      es.addEventListener('bye', () => {
        this.sse = { cls: 'badge', dot: 'dot-bad', text: '服务已停止' };
      });

      es.onopen = () => this.setSseOk();
      es.onerror = () => {
        // EventSource 自己会重连，这里只更新提示
        this.sse = { cls: 'badge badge-warn', dot: 'dot-warn', text: '连接中断，重连中…' };
      };
    },

    setSseOk() {
      this.sse = { cls: 'badge badge-ok', dot: 'dot-ok', text: '实时连接正常' };
    },

    // ---------- 品种 ----------
    async loadSymbols() {
      this.symbolsLoading = true;
      try {
        const r = await fetch('/api/symbols');
        const d = await r.json();
        this.symbols = d.symbols || [];
        this.registry = d.registry || { symbols: [] };
        if (!this.selectedSlug && this.symbols.length) {
          this.selectedSlug = this.symbols[0].slug;
          this.calMonth = '';
        }
        // 选中的库可能已经不在了
        if (this.selectedSlug && !this.symbols.some((s) => s.slug === this.selectedSlug)) {
          this.selectedSlug = this.symbols[0]?.slug || '';
          this.cal = null;
        }
      } finally {
        this.symbolsLoading = false;
      }
    },

    onSymbolChange() {
      this.cal = null;
      this.calMonth = '';
      this.focusDay = null;
      if (this.tab === 'coverage') this.loadCalendar();
    },

    selectSymbol(slug) {
      this.selectedSlug = slug;
      this.cal = null;
      this.calMonth = '';
      this.focusDay = null;
      this.tab = 'coverage';
      this.$nextTick(() => this.loadCalendar());
    },

    switchCoverage() {
      this.tab = 'coverage';
      if (this.selectedSlug && !this.cal) this.loadCalendar();
      else this.$nextTick(() => this.drawMatrix());
    },

    switchData() {
      this.tab = 'data';
      this.loadSymbols();
      this.loadRaw();
    },

    // ---------- 覆盖视图 ----------
    async loadCalendar() {
      if (!this.selectedSlug) return;
      this.calLoading = true;
      try {
        const qs = new URLSearchParams();
        if (this.windowDays) qs.set('windowDays', String(this.windowDays));
        if (this.focusDay) qs.set('day', this.focusDay);
        const r = await fetch(`/api/symbols/${encodeURIComponent(this.selectedSlug)}/calendar?${qs}`);
        const d = await r.json();
        if (d.error) {
          this.cal = null;
          return;
        }
        this.cal = d;
        if (!this.calMonth && d.months && d.months.length) {
          this.calMonth = d.months[d.months.length - 1].month;
        }
        this.$nextTick(() => this.drawMatrix());
      } catch (e) {
        this.cal = null;
      } finally {
        this.calLoading = false;
      }
    },

    setWindow(v) {
      this.windowDays = v;
      this.loadCalendar();
    },

    focusOnDay(iso) {
      this.focusDay = iso;
      this.calMonth = iso.slice(0, 7);
      this.loadCalendar().then(() => {
        // 把对应那一行滚到可视区
        const wrap = this.$refs.matrixCanvas?.parentElement;
        if (!wrap || !this.cal || !this._layout) return;
        const idx = this.cal.days.findIndex((d) => d.iso === iso);
        if (idx < 0) return;
        wrap.scrollTop = Math.max(0, idx * this._layout.rowH - wrap.clientHeight / 2);
      });
    },

    shiftMonth(delta) {
      if (!this.calMonth) return;
      const [y, m] = this.calMonth.split('-').map(Number);
      const d = new Date(Date.UTC(y, m - 1 + delta, 1));
      this.calMonth = `${d.getUTCFullYear()}-${pad(d.getUTCMonth() + 1)}`;
    },

    gotoLatestMonth() {
      if (this.cal?.months?.length) this.calMonth = this.cal.months[this.cal.months.length - 1].month;
    },

    dayCellStyle(c) {
      const map = {
        full: { bg: 'var(--color-okbg)', bd: 'color-mix(in oklab, var(--color-ok) 45%, transparent)' },
        gap: { bg: 'var(--color-warnbg)', bd: 'color-mix(in oklab, var(--color-warn) 45%, transparent)' },
        'gap-big': { bg: 'var(--color-badbg)', bd: 'color-mix(in oklab, var(--color-bad) 45%, transparent)' },
        'unreferenced-data': { bg: 'var(--color-accentbg)', bd: 'color-mix(in oklab, var(--color-accent) 45%, transparent)' },
        unknown: { bg: 'var(--color-panel2)', bd: 'var(--color-line)' },
        absent: { bg: 'var(--color-panel)', bd: 'var(--color-line)' },
        'out-of-range': { bg: 'transparent', bd: 'var(--color-line)' },
      };
      const s = map[c.status] || map.absent;
      return { background: s.bg, borderColor: s.bd };
    },

    ratioPct(c) {
      return dayRatio(c) + '%';
    },

    dayTitle(c) {
      if (!c) return '';
      const parts = [`${c.iso} ${WD_CN[c.weekday] || ''}`, `${c.n} 根`];
      if (c.expected) parts.push(`应有 ${c.expected}｜缺 ${c.missing}`);
      else parts.push('无参照（该星期几样本不足）');
      return parts.join(' · ');
    },

    // ---------- 画时间轴 ----------
    drawMatrix() {
      const cv = this.$refs.matrixCanvas;
      if (!cv || !this.cal || this.cal.empty || !this.cal.days || !this.cal.days.length) return;
      const wrap = cv.parentElement;
      const availW = Math.max(420, wrap.clientWidth - 2);
      const layout = computeMatrixLayout({
        rows: this.cal.days.length,
        slotsPerDay: this.cal.slotsPerDay,
        availW,
      });
      this._layout = layout;

      const dpr = Math.min(3, window.devicePixelRatio || 1);
      cv.width = Math.round(layout.width * dpr);
      cv.height = Math.round(layout.height * dpr);
      cv.style.width = layout.width + 'px';
      cv.style.height = layout.height + 'px';
      const ctx = cv.getContext('2d');
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, layout.width, layout.height);

      const C = this.canvasPalette();
      const days = this.cal.days;
      const slots = this.cal.slotsPerDay;
      const bw = layout.bucketW;
      const total = days.length;

      // ---- 顶部直方图：每个时段在多少天里有数据 ----
      const hist = this.cal.histogram || [];
      const maxDays = Math.max(1, ...hist.map((h) => h.expect || 0), ...hist.map((h) => h.days || 0));
      const histTop = layout.histH;
      // "应该有多少天"的底衬
      ctx.fillStyle = C.line;
      for (let i = 0; i < hist.length; i++) {
        const want = hist[i].expect || 0;
        if (!want) continue;
        const h = Math.max(1, (want / maxDays) * (layout.histH - 6));
        ctx.fillRect(layout.gutter + i * bw, histTop - h, Math.max(1, bw - 0.15), h);
      }
      // "实际有多少天"
      ctx.fillStyle = C.accent;
      for (let i = 0; i < hist.length; i++) {
        const got = hist[i].days || 0;
        if (!got) continue;
        const h = Math.max(1, (got / maxDays) * (layout.histH - 6));
        ctx.fillRect(layout.gutter + i * bw, histTop - h, Math.max(1, bw - 0.15), h);
      }
      ctx.fillStyle = C.line2;
      ctx.fillRect(layout.gutter, histTop, slots * bw, 1);

      ctx.font = '10px system-ui, -apple-system, sans-serif';
      ctx.textAlign = 'right';
      ctx.textBaseline = 'top';
      ctx.fillStyle = C.faint;
      ctx.fillText('每时段覆盖天数', layout.gutter - 8, 0);
      ctx.fillText(`0 – ${maxDays} 天`, layout.gutter - 8, 13);

      // ---- 矩阵：把连续同色的格子合成一个矩形，避免几万次 fillRect ----
      //
      // 用 globalAlpha 表达"淡一点"，不用 CSS 的 color-mix()。
      // color-mix() 是 CSS 颜色 5 的能力，canvas 的 fillStyle 解析能不能吃它取决于浏览器版本，
      // 而这份数据里当时一根"应有但缺"的格子都没有 —— 也就是那条分支根本没被验证过。
      // globalAlpha 是所有 canvas 实现都有的老能力，不赌。
      const colorFor = (day, ch) => {
        if (ch === '1') return { color: C.accent, alpha: 1 };
        if (ch === '2') return { color: C.bad, alpha: 0.62 };
        if (ch === '3') return { color: C.accent, alpha: 0.45 };
        return { color: C.line, alpha: 1 };
      };

      for (let r = 0; r < total; r++) {
        const day = days[r];
        const y = layout.matrixTop + r * layout.rowH;
        const h = Math.max(1, layout.rowH - 1);
        const states = day.states || '';
        // 同字符必然同色同透明度，所以直接比字符就够了 ——
        // 不去比颜色对象，就不会卷进"对象引用 / 空字符串是假值"这类坑里
        let runStart = 0;
        let runCh = states[0];
        for (let i = 1; i <= slots; i++) {
          const ch = i < slots ? states[i] : null;
          if (ch !== runCh) {
            if (i > runStart) {
              const c = colorFor(day, runCh);
              ctx.fillStyle = c.color;
              ctx.globalAlpha = c.alpha;
              ctx.fillRect(layout.gutter + runStart * bw, y, (i - runStart) * bw - 0.2, h);
            }
            runStart = i;
            runCh = ch;
          }
        }
        ctx.globalAlpha = 1;
      }
      ctx.globalAlpha = 1;

      // ---- 小时刻度 ----
      ctx.save();
      ctx.strokeStyle = C.line2;
      ctx.globalAlpha = 0.5;
      ctx.beginPath();
      for (let hh = 0; hh <= 24; hh++) {
        const x = Math.round(layout.gutter + (hh * slots / 24) * bw) + 0.5;
        ctx.moveTo(x, layout.matrixTop);
        ctx.lineTo(x, layout.matrixTop + total * layout.rowH);
      }
      ctx.stroke();
      ctx.restore();

      // 底部小时标签（每 6 小时一个，避免挤）
      ctx.fillStyle = C.faint;
      ctx.textAlign = 'center';
      ctx.textBaseline = 'top';
      const axisY = layout.matrixTop + total * layout.rowH + 4;
      for (let hh = 0; hh <= 24; hh += 6) {
        const x = layout.gutter + (hh / 24) * slots * bw;
        ctx.fillText(`${pad(hh % 24)}:00`, Math.min(x, layout.width - 14), axisY);
      }

      // ---- 日期标签 + 月份分隔线 ----
      ctx.textAlign = 'right';
      ctx.textBaseline = 'middle';
      ctx.font = '10px system-ui, -apple-system, sans-serif';
      let lastMonth = '';
      for (let r = 0; r < total; r++) {
        const day = days[r];
        const y = layout.matrixTop + r * layout.rowH + layout.rowH / 2;
        const month = day.iso.slice(0, 7);

        if (month !== lastMonth) {
          lastMonth = month;
          ctx.save();
          ctx.strokeStyle = C.line2;
          ctx.beginPath();
          const ly = Math.round(layout.matrixTop + r * layout.rowH) + 0.5;
          ctx.moveTo(0, ly);
          ctx.lineTo(layout.width - 4, ly);
          ctx.stroke();
          ctx.restore();
        }

        const dd = Number(day.iso.slice(8, 10));
        const isMonthStart = dd === 1;
        const isMajor = dd % 5 === 1 || isMonthStart || r === 0 || r === total - 1;
        if (isMajor) {
          ctx.fillStyle = isMonthStart ? C.muted : C.faint;
          ctx.font = isMonthStart
            ? '600 10px system-ui, -apple-system, sans-serif'
            : '10px system-ui, -apple-system, sans-serif';
          ctx.fillText(isMonthStart ? day.iso.slice(0, 7) : day.iso, layout.gutter - 8, y);
        }
      }
    },

    onMatrixMove(ev) {
      if (!this._layout || !this.cal) return;
      const hit = matrixHitTest(ev.offsetX, ev.offsetY, this._layout);
      if (!hit) {
        this.tip = null;
        return;
      }
      const slot = this.cal.histogram?.[hit.slot];
      if (hit.kind === 'hist') {
        this.tip = {
          x: ev.offsetX,
          y: ev.offsetY,
          text: `${slot?.label || ''} · 该时段 ${slot?.days ?? 0} 天有数据 / ${slot?.expect ?? 0} 天应有`,
        };
        return;
      }
      const day = this.cal.days[hit.row];
      if (!day) {
        this.tip = null;
        return;
      }
      const st = slotState(day.states?.[hit.slot] || '0');
      this.tip = {
        x: ev.offsetX,
        y: ev.offsetY,
        text: `${day.iso} ${WD_CN[day.weekday]} ${slot?.label || ''} · ${st.text}`,
      };
    },

    onMatrixClick(ev) {
      if (!this._layout || !this.cal) return;
      const hit = matrixHitTest(ev.offsetX, ev.offsetY, this._layout);
      if (!hit || hit.kind !== 'cell') return;
      const day = this.cal.days[hit.row];
      if (day) this.focusOnDay(day.iso);
    },

    // ---------- 会话控制 ----------
    async startCapture() {
      const payload = { ...this.form };
      // 表单里 digit 字段可能是字符串（用户边输边改），统一成数字
      payload.autoPull = Boolean(payload.autoPull);
      for (const f of this.fieldDefs) {
        if (f.type === 'number') payload[f.k] = Number(payload[f.k]);
      }
      this.logs = [];
      await fetch('/api/session/start', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      this.loadState();
    },

    async stopCapture() {
      await fetch('/api/session/stop', { method: 'POST' });
      this.loadState();
    },

    async toggleAutoPull() {
      if (!this.session.running) {
        // 未运行时只是在改默认值
        this.form.autoPull = !this.form.autoPull;
        return;
      }
      const on = !this.session.autoPull;
      await fetch('/api/session/autopull', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ on }),
      });
      this.form.autoPull = on;
    },

    async saveSettings() {
      this.savingSettings = true;
      try {
        const r = await fetch('/api/settings', {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(this.form),
        });
        const d = await r.json();
        this.settings = d.settings;
        this.pushLog({ at: Date.now(), level: 'ok', text: '设置已保存' });
      } finally {
        this.savingSettings = false;
      }
    },

    // ---------- 诊断 ----------
    async runDoctor() {
      this.doctorLoading = true;
      try {
        const r = await fetch('/api/doctor');
        this.doctor = await r.json();
      } finally {
        this.doctorLoading = false;
      }
    },

    // ---------- 数据管理 ----------
    async loadRaw() {
      const r = await fetch('/api/raw');
      const d = await r.json();
      this.rawFiles = d.files || [];
    },

    async doIngest(all) {
      this.ingesting = true;
      this.ingestMsg = '';
      try {
        const r = await fetch('/api/ingest', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ all: Boolean(all) }),
        });
        const d = await r.json();
        this.ingestMsg = d.started
          ? `已开始入库 ${d.files} 个文件（共 ${d.total} 个）—— 进度见「抓取控制」的日志`
          : d.message;
        if (d.started) {
          // 后台在跑，等一会儿再刷新
          setTimeout(() => { this.loadSymbols(); this.loadRaw(); this.ingesting = false; }, 1500);
          return;
        }
      } catch (e) {
        this.ingestMsg = '入库请求失败：' + e.message;
      }
      this.ingesting = false;
    },

    async exportCsv(s) {
      this.exporting = s.slug;
      try {
        const r = await fetch(`/api/symbols/${encodeURIComponent(s.slug)}/export`, { method: 'POST' });
        const d = await r.json();
        if (d.error) {
          this.pushLog({ at: Date.now(), level: 'error', text: '导出失败：' + d.error });
          return;
        }
        const blob = new Blob([d.csv], { type: 'text/csv;charset=utf-8' });
        const url = URL.createObjectURL(blob);
        const a = document.createElement('a');
        a.href = url;
        a.download = d.filename;
        document.body.appendChild(a);
        a.click();
        document.body.removeChild(a);
        setTimeout(() => URL.revokeObjectURL(url), 4000);
        this.pushLog({
          at: Date.now(),
          level: 'ok',
          text: `导出 ${d.rows} 行 → ${d.filename}` + (d.collapsed ? `（收敛掉 ${d.collapsed} 行重复）` : ''),
        });
      } finally {
        this.exporting = '';
      }
    },

    async doMigrate() {
      this.migrating = true;
      try {
        await fetch('/api/migrate', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({}),
        });
        await this.loadSymbols();
        await this.loadState();
      } finally {
        this.migrating = false;
      }
    },

    // ---------- 主题 ----------
    toggleTheme() {
      this.theme = this.theme === 'dark' ? 'light' : 'dark';
      document.documentElement.classList.toggle('dark', this.theme === 'dark');
      try { localStorage.setItem('tv-theme', this.theme); } catch {}
      this.$nextTick(() => this.drawMatrix());
    },
  },

  mounted() {
    // 给端到端测试留的钩子。
    //
    // 为什么不直接从 DOM 反推：时间轴画在 canvas 上，格子是在位图里一个个 fillRect 出来的，
    // DOM 里没有任何元素能告诉你 gutter / rowH / bucketW 是多少。测试要逐格采样像素，
    // 就必须拿到这份布局；把它重新抄一份进测试，等于多一处会漂的副本 ——
    // 哪天改了 MATRIX_DEFAULTS，测试要么假绿要么假红。
    window.__tv = { app: this, layout: () => this._layout, computeMatrixLayout };
    this.boot();
    window.addEventListener('resize', () => {
      clearTimeout(this._rz);
      this._rz = setTimeout(() => this.drawMatrix(), 160);
    });
  },
}).mount('#app');
