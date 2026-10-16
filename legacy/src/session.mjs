// 常驻抓取会话：把「接入 CDP + 抓帧 + 边抓边入库 + 自动回拉」封装成一个可启停的对象
//
// 给两个调用方共用：
//   - HTTP 服务（网页上的"开始/停止抓取"按钮）
//   - CLI 的 tv watch / tv backfill
//
// 这里最重要的一条设计：**抓取**和**自动回拉**是两个独立的开关**。
//   抓取     = 被动监听页面上的所有 WebSocket 帧（页面自己干什么就记什么）
//   自动回拉 = 程序主动去拖动图表，逼页面发出 request_more_data
//
// 为什么必须分开：主动操作图表会让 TradingView 收到"非人类"的交互序列，
// 高频使用有触发风控的可能。默认关掉自动回拉，用户就能只做被动监听
// —— 自己在图表上手动回放，程序老实录下来，这是零额外请求的用法。
//
// 自动回拉还必须是**运行时**可切换的，不能只在启动时定：用户看着数据涨得差不多了
// 想收手，得能立刻停下来，而不是"等这一轮跑完"。

import { EventEmitter } from 'node:events';
import { capture } from './capture.mjs';
import { createIngestor } from './ingest.mjs';
import { rawDirFor, rememberRawFile } from './rawindex.mjs';
import { symbolFromUrl } from './frames.mjs';

export const DEFAULT_SETTINGS = {
  // 自动回拉默认关闭 —— 这是"避免不必要风控"的第一道闸
  autoPull: false,
  // 每次拖动多少像素。太小要拖很多次，太大可能一步跨过缓存窗口反而"看起来没反应"
  dx: 1200,
  // 一次会话最多自动拖多少步（硬上限，防止挂机跑一整夜）
  maxSteps: 12,
  // 连续多少步没有新 K 线就认为到了可拉取边界
  stopAfter: 4,
  // 每步之后等页面安静多久算"数据到齐"
  quietSec: 3,
  // 两步之间的间隔。给大一点是刻意的：这是最直接的风控缓释手段
  betweenSec: 2.5,
  // 会话硬超时（分钟）
  maxMinutes: 30,
  // 结束时把视图拨回原位
  restoreView: true,
  // 接入时匹配哪个标签页
  match: 'tradingview.com/chart',
  // 站内是否安装 WebSocket 记录器（装了才能主动发帧，调试用）
  hookWs: true,
  // 接入后是否刷新页面（刷新会重建 WebSocket，能拿到连接 URL，但会丢当前视图）
  reloadOnStart: false,
  // 指定品种；留空则从图表页 URL 的 ?symbol= 自动识别
  symbol: '',
};

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// 拖动图表：把可视窗口往回拖 -> 图表需要更早的 K 线 -> 页面自己发 request_more_data。
// dx > 0 = 拖向右侧 = 看更早的数据；dx < 0 = 看更晚的数据。
export async function dragPan(session, { cx, cy, dx, dy = 0, segments = 6 }) {
  await session.send('Input.dispatchMouseEvent', {
    type: 'mousePressed', x: cx, y: cy, button: 'left', buttons: 1, clickCount: 1,
  });
  // 分几步移动，图表才认得出是拖拽而不是瞬移
  for (let s = 1; s <= segments; s++) {
    await session.send('Input.dispatchMouseEvent', {
      type: 'mouseMoved',
      x: Math.round(cx + (dx * s) / segments),
      y: cy + dy,
      button: 'left',
      buttons: 1,
    });
    await sleep(30);
  }
  await session.send('Input.dispatchMouseEvent', {
    type: 'mouseReleased',
    x: Math.round(cx + dx),
    y: cy + dy,
    button: 'left',
    buttons: 0,
    clickCount: 1,
  });
}

export class CaptureSession extends EventEmitter {
  /** @param {{ root: string, settings?: object }} */
  constructor({ root, settings = {} }) {
    super();
    this.setMaxListeners(50);
    this.root = root;
    this.settings = { ...DEFAULT_SETTINGS, ...settings };

    this.state = 'idle'; // idle | connecting | running | stopping | error
    this.autoPull = Boolean(this.settings.autoPull);
    this.autoPullSource = this.autoPull ? 'settings' : 'off';
    this.autoPullStopReason = null;
    this.startedAt = null;
    this.stoppedAt = null;
    this.lastError = null;
    this.activeSymbol = this.settings.symbol || null;
    this.targetUrl = null;
    this.targetTitle = null;

    this.counters = { recv: 0, sent: 0, wsCount: 0, moreDataRequests: 0 };
    this.steps = 0;
    this.stepLog = [];
    this.addedBars = 0;
    this.barsNow = 0;
    this.ingestedBars = 0;
    this.bySymbol = {};
    this.lastFrameAt = null;
    this.rawFile = null;

    this.#baseline = null;
    this.#ingestor = null;
    this.#capturePromise = null;
    this.#stopping = false;
    this.#lastStatsAt = 0;
    this.#logs = [];
    this.#runRecorded = false;
  }

