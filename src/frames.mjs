// TradingView WebSocket 协议层
//
// 帧的外层是自定义分帧：~m~<长度>~m~<负载>，负载通常是 JSON，也可能是心跳 ~h~<n>。
// 一个 WebSocket 帧里可以串接多段。心跳必须原样回送，否则连接会被服务端断开。
//
// 这里不硬编码方法名去取数据，而是"按形状找"：
// 任何形状为 { s: [ { i, v: [数值...] }, ... ] } 的节点都当成一串 K 线。
// 这样 du / timescale_update / 未来新增的方法名都能吃到。

const HEADER_RE = /^~m~(\d+)~m~/;
const HEARTBEAT_RE = /^~h~(\d+)/;

function nextHeaderIndex(text, from) {
  const a = text.indexOf('~m~', from);
  const b = text.indexOf('~h~', from);
  const cands = [a, b].filter((i) => i !== -1);
  return cands.length ? Math.min(...cands) : -1;
}

function makeSegment(payload, header, declared) {
  if (payload.startsWith('~h~')) {
    return { kind: 'heartbeat', raw: payload, header, length: declared };
  }
  try {
    return {
      kind: 'json',
      raw: payload,
      header,
      length: declared,
      value: JSON.parse(payload),
    };
  } catch {
    return { kind: 'opaque', raw: payload, header, length: declared };
  }
}

// 把一段帧文本切成消息列表
// 返回 [{ kind: 'heartbeat' | 'json' | 'opaque', raw, header?, length?, value? }]
//
// 正向扫描，而不是"按 ~m~ 头切分后取间隙"：
// 因为心跳（~h~<n>）没有 ~m~ 头，服务端会把它直接贴在 JSON 负载后面，
// 用间隙法会把 `{"m":...}~h~7` 当成一段，导致 JSON 解析失败。
export function splitFrame(text) {
  const segs = [];
  let pos = 0;

  while (pos < text.length) {
    const rest = text.slice(pos);

    const hb = HEARTBEAT_RE.exec(rest);
    if (hb) {
      segs.push({ kind: 'heartbeat', raw: hb[0], header: null, length: Number(hb[1]) });
      pos += hb[0].length;
      continue;
    }

    const hd = HEADER_RE.exec(rest);
    if (hd) {
      const declared = Number(hd[1]);
      const payloadStart = pos + hd[0].length;
      let end = payloadStart + declared;
      let payload = text.slice(payloadStart, end);

      // 声明长度按字符算。若对不上（例如服务端按字节算，负载含非 ASCII），
      // 退化成"切到下一个帧头之前"，宁可多吞一点也不丢数据。
      const after = text.slice(end, end + 3);
      const consistent =
        payload.length === declared && (after === '' || after.startsWith('~m~') || after.startsWith('~h~'));
      if (!consistent) {
        const next = nextHeaderIndex(text, payloadStart);
        end = next === -1 ? text.length : next;
        payload = text.slice(payloadStart, end);
      }

      pos = end;
      if (payload) segs.push(makeSegment(payload, hd[0], declared));
      continue;
    }

    // 既不是心跳头也不是消息头：吞到下一个帧头之前，作为无法解析的原始片段保留
    const next = nextHeaderIndex(text, pos);
    const end = next === -1 ? text.length : next;
    const raw = text.slice(pos, end);
    pos = end;
    if (raw) segs.push({ kind: 'opaque', raw, header: null });
  }

  return segs;
}

// 构造待发送的帧
export function buildFrame(payload) {
  const body = typeof payload === 'string' ? payload : JSON.stringify(payload);
  return `~m~${body.length}~m~${body}`;
}

// 从任意 JSON 里按形状抽出 K 线
// 返回 [{ series, i, v, ns, timescale, raw }]
//
// 注意 lbs.bar_close_time **不是**这根 K 线的收盘时间。实测整段抓包里它只有一个取值
// （1792115700），是图表右侧可视边界（TradingView 会在最后一根 K 线右侧留白），
// 拿它减 t 会得到 94200s 这种随时间线性漂移的垃圾值。周期只能靠相邻时间戳反推。
export function extractBars(node, out = [], seriesId = null) {
  if (Array.isArray(node)) {
    for (const child of node) extractBars(child, out, seriesId);
    return out;
  }
  if (!node || typeof node !== 'object') return out;

  if (Array.isArray(node.s)) {
    for (const item of node.s) {
      if (Array.isArray(item?.v)) {
        out.push({
          series: seriesId,
          i: item.i,
          v: item.v,
          ns: item.ns ?? node.ns ?? null,
          timescale: node.t ?? null,
          raw: item,
        });
      }
    }
  }

  for (const [k, v] of Object.entries(node)) {
    if (k === 'v' || k === 's' || k === 'ns' || k === 'lbs') continue;
    if (!v || typeof v !== 'object') continue;
    const nextSeries = Array.isArray(v.s) ? k : seriesId;
    extractBars(v, out, nextSeries);
  }
  return out;
}

export const INTERVAL_NAMES = {
  1: '1s', 5: '5s', 15: '15s', 30: '30s',
  60: '1m', 120: '2m', 180: '3m', 300: '5m', 600: '10m', 900: '15m', 1800: '30m',
  3600: '1h', 7200: '2h', 14400: '4h', 86400: '1d', 604800: '1w', 2592000: '1M',
};

export function intervalName(sec) {
  if (!sec || sec <= 0) return null;
  return INTERVAL_NAMES[sec] || `${sec}s`;
}

// [t,o,h,l,c,v] -> 对象；容忍 5 元素（无成交量）与更长的数组
export function toBar(entry, fallbackSymbol = null) {
  const v = entry.v;
  if (!Array.isArray(v) || v.length < 4) return null;
  const [t, o, h, l, c, vol] = v;
  if (![t, o, h, l, c].every((x) => typeof x === 'number' && Number.isFinite(x))) return null;
  return {
    symbol: fallbackSymbol,
    series: entry.series,
    t: Math.round(t),
    o,
    h,
    l,
    c,
    v: typeof vol === 'number' && Number.isFinite(vol) ? vol : null,
    timescale: entry.timescale ?? null,
    raw: v,
  };
}

// 从 TradingView 的 chart 页 URL 里读 ?symbol=，作为兜底品种名
export function symbolFromUrl(url) {
  try {
    const u = new URL(url);
    const s = u.searchParams.get('symbol');
    return s ? decodeURIComponent(s) : null;
  } catch {
    return null;
  }
}

// 读 NDJSON 抓包文件，产出结构化事件
export function* readRecords(lines) {
  for (const line of lines) {
    if (!line.trim()) continue;
    try {
      yield JSON.parse(line);
    } catch {}
  }
}
