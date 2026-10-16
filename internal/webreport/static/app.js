// 网页版「分析已抓取的数据」的前端逻辑。
//
// 这里**不计算任何统计量**：根数、缺口、断档、坏行全部由 Go 侧的 store.Scan 算好，
// 通过 /api/analysis 原样送过来。前端只做两件事：格式化显示、按日历摆放。
// 之所以这么切，是因为统计口径一旦有两份实现（Go 一份、JS 一份），迟早会出现
// 「终端说这一天 288 根、网页说 287 根」而这种矛盾用户没法自查。
//
// ↓↓↓ 这一段是纯函数区（不依赖 Vue），可以被 node 直接 eval 出来断言，
//     见 internal/webreport/static/test/format.test.cjs。下面的期望值刻意和
//     report_test.go 里的逐条对齐 —— 两边都改才算改对。

/* ---------- 纯函数：格式化 ---------- */

// num 加千分位：10822 -> 10,822。和 analyze.go 的 comma() 同口径。
function num(n) {
  if (n === null || n === undefined || n === '') return '0';
  const v = Number(n);
  if (isNaN(v)) return '0';
  const s = String(Math.round(v));
  const sign = s.charAt(0) === '-' ? '-' : '';
  const d = sign ? s.slice(1) : s;
  let out = '';
  for (let i = 0; i < d.length; i++) {
    if (i > 0 && (d.length - i) % 3 === 0) out += ',';
    out += d.charAt(i);
  }
  return sign + out;
}

// bytes 走人和机器都顺眼的单位切换，和 analyze.go 的 humanBytes() 同口径。
function bytes(n) {
  const v = Number(n) || 0;
  if (v < 1024) return num(v) + ' B';
  if (v < 1024 * 1024) return (v / 1024).toFixed(1) + ' KB';
  return (v / 1048576).toFixed(1) + ' MB';
}

// gap 把秒数说成人话：3900 -> 1时5分。和 analyze.go 的 gapText() 同口径。
function gap(sec) {
  const s = Math.round(Number(sec) || 0);
  if (s < 60) return s + '秒';
  if (s < 3600) return Math.floor(s / 60) + '分';
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  return m === 0 ? h + '时' : h + '时' + m + '分';
}

function pad2(n) { return n < 10 ? '0' + n : String(n); }

// clock / stamp 把 Unix 秒按**浏览器本地时区**格式化。
// 服务端和浏览器在同一台机器上，所以这里得到的就是 Go 侧 time.Local 的那一套；
// 「哪一天」的划分早在扫描时就定死了（日期取自文件名），这里只负责显示。
function clock(unix) {
  const d = new Date(unix * 1000);
  return pad2(d.getHours()) + ':' + pad2(d.getMinutes());
}

function stamp(unix) {
  const d = new Date(unix * 1000);
  return d.getFullYear() + '-' + pad2(d.getMonth() + 1) + '-' + pad2(d.getDate()) +
    ' ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes());
}

/* ---------- 纯函数：日历 ---------- */

// 一周从**周一**排起（中文习惯），所以表头是「一二三四五六日」，
// 而不是 Date 自带的「日一二三四五六」。
const WEEKDAYS = ['一', '二', '三', '四', '五', '六', '日'];
const WD_CN = ['周日', '周一', '周二', '周三', '周四', '周五', '周六'];

// monthOf 取月份：'2026-10-16' -> '2026-10'。传进来的本来就是月份时原样返回。
function monthOf(date) { return String(date || '').slice(0, 7); }

// isMonth 判断是不是一个合法的月份键。月份要卡在 01–12：
// 光看「四个数字-两个数字」的话，用户在搜索框里敲的 "2026-13" 也会被当成月份，
// 日历会翻到一个不存在的月份上，格子里一片空白而没有任何解释。
function isMonth(s) { return /^\d{4}-(0[1-9]|1[0-2])$/.test(String(s || '')); }

// addMonth 前后翻月：('2026-01', -1) -> '2025-12'。
// 跨年、闰月一律交给 Date 去算，不自己进位 —— 自己写日历迟早会在 2 月和 12 月出岔子。
function addMonth(ym, delta) {
  const d = new Date(Number(ym.slice(0, 4)), Number(ym.slice(5, 7)) - 1 + Number(delta || 0), 1);
  return d.getFullYear() + '-' + pad2(d.getMonth() + 1);
}

// todayMonth 本机当前月份。日历在「一条数据都没有」时也得有个月份可显示。
function todayMonth() {
  const d = new Date();
  return d.getFullYear() + '-' + pad2(d.getMonth() + 1);
}