  #baseline;
  #ingestor;
  #capturePromise;
  #stopping;
  #lastStatsAt;
  #logs;
  #runRecorded;

  // ---------- 日志环形缓冲：网页刷新后仍能看到刚才发生了什么 ----------
  log(text, level = 'info') {
    const line = { at: Date.now(), level, text: String(text) };
    this.#logs.push(line);
    if (this.#logs.length > 800) this.#logs.splice(0, this.#logs.length - 800);
    this.emit('log', line);
    return line;
  }

  logs(limit = 300) {
    return this.#logs.slice(-limit);
  }

  // ---------- 品种解析 ----------
  // 三级兜底：显式设置 -> 图表页 URL 的 ?symbol= -> 本次会话已经解出 K 线的品种
  resolveSymbol() {
    if (this.settings.symbol) return this.settings.symbol;
    if (this.activeSymbol) return this.activeSymbol;
    const fromUrl = symbolFromUrl(this.targetUrl || '');
    if (fromUrl) {
      this.activeSymbol = fromUrl;
      return fromUrl;
    }
    const seen = Object.keys(this.bySymbol);
    if (seen.length === 1) {
      this.activeSymbol = seen[0];
      return seen[0];
    }
    return null;
  }

  // 当前品种库里的不同时间戳数 —— 判断"这一步有没有拉到新东西"的硬指标。
  // 用不同 t 而不是行数：同一根 K 线被抓两次行数会变，时间戳数不会。
  // 直接复用 ingestor 的写连接，保证看得到刚写进去的行（也避免开一堆句柄）。
  countBars(symbol = null) {
    const sym = symbol || this.resolveSymbol();
    if (!sym || !this.#ingestor) return null;
    try {
      const db = this.#ingestor.forSymbol(sym).db;
      const row = db.prepare('SELECT COUNT(DISTINCT t) AS n FROM bars').get();
      return row?.n ?? 0;
    } catch {
      return null;
    }
  }

  // 刷新"本会话净增"统计。刻意做了节流：du 增量是一根一根来的，
  // 每帧都跑一次 COUNT(DISTINCT t) 会把 CPU 烧在扫全表上。
  refreshStats(force = false) {
    const now = Date.now();
    if (!force && now - this.#lastStatsAt < 1000) return;
    this.#lastStatsAt = now;
    try {
      this.#ingestor?.flush();
    } catch {}
    const sym = this.resolveSymbol();
    if (!sym) return;
    const n = this.countBars(sym);
    if (n == null) return;
    if (this.#baseline == null) this.#baseline = n;
    this.barsNow = n;
    this.addedBars = Math.max(0, n - this.#baseline);
  }

  get baseline() {
    return this.#baseline;
  }

  snapshot() {
    return {
      state: this.state,
      autoPull: this.autoPull,
      autoPullSource: this.autoPullSource,
      autoPullStopReason: this.autoPullStopReason,
      settings: this.settings,
      startedAt: this.startedAt,
      stoppedAt: this.stoppedAt,
      elapsedMs: this.startedAt ? (this.stoppedAt || Date.now()) - this.startedAt : 0,
      counters: { ...this.counters },
      steps: this.steps,
      stepLog: this.stepLog.slice(-40),
      addedBars: this.addedBars,
      baseline: this.#baseline,
      barsNow: this.barsNow,
      bySymbol: { ...this.bySymbol },
      symbol: this.resolveSymbol(),
      targetTitle: this.targetTitle,
      targetUrl: this.targetUrl,
      lastError: this.lastError,
      lastFrameAt: this.lastFrameAt,
      rawFile: this.rawFile,
      running: this.state === 'running' || this.state === 'connecting' || this.state === 'stopping',
    };
  }

  // ---------- 启停 ----------
  async start(overrides = {}) {
    if (this.state === 'running' || this.state === 'connecting') {
      throw new Error('已经有一个抓取会话在运行了');
    }
    this.settings = { ...this.settings, ...overrides };
    if (overrides.autoPull !== undefined) {
      this.autoPull = Boolean(overrides.autoPull);
      this.autoPullSource = this.autoPull ? 'settings' : 'off';
    }

    this.state = 'connecting';
    this.startedAt = Date.now();
    this.stoppedAt = null;
    this.lastError = null;
    this.steps = 0;
    this.stepLog = [];
    this.addedBars = 0;
    this.barsNow = 0;
    this.ingestedBars = 0;
    this.bySymbol = {};
    this.#baseline = null;
    this.autoPullStopReason = null;
    this.counters = { recv: 0, sent: 0, wsCount: 0, moreDataRequests: 0 };
    this.#stopping = false;
    this.#lastStatsAt = 0;
    this.#runRecorded = false;
    this.#ingestor = createIngestor({ root: this.root });
    this.emit('state', this.snapshot());

    const symbolForRaw = this.settings.symbol || null;

    const p = capture({
      root: this.root,
      match: [this.settings.match || DEFAULT_SETTINGS.match].flat().filter(Boolean),
      reload: Boolean(this.settings.reloadOnStart),
      hookWs: Boolean(this.settings.hookWs),
      rawDir: rawDirFor(this.root, symbolForRaw),
      symbolHint: symbolForRaw,
      log: (m) => this.log(m),
      onRecord: (rec) => this.#onRecord(rec),
      onOpen: ({ file }) => {
        this.rawFile = file;
        rememberRawFile(this.root, {
          file,
          symbol: symbolForRaw,
          url: this.targetUrl,
          title: this.targetTitle,
        });
      },
      progressEveryMs: 2000,
      onProgress: (pr) => this.#onProgress(pr),
      shouldStop: () => this.#stopping,
      onReady: (ctx) => {
        this.state = 'running';
        this.targetUrl = ctx.target?.url || null;
        this.targetTitle = ctx.target?.title || null;
        // 先把品种定下来，raw 索引也就准了
        const sym = this.resolveSymbol();
        if (sym) {
          rememberRawFile(this.root, {
            file: this.rawFile,
            symbol: sym,
            url: this.targetUrl,
            title: this.targetTitle,
          });
        }
        // 基线要在数据涌进来之前取。早期帧可能已经处理过几根，所以取"当前值"，
        // 宁可少算几根也不要凭空多算。
        this.#baseline = this.countBars(sym) ?? 0;
        this.log(`已接入：${[this.targetTitle, this.targetUrl].filter(Boolean).join(' ')}`);
        this.log(`识别品种：${sym || '（未知，自动回拉将不可用）'} · 当前库内 ${this.#baseline} 根`);
        this.log(
          this.autoPull
            ? '自动回拉：已开启 —— 程序会主动拖动图表来拉历史'
            : '自动回拉：已关闭 —— 只被动记录，你自己在图表上操作即可',
        );
        this.#runDriver(ctx.session).catch((e) => {
          this.log(`[驱动器异常] ${e.message}`, 'error');
        });
        this.emit('state', this.snapshot());
      },
    })
      .then((r) => {
        this.log(`抓帧结束：收 ${r.recv} / 发 ${r.sent} 帧 · 原始记录 ${r.file}`);
        return r;
      })
      .catch((e) => {
        this.lastError = e.message;
        this.state = 'error';
        this.log(`抓取失败：${e.message}`, 'error');
        throw e;
      })
      .finally(() => this.#finalize());

    // 内部兜一层，避免调用方忘了 catch 时冒出 unhandled rejection
    // （返回给调用方的仍然是原始 promise，错误照样能拿到）
    p.catch(() => {});
    this.#capturePromise = p;
    return p;
  }

  #finalize() {
    try {
      this.#ingestor?.flush();
      // 顺序很重要：refreshStats / recordRun 都要用 ingestor 里的写连接，
      // 关掉之后 forSymbol 会重新开一个连接，那就永远没人关了（每次会话漏一个句柄）。
      this.refreshStats(true);
      this.#recordRun(this.resolveSymbol());
    } catch {}
    try {
      this.#ingestor?.close();
    } catch {}
    this.#ingestor = null;
    this.stoppedAt = Date.now();
    if (this.state !== 'error') this.state = 'idle';
    this.emit('state', this.snapshot());
  }

  // 把这次会话记进品种库的 runs 表（UI 的"抓取历史"用得上）
  #recordRun(sym) {
    if (this.#runRecorded || !sym || !this.#ingestor) return;
    this.#runRecorded = true;
    try {
      const db = this.#ingestor.forSymbol(sym).db;
      db.prepare(
        `INSERT INTO runs (file, target, started, finished, recv, send, bars) VALUES (?, ?, ?, ?, ?, ?, ?)`,
      ).run(
        this.rawFile,
        this.targetUrl,
        this.startedAt,
        Date.now(),
        this.counters.recv,
        this.counters.sent,
        this.addedBars,
      );
    } catch {}
  }

  // 优雅停止：只置标志位，实际收尾由 capture 的 try/finally 完成
  stop(reason = 'user') {
    if (this.state === 'idle' || this.state === 'error') return false;
    this.#stopping = true;
    this.state = 'stopping';
    this.autoPull = false;
    this.autoPullSource = 'off';
    this.log(`请求停止（${reason}）…`);
    this.emit('state', this.snapshot());
    return true;
  }

  // ---------- 自动回拉的运行时开关 ----------
  setAutoPull(on, source = 'user') {
    const next = Boolean(on);
    if (next === this.autoPull) return this.autoPull;
    this.autoPull = next;
    this.autoPullSource = next ? source : 'off';
    if (next) this.autoPullStopReason = null;
    this.log(next ? '自动回拉：已开启' : '自动回拉：已关闭（转为被动监听）');
    // 开启时如果已经跑完预算，重置计数，让用户能再要一轮
    if (next && this.steps >= Number(this.settings.maxSteps)) {
      this.log(`步数已达上限（${this.settings.maxSteps}），重置计数以开始新一轮`);
      this.steps = 0;
      this.stepLog = [];
    }
    this.emit('state', this.snapshot());
    return this.autoPull;
  }

  #onRecord(rec) {
    this.lastFrameAt = Date.now();
    if (
      rec.k === 'frame' &&
      rec.dir === 'send' &&
      typeof rec.text === 'string' &&
      rec.text.includes('request_more_data')
    ) {
      this.counters.moreDataRequests++;
    }
    try {
      this.#ingestor?.push(rec);
      const s = this.#ingestor?.summary;
      if (s) {
        this.ingestedBars = s.bars;
        for (const [sym, v] of s.bySymbol) this.bySymbol[sym] = v.bars;
      }
    } catch (e) {
      this.log(`[warn] 入库失败：${e.message}`, 'warn');
      return;
    }
    this.refreshStats(false);
  }

  #onProgress({ recv, sent, wsCount }) {
    this.counters.recv = recv;
    this.counters.sent = sent;
    this.counters.wsCount = wsCount;
    this.emit('state', this.snapshot());
  }

  // 自动回拉驱动器
  async #runDriver(session) {
    // 注意：这里刻意每次循环都重新读 this.settings，而不是开头解构一次。
    // 「每步间隔」「最多步数」这些是用户会在运行中现场调的（节奏太快想放慢、
    // 数据够了想收手），解构一次就等于把它们钉死在启动那一刻。
    const cfg = () => this.settings;
    const num = (v, d, min) => {
      const n = Number(v);
      return Number.isFinite(n) && n >= min ? n : d;
    };
    const hardCapMs = () => num(cfg().maxMinutes, 30, 1) * 60000;

    let geo;
    try {
      geo = JSON.parse(
        await session.eval('JSON.stringify({w: window.innerWidth, h: window.innerHeight})', {
          returnByValue: true,
        }),
      );
    } catch (e) {
      this.log(`无法读取页面尺寸，自动回拉不可用：${e.message}`, 'warn');
      return;
    }
    const cx = Math.round(geo.w * 0.5);
    const cy = Math.round(geo.h * 0.45);
    this.log(`图表窗口 ${geo.w}×${geo.h}，拖动锚点 (${cx}, ${cy})`);

    // 后台标签页的渲染循环会被节流，图表可能压根不处理拖拽。
    // 我们踩过这个坑：同一段拖动代码在前台有效、在常驻会话里无效。
    await session.send('Page.bringToFront').catch(() => {});
    await sleep(1200);

    let noGain = 0;
    let moved = 0;
    const driverStart = Date.now();
    let exitReason = null;

    while (!this.#stopping) {
      // 等自动回拉被打开。这个循环就是"运行时开关"的落点 ——
      // 关掉之后驱动器停在这里空转（每 300ms 一次廉价判断），完全不碰页面。
      if (!this.autoPull) {
        if (this.autoPullStopReason && exitReason == null) exitReason = this.autoPullStopReason;
        await sleep(300);
        continue;
      }
      if (this.autoPullStopReason) {
        // 用户又手动打开过，之前的终止原因作废
        this.autoPullStopReason = null;
        exitReason = null;
      }

      if (Date.now() - driverStart > hardCapMs()) {
        exitReason = 'hard-cap';
        this.log(`已达会话硬上限（${num(cfg().maxMinutes, 30, 1)} 分钟），自动回拉停下`, 'warn');
        break;
      }
      if (this.steps >= num(cfg().maxSteps, 12, 1)) {
        exitReason = 'budget';
        this.log(`已达本次自动回拉步数上限（${num(cfg().maxSteps, 12, 1)} 步），转为被动监听`);
        break;
      }

      const sym = this.resolveSymbol();
      if (!sym) {
        exitReason = 'no-symbol';
        this.log('无法确定品种（图表页 URL 没有 ?symbol=，也没在设置里指定），自动回拉停下', 'warn');
        break;
      }

      const before = this.countBars(sym) ?? 0;
      const moreBefore = this.counters.moreDataRequests;
      const t0 = Date.now();
      const stepNo = this.steps + 1;

      await session.send('Page.bringToFront').catch(() => {});
      if (this.#stopping) break;
      await dragPan(session, { cx, cy, dx: num(cfg().dx, 1200, 50) });
      moved++;

      // 等这一轮的数据到齐：页面安静 quietMs 就认为到齐，但最多多等 8 秒
      const quietMs = Math.max(500, num(cfg().quietSec, 3, 0.5) * 1000);
      let lastSeen = this.lastFrameAt || Date.now();
      while (!this.#stopping && Date.now() - lastSeen < quietMs && Date.now() - t0 < quietMs + 8000) {
        await sleep(200);
        lastSeen = this.lastFrameAt || lastSeen;
      }
      this.refreshStats(true);

      const after = this.countBars(sym) ?? before;
      const gained = after - before;
      const asked = this.counters.moreDataRequests - moreBefore;
      this.steps = stepNo;
      if (this.#baseline == null) this.#baseline = before;

      const entry = { step: stepNo, at: Date.now(), gained, total: after, asked, symbol: sym };
      this.stepLog.push(entry);
      if (this.stepLog.length > 200) this.stepLog.splice(0, this.stepLog.length - 200);

      this.log(
        `  第 ${stepNo}/${num(cfg().maxSteps, 12, 1)} 步 · 新增 ${gained} 根 · 共 ${after} 根 · 图表请求 ${asked} 次`,
      );
      // 区分两种"没新增"：图表没请求（可视范围还没走出缓存窗口）
      // vs 请求了但没有更早的数据（真到边界）
      if (gained === 0 && asked === 0) {
        this.log('  图表这一步没发 request_more_data —— 可视范围还没走出已缓存的那一窗', 'warn');
      }
      this.emit('step', entry);

      if (gained === 0) {
        noGain++;
        if (noGain >= num(cfg().stopAfter, 4, 1)) {
          exitReason = 'boundary';
          this.log(`连续 ${noGain} 步没有新数据 —— 已到可拉取的边界，自动回拉停下`);
          break;
        }
      } else {
        noGain = 0;
      }

      await sleep(Math.max(0, num(cfg().betweenSec, 2.5, 0) * 1000));
    }

    // 回填把视图留在很久以前，默认拨回现在
    if (this.settings.restoreView && moved && !this.#stopping) {
      this.log(`恢复视图：反向拖 ${moved} 次…`);
      await session.send('Page.bringToFront').catch(() => {});
      const back = num(cfg().dx, 1200, 50);
      for (let i = 0; i < moved && !this.#stopping; i++) {
        try {
          await dragPan(session, { cx, cy, dx: -back });
        } catch {
          break;
        }
        await sleep(250);
      }
    }

    if (exitReason && exitReason !== 'budget-idle') {
      this.autoPull = false;
      this.autoPullSource = 'off';
      this.autoPullStopReason = exitReason;
      this.emit('state', this.snapshot());
    }
    this.emit('driver-done', { reason: exitReason, steps: this.steps, moved });
  }

  // 等待 capture 真正收尾（服务关闭时用）
  async wait() {
    if (!this.#capturePromise) return this.snapshot();
    try {
      await this.#capturePromise;
    } catch {}
    return this.snapshot();
  }
}
