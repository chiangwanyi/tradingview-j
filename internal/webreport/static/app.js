// 网页版「分析已抓取的数据」的前端逻辑。
//
// 这里**不计算任何统计量**：根数、缺口、断档、坏行全部由 Go 侧的 store.Scan 算好，
// 通过 /api/analysis 原样送过来。前端只做两件事：格式化显示、排序筛选。
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
        '（见下面备注列标了「坏N行」「重复N行」的日文件）。',
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

createApp({
  data() {
    return {
      rep: null,
      loading: false,
      err: '',
      q: '',
      onlyIssues: false,
      onlyGap: false,
      sortKey: 'date',
      sortDir: 1,
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
    fileFilterOn() { return !!(this.onlyIssues || this.onlyGap || this.qIsDate); },
    shownFolders() {
      const self = this;
      return this.folders.filter(function (f) {
        if (!self.folderMatches(f)) return false;
        if (!self.fileFilterOn) return true;
        return self.filesOf(f).length > 0;
      });
    },
    shownFolderCount() { return this.shownFolders.length; },
    byDate() {
      if (!this.qIsDate) return this.byDateAll;
      const q = this.q;
      return this.byDateAll.filter(function (r) { return r.date.indexOf(q) >= 0; });
    },
    snapshot() {
      if (!this.rep) return '';
      const g = this.rep.meta.generatedAt;
      if (!g) return '';
      const d = new Date(g * 1000);
      return '快照 ' + pad2(d.getHours()) + ':' + pad2(d.getMinutes()) + ':' + pad2(d.getSeconds());
    },
  },

  methods: {
    // ---- 格式化（模板里直接用，函数本体在纯函数区） ----
    num, bytes, gap, clock, stamp,

    avg(r) { return Math.round((Number(r.bars) || 0) / Math.max(1, Number(r.symbols) || 1)); },

    sameName(f) { return (f.symbol || '').toLowerCase() === (f.folder || '').toLowerCase(); },

    // ---- 折叠 ----
    isOpen(folder) {
      return this.open[folder] === undefined ? this.allOpen : this.open[folder];
    },
    toggleFolder(folder) { this.open[folder] = !this.isOpen(folder); },

    // ---- 筛选 ----
    folderMatches(f) {
      if (!this.q) return true;
      if (this.qIsDate) return true; // 日期查询交给 filesOf 判定
      const q = this.q.toLowerCase();
      return (f.symbol || '').toLowerCase().indexOf(q) >= 0 ||
        (f.folder || '').toLowerCase().indexOf(q) >= 0;
    },
    filesOf(f) {
      let list = f.files || [];
      if (this.qIsDate) {
        const q = this.q;
        list = list.filter(function (d) { return d.date.indexOf(q) >= 0; });
      }
      if (this.onlyIssues) list = list.filter(dayIssue);
      if (this.onlyGap) list = list.filter(function (d) { return (Number(d.missing) || 0) > 0; });
      return sortFiles(list, this.sortKey, this.sortDir);
    },
    sumOf(f) { return sumOf(this.filesOf(f)); },

    // ---- 排序 ----
    sortBy(key) {
      if (this.sortKey === key) {
        this.sortDir = -this.sortDir;
      } else {
        this.sortKey = key;
        this.sortDir = 1;
      }
    },
    sortCls(key) { return this.sortKey === key ? 'sorted' : ''; },
    arrow(key) { return this.sortKey === key ? (this.sortDir > 0 ? ' ↑' : ' ↓') : ''; },

    rowCls(d) { return dayIssue(d) ? 'row-issue' : ''; },

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
}).mount('#app');