// monthCells 铺一个月的格子：前面按星期补空、末尾补满整周。
// 补位格子的 date 是空串，模板据此渲染成不可点的空格。
function monthCells(ym) {
  if (!isMonth(ym)) return [];
  const y = Number(ym.slice(0, 4)), m = Number(ym.slice(5, 7));
  const last = new Date(y, m, 0).getDate();              // 下个月第 0 天 = 本月最后一天
  const lead = (new Date(y, m - 1, 1).getDay() + 6) % 7; // Date 里周日是 0，这里要周一为 0
  const cells = [];
  for (let i = 0; i < lead; i++) cells.push({ date: '', day: 0 });
  for (let i = 1; i <= last; i++) cells.push({ date: ym + '-' + pad2(i), day: i });
  while (cells.length % 7 !== 0) cells.push({ date: '', day: 0 });
  return cells;
}

// weekday 中文星期：'2026-10-16' -> '周五'。
function weekday(date) {
  const p = String(date || '').split('-');
  if (p.length !== 3) return '';
  return WD_CN[new Date(Number(p[0]), Number(p[1]) - 1, Number(p[2])).getDay()];
}

// monthsOf 把一批日期收成「出现过的月份」，倒序（最近的排最前）。
function monthsOf(dates) {
  const seen = {}, out = [];
  for (let i = 0; i < dates.length; i++) {
    const m = monthOf(dates[i]);
    if (isMonth(m) && !seen[m]) { seen[m] = 1; out.push(m); }
  }
  out.sort();
  out.reverse();
  return out;
}

/* ---------- 纯函数：筛选与排序 ---------- */

// dayIssue：「异常」= 坏行 / 重复行 / 整份读不了。
// 缺口不算异常 —— 期货每日结算休市、周末休市都会稳定留下缺口，那是正常形态。
function dayIssue(d) {
  return !!(d.err || d.skipped || d.dupes);
}

// 只有纯数字/连字符的查询词才当成日期（"2026-10"），否则当成标的名（"MES"）。
// 不然搜「MES」会把所有日期都滤没，表格空得莫名其妙。
function isDateQuery(q) {
  return /^[0-9][0-9\-\/.]*$/.test(q || '');
}

const NUMERIC_SORTS = { bars: 1, missing: 1, maxGap: 1, bytes: 1 };

// sortFiles 返回排好序的副本；同值时按日期升序兜底，避免刷新一次顺序就变。
function sortFiles(files, key, dir) {
  const arr = files.slice();
  const numeric = !!NUMERIC_SORTS[key];
  arr.sort(function (a, b) {
    let r;
    if (numeric) {
      r = (Number(a[key]) || 0) - (Number(b[key]) || 0);
    } else {
      const x = String(a[key]), y = String(b[key]);
      r = x === y ? 0 : (x < y ? -1 : 1);
    }
    if (r === 0) return a.date < b.date ? -1 : (a.date > b.date ? 1 : 0);
    return r * dir;
  });
  return arr;
}

function sumOf(files) {
  const out = { bars: 0, missing: 0, bytes: 0 };
  for (let i = 0; i < files.length; i++) {
    out.bars += Number(files[i].bars) || 0;
    out.missing += Number(files[i].missing) || 0;
    out.bytes += Number(files[i].bytes) || 0;
  }
  return out;
}

// alertsOf 把「值得单独提醒一句」的情况收成一列。
// 数据目录不存在、读不了某份文件这类事实由 Go 侧的 store.Scan 记在 notes 里，原样透传。
function alertsOf(rep) {
  const out = [];
  const notes = (rep && rep.notes) || [];
  for (let i = 0; i < notes.length; i++) out.push({ kind: 'warn', text: notes[i] });
  const t = (rep && rep.totals) || {};
  if (t.skipped > 0 || t.dupes > 0) {
    out.push({
      kind: 'bad',
      text: '合计有 ' + num(t.skipped) + ' 行解析不了、' + num(t.dupes) + ' 行时间戳重复' +
        '（见日历上标红的日期）。',
    });
  }
  return out;
}

/* ---------- 应用 ---------- */

const { createApp } = Vue;

// 类名写成字面量映射表：Tailwind 的浏览器版靠扫描 DOM 发现类名，
// 运行期拼出来的 'alert-' + kind 它认不出来。具体长相在 style.css 里。
const KIND_CLS = {
  warn: 'alert-warn',
  bad: 'alert-bad',
};

// 日历格子：每个标的的日历和「全部标的合计」的日历长得一模一样，只有喂进来的
// cells 不同，所以做成一个组件，模板只写一份 —— 模板文本放在 index.html 的
// <template id="tv-cal-tpl"> 里（**在 #app 之外**：放进 #app 会被当成页面模板编译）。
//
// 组件模板里刻意一个 Tailwind 工具类都不用，全走 style.css 的 .cal-*：
// 这段模板是运行期字符串，Tailwind 的浏览器版是扫 DOM 生成样式的，
// 运行期才出现的类名有漏扫的风险，而普通 CSS 的类是编译期就写死的。
//
// 用 innerHTML 取模板需要带编译器的 Vue（vendor 里的 vue.global.prod.js 是完整版）。
function templateText(id) {
  const el = document.getElementById(id);
  if (!el || !el.content) return '';
  // <template> 的子节点放在 .content 这个 DocumentFragment 里，元素本身没有子节点。
  // 现代浏览器里 el.innerHTML 会特判模板、把 content 序列化出来，但这条路我不打算赌：
  // 真取空了，页面上就是「日历整块不见」而控制台一声不吭。所以为空时再用一个临时
  // div 把 content 克隆出来读一次 —— 两条路总能走通一条。
  if (el.innerHTML) return el.innerHTML;
  const box = document.createElement('div');
  box.appendChild(el.content.cloneNode(true));
  return box.innerHTML;
}

const CAL_TPL = templateText('tv-cal-tpl');

const CalGrid = {
  props: { cells: { type: Array, default: function () { return []; } } },
  emits: ['pick'],
  data() { return { wd: WEEKDAYS }; },
  methods: {
    num,
    cls(c) {
      return {
        'cal-has': !!c.have,
        'cal-issue': !!(c.have && c.bad),
        'cal-sel': !!(c.have && c.sel),
        'cal-dim': !!(c.have && c.dim),
      };
    },
    tip(c) {
      if (!c.date) return '';
      const head = c.date + ' ' + weekday(c.date);
      if (!c.have) return head + ' · 没有数据';
      return head + ' · ' + num(c.bars) + ' 根' + (c.sub ? ' · ' + c.sub : '');
    },
  },
  template: CAL_TPL,
};

const app = createApp({
  data() {
    return {
      rep: null,
      loading: false,
      err: '',
      q: '',
      hlIssue: false,   // 高亮异常日（剩下的变淡，不隐藏 —— 格子里「没了」会让人以为那天没数据）
      hlGap: false,     // 高亮有缺口的日
      month: '',        // 日历月份，所有日历共用；翻一次到处都跟着翻
      monthTouched: false, // 用户自己翻过月份后，重新扫描不再把月份抢回最新
      picked: {},       // 标的目录 -> 选中的那一天的 Day 对象
      gPicked: '',      // 全部标的合计那张日历选中的日期
      dark: false,
      allOpen: true,
      open: {}, // folder -> 是否展开；没记过的用 allOpen
    };
  },

  computed: {
    // 模板用 rep 本身判断「读到没有」—— 不再另设一个 ready 标志。
    // 一开始我就是这么干的，结果忘了在拿到数据时把标志置上，页面永远停在「正在读取…」，
    // 而接口、单测、DOM 里都能看到数据（只有真跑浏览器才暴露）。少一个可漂移的状态。
    meta() {
      return (this.rep && this.rep.meta) || { root: '', step: 300, tz: '', version: '' };
    },
    totals() { return (this.rep && this.rep.totals) || {}; },
    span() { return this.rep && this.rep.span ? this.rep.span : null; },
    folders() { return (this.rep && this.rep.folders) || []; },
    byDateAll() { return (this.rep && this.rep.byDate) || []; },
    alerts() {
      return alertsOf(this.rep).map(function (a) {
        return { text: a.text, cls: KIND_CLS[a.kind] };
      });
    },
    // 查询词是日期时才拿它过日期，否则只当标的名 —— 见 isDateQuery 的注释
    qIsDate() { return isDateQuery(this.q); },
    // 日期类筛选（而不是「按标的名找」）打开时，工具条才需要报命中数
    dayFilterOn() { return !!(this.qIsDate || this.hlIssue || this.hlGap); },
    hits() {
      const self = this;
      let days = 0, bars = 0;
      this.folders.forEach(function (f) {
        (f.files || []).forEach(function (d) {
          if (self.dayPass(d)) { days++; bars += Number(d.bars) || 0; }
        });
      });
      return { days: days, bars: bars };
    },
    // 标的名查询才过滤标的；日期类筛选留着卡片，只把日历上不符合的日变淡
    shownFolders() {
      const self = this;
      return this.folders.filter(function (f) { return self.folderMatches(f); });
    },

    months() { return monthsOf(this.byDateAll.map(function (r) { return r.date; })); },
    // 月份还没定（首屏）时退回最新有数据的月份；一条数据都没有就用本机当月
    curMonth() { return isMonth(this.month) ? this.month : (this.months[0] || todayMonth()); },
    monthOptions() {
      const m = this.months.slice();
      if (m.indexOf(this.curMonth) < 0) m.push(this.curMonth);
      m.sort();
      m.reverse();
      return m;
    },
    gRow() {
      const date = this.gPicked;
      if (!date) return null;
      const rows = this.byDateAll;
      for (let i = 0; i < rows.length; i++) if (rows[i].date === date) return rows[i];
      return null;
    },
    snapshot() {
      if (!this.rep) return '';
      const g = this.rep.meta.generatedAt;
      if (!g) return '';
      const d = new Date(g * 1000);
      return '快照 ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + ':' + pad2(d.getSeconds());
    },
  },

  watch: {
    // 在搜索框里敲一个月份（"2026-10"）就把日历翻过去 —— 敲了半天月份还停在原地会很奇怪。
    // 只有真的构成月份才翻（"2026" 不翻），否则每敲一个数字日历都在乱跳。
    q(v) {
      const m = monthOf(v);
      if (this.qIsDate && isMonth(m)) this.month = m;
    },
  },

  methods: {
    // ---- 格式化（模板里直接用，函数本体在纯函数区） ----
    num, bytes, gap, clock, stamp, weekday,

    rowIssue(r) { return dayIssue(r); },
    sameName(f) { return (f.symbol || '').toLowerCase() === (f.folder || '').toLowerCase(); },

    // one 把「有或没有」的一个对象包成长度 0/1 的数组：模板里 `v-for="d in one(x)"`
    // 就能给这个对象起个别名，省掉二十处 `pickedDay(f).bars` 这样的重复取值。
    one(x) { return x ? [x] : []; },

    // ---- 折叠 ----
    isOpen(folder) {
      return this.open[folder] === undefined ? this.allOpen : this.open[folder];
    },
    toggleFolder(folder) { this.open[folder] = !this.isOpen(folder); },

    // ---- 筛选 ----
    folderMatches(f) {
      if (!this.q) return true;
      if (this.qIsDate) return true; // 日期查询由日历上的变淡来表示
      const q = this.q.toLowerCase();
      return (f.symbol || '').toLowerCase().indexOf(q) >= 0 ||
        (f.folder || '').toLowerCase().indexOf(q) >= 0;
    },
    // dayPass 是「这个日文件算不算命中了筛选」。日历不隐藏格子（会让人以为那天没数据），
    // 而是把没命中的变淡；命中数报在工具条上。
    dayPass(d) {
      if (!d) return false;
      if (this.qIsDate && d.date.indexOf(this.q) < 0) return false;
      if (this.hlIssue && !dayIssue(d)) return false;
      if (this.hlGap && !(Number(d.missing) > 0)) return false;
      return true;
    },
    filesOf(f) {
      const self = this;
      return sortFiles((f.files || []).filter(function (d) { return self.dayPass(d); }), 'date', 1);
    },
    sumOf(f) { return sumOf(this.filesOf(f)); },

    // ---- 日历 ----
    // 格子的描述对象由这里拼好：日期、根数、一行小字、是否标红/变淡/选中。
    // 数字全部来自 Go 送的字段，这里只挑「显示哪一个」。
    dayOf(f, date) {
      const list = f.files || [];
      for (let i = 0; i < list.length; i++) if (list[i].date === date) return list[i];
      return null;
    },
    cellSub(d) {
      if (!d) return '';
      if (dayIssue(d)) return '异常';
      return Number(d.missing) > 0 ? '缺 ' + num(d.missing) : '';
    },
    cellsOf(f) {
      const self = this;
      const sel = this.picked[f.folder];
      return monthCells(this.curMonth).map(function (c) {
        const d = c.date ? self.dayOf(f, c.date) : null;
        return {
          date: c.date, day: c.day,
          have: !!d,
          bars: d ? Number(d.bars) || 0 : 0,
          bad: !!(d && dayIssue(d)),
          sub: self.cellSub(d),
          dim: !!(d && !self.dayPass(d)),
          sel: !!(d && sel && sel.date === c.date),
        };
      });
    },
    gCells() {
      const self = this;
      const map = {};
      this.byDateAll.forEach(function (r) { map[r.date] = r; });
      return monthCells(this.curMonth).map(function (c) {
        const r = c.date ? map[c.date] : null;
        const have = !!r;
        return {
          date: c.date, day: c.day,
          have: have,
          bars: have ? Number(r.bars) || 0 : 0,
          sub: have ? num(r.symbols) + ' 标的' : '',
          // 有异常的日子标红。异常标的数是 Go 的 ByDate 算的（见 store.ByDate），
          // 前端不自己扫一遍文件去凑 —— 那又是一份口径。
          bad: !!(have && Number(r.issues) > 0),
          dim: !!(have && ((self.qIsDate && r.date.indexOf(self.q) < 0) ||
            (self.hlIssue && !(Number(r.issues) > 0)))),
          sel: !!(have && self.gPicked === c.date),
        };
      });
    },
    monthHasData(f) {
      const m = this.curMonth;
      const list = f.files || [];
      for (let i = 0; i < list.length; i++) {
        if (monthOf(list[i].date) === m && (Number(list[i].bars) || 0) > 0) return true;
      }
      return false;
    },

    // ---- 月份翻页 ----
    stepMonth(delta) {
      this.monthTouched = true;
      this.month = addMonth(this.curMonth, delta);
    },
    gotoLatest() {
      this.monthTouched = true;
      this.month = this.months[0] || todayMonth();
    },
    markMonthTouched() { this.monthTouched = true; },

    // ---- 点选某一天 ----
    pickDay(f, cell) {
      if (!cell || !cell.date) return;
      const cur = this.picked[f.folder];
      // 点已经选中的那天 = 收起（空对象在 Vue 3 里是响应式的，赋值是安全的）
      this.picked[f.folder] = cur && cur.date === cell.date ? null : this.dayOf(f, cell.date);
    },
    pickedDay(f) { return this.picked[f.folder] || null; },
    pickGlobal(cell) {
      if (!cell || !cell.date) return;
      this.gPicked = this.gPicked === cell.date ? '' : cell.date;
    },
    // 这一天每个标的各抓了多少 —— 就是把各标的当天那条记录挑出来，不重算任何东西
    symbolsOnDate(date) {
      const out = [];
      this.folders.forEach(function (f) {
        const list = f.files || [];
        for (let i = 0; i < list.length; i++) {
          if (list[i].date !== date) continue;
          const d = list[i];
          out.push({
            folder: f.folder, symbol: f.symbol,
            date: d.date, bars: d.bars, rows: d.rows, skipped: d.skipped, dupes: d.dupes,
            firstT: d.firstT, lastT: d.lastT, span: d.span, missing: d.missing,
            maxGap: d.maxGap, bytes: d.bytes, err: d.err || '',
          });
          break;
        }
      });
      return sortFiles(out, 'bars', -1);
    },

    // ---- 数据 ----
    load() {
      const self = this;
      self.loading = true;
      fetch('/api/analysis', { cache: 'no-store' })
        .then(function (r) {
          if (!r.ok) throw new Error('HTTP ' + r.status);
          return r.json();
        })
        .then(function (j) {
          self.rep = j;
          self.err = '';
          // 快照换了，选中的那天/那个标的可能已经不存在了，一律清掉重选，
          // 免得右侧明细停在上一份快照的数据上
          self.picked = {};
          self.gPicked = '';
          if (!self.monthTouched) self.month = self.months[0] || todayMonth();
        })
        .catch(function (e) {
          self.err = e && e.message ? e.message : String(e);
        })
        .then(function () { self.loading = false; });
    },

    toggleDark() {
      this.dark = !this.dark;
      document.documentElement.classList.toggle('dark', this.dark);
      try { localStorage.setItem('tvj-theme', this.dark ? 'dark' : 'light'); } catch (e) {}
    },
  },

  mounted() {
    try { this.dark = localStorage.getItem('tvj-theme') === 'dark'; } catch (e) {}
    this.month = todayMonth(); // 先给个真月份，免得下拉框在首屏是空的
    this.load();

    // Tailwind 的浏览器版要等它扫完 DOM 才注入样式，中间有一段「有内容没样式」的窗口。
    // 等两帧再淡入，避免用户看到一屏裸 HTML（见 style.css 里的 #app 透明度）。
    const reveal = function () {
      const el = document.getElementById('app');
      if (el) el.classList.add('ready');
    };
    requestAnimationFrame(function () {
      requestAnimationFrame(function () { setTimeout(reveal, 60); });
    });
    // 兜底：标签页在后台时 rAF 根本不会跑（切过去就白屏直到它开始跑），
    // 所以再挂一个不依赖 rAF 的定时器 —— 到时候无论样式编译完没有都先把内容显示出来。
    setTimeout(reveal, 800);
  },
});

app.component('cal-grid', CalGrid);
app.mount('#app');
