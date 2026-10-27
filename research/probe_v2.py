"""
probe_v2 —— 修正了 6 处实验方法论问题后的信号探针。

相对 probe_signal.py 的改动（每一条都对应 review 的一条意见）：

1. 特征改成**显式白名单** FEATURES。原版是黑名单，新增内部列 (_high/_low) 会静默混进特征。
   白名单在结构上杜绝这类问题。
2. 时间边界 purge。每个样本记录 label_end_ts（标签真正用到的最后一根 bar 的时间），
   切分时要求 label_end_ts 也落在同一区间内，避免训练样本的标签伸进验证/测试期。
3. 单特征 IC 在**全部有效样本**上算（不按死区筛），与此前的注释一致。
4. 「最高置信子集准确率」的基线改为**同一子集内**的多数类比例，而非全局多数类。
5. AUC 置信区间改用**按交易日分块的 block bootstrap**，避免相邻窗口重叠导致的低估。
6. 打乱标签对照重复 N 次，给出随机 AUC 的分布，看真实结果落在什么分位。

另外补上：测试段的**分季度稳定性**。单一 AUC 可能掩盖「某段时间特别准、其余时间无效」。
"""
import os
import sys
import glob
import numpy as np
import pandas as pd
from sklearn.linear_model import LogisticRegression
from sklearn.preprocessing import StandardScaler
from sklearn.metrics import roc_auc_score, log_loss

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DATA = os.path.join(ROOT, "data")
BAR_SEC = 300
WARMUP = 96
EPS = 1e-12

SYMS = {
    "MES": ("CME_MINI_MES1!", 0.25),
    "MNQ": ("CME_MINI_MNQ1!", 0.25),
    "MGC": ("COMEX_MINI_MGC1!", 0.1),
    "CL": ("NYMEX_CL1!", 0.01),
}
TRAIN_END = np.datetime64("2026-01-01")
VAL_END = np.datetime64("2026-05-01")

# 显式白名单：只有这里列出的才允许进模型
FEATURES = [
    "z96",
    "r1n", "r2n", "r3n", "r6n", "r12n", "r24n", "r48n",
    "body", "uw", "lw", "rng_n",
    "vol_z", "vol_d",
    "streak5", "streak12",
    "tod_sin", "tod_cos", "dow",
]
META = ["ts", "pos", "fwd_px", "fwd_tick", "label_end_ts",
        "_close", "_high", "_low", "_seg_start"]


def load_symbol(folder):
    files = sorted(glob.glob(os.path.join(DATA, folder, "*.csv")))
    frames = []
    for fn in files:
        try:
            frames.append(pd.read_csv(fn))
        except Exception:
            continue
    df = pd.concat(frames, ignore_index=True)
    df["ts"] = pd.to_datetime(df["time"], format="%Y-%m-%d %H:%M")
    df = df.sort_values("ts").drop_duplicates("ts").reset_index(drop=True)
    return df[["ts", "open", "high", "low", "close", "volume"]]


def build(df):
    n = len(df)
    ts = df["ts"].values
    o = df["open"].values.astype(np.float64)
    h = df["high"].values.astype(np.float64)
    l = df["low"].values.astype(np.float64)
    c = df["close"].values.astype(np.float64)
    v = df["volume"].values.astype(np.float64)

    gap = np.diff(ts).astype("timedelta64[s]").astype(np.float64)
    new_seg = np.zeros(n, dtype=bool)
    new_seg[0] = True
    if n > 1:
        new_seg[1:] = gap != BAR_SEC
    seg_start = np.maximum.accumulate(np.where(new_seg, np.arange(n), 0))
    pos = np.arange(n) - seg_start

    def rmean(x, w):
        P = np.concatenate([[0.0], np.cumsum(x)])
        i = np.arange(n)
        j = i + 1 - w
        out = np.full(n, np.nan)
        ok = (pos >= w - 1) & (j >= 0)
        out[ok] = (P[i[ok] + 1] - P[j[ok]]) / w
        return out

    def rstd(x, w):
        m = rmean(x, w)
        return np.sqrt(np.maximum(rmean(x * x, w) - m * m, 0.0))

    r1 = np.zeros(n)
    if n > 1:
        r1[1:] = np.log(c[1:] / c[:-1])
    r1[new_seg] = 0.0

    sd96 = np.maximum(rstd(r1, WARMUP), EPS)
    pmean = rmean(c, WARMUP)
    psd = np.maximum(rstd(c, WARMUP), EPS)

    f = {}
    f["z96"] = (c - pmean) / psd
    f["r1n"] = r1 / sd96
    for hh in (2, 3, 6, 12, 24, 48):
        rh = np.full(n, np.nan)
        idx = np.where(pos >= hh)[0]
        rh[idx] = np.log(c[idx] / c[idx - hh])
        f["r%dn" % hh] = rh / (sd96 * np.sqrt(hh))

    rng = np.maximum(h - l, EPS)
    f["body"] = (c - o) / rng
    f["uw"] = (h - np.maximum(o, c)) / rng
    f["lw"] = (np.minimum(o, c) - l) / rng
    f["rng_n"] = rng / np.maximum(rmean(rng, WARMUP), EPS)
    f["vol_z"] = np.log(v + 1) - np.log(rmean(v, WARMUP) + 1)
    vd = np.zeros(n)
    if n > 1:
        vd[1:] = np.log(v[1:] + 1) - np.log(v[:-1] + 1)
    vd[new_seg] = 0.0
    f["vol_d"] = vd
    f["streak5"] = rmean(np.sign(r1), 5)
    f["streak12"] = rmean(np.sign(r1), 12)

    mins = ts.astype("datetime64[m]").astype(np.int64) % 1440
    f["tod_sin"] = np.sin(2 * np.pi * mins / 1440.0)
    f["tod_cos"] = np.cos(2 * np.pi * mins / 1440.0)
    f["dow"] = (ts.astype("datetime64[D]").astype(np.int64) % 7).astype(np.float64)

    f["_close"] = c
    f["_high"] = h
    f["_low"] = l
    f["_seg_start"] = seg_start.astype(np.float64)

    out = pd.DataFrame(f)
    out["ts"] = ts
    out["pos"] = pos
    return out


def add_labels(d, horizon):
    """返回连续未来收益 + 标签真正用到的那根 bar 的时间。"""
    c = d["_close"].values
    seg = d["_seg_start"].values.astype(np.int64)
    ts = d["ts"].values
    n = len(c)
    j = np.arange(n) + horizon
    ok = (j < n) & (seg[np.clip(j, 0, n - 1)] == seg)
    fwd = np.full(n, np.nan)
    end = np.full(n, np.datetime64("2100-01-01"), dtype="datetime64[ns]")
    fwd[ok] = c[j[ok]] - c[ok]
    end[ok] = ts[j[ok]].astype("datetime64[ns]")
    d = d.copy()
    d["fwd_px"] = fwd
    d["label_end_ts"] = end
    return d


def split(ts, end):
    """purge：标签窗口也必须在区间内结束。"""
    tr = (ts < TRAIN_END) & (end < TRAIN_END)
    va = (ts >= TRAIN_END) & (ts < VAL_END) & (end < VAL_END)
    te = ts >= VAL_END
    return tr, va, te


def block_boot_auc(ts, y, p, n_boot=600, seed=7):
    """按交易日整块重采样，保留日内自相关。"""
    rng = np.random.default_rng(seed)
    days = ts.astype("datetime64[D]")
    uniq, inv = np.unique(days, return_inverse=True)
    groups = [np.flatnonzero(inv == k) for k in range(len(uniq))]
    stats = []
    for _ in range(n_boot):
        pick = rng.integers(0, len(uniq), len(uniq))
        idx = np.concatenate([groups[k] for k in pick])
        yy = y[idx]
        if yy.min() == yy.max():
            continue
        stats.append(roc_auc_score(yy, p[idx]))
    if not stats:
        return (np.nan, np.nan, np.nan)
    return tuple(np.percentile(stats, [2.5, 50, 97.5]))


def within_day_auc(ts, y, p, min_n=50):
    """在每个交易日内单独算 AUC 再加权平均。

    用途：排除「模型只是跟踪了不同日期的基准上涨率」这种假信号。
    混合样本上算的 AUC 会把「某天普涨」当成排序能力，日内 AUC 不会。
    """
    days = ts.astype("datetime64[D]")
    aucs, wts = [], []
    for d in np.unique(days):
        m = days == d
        if m.sum() < min_n:
            continue
        yy = y[m]
        if yy.min() == yy.max():
            continue
        aucs.append(roc_auc_score(yy, p[m]))
        wts.append(int(m.sum()))
    if not aucs:
        return float("nan"), 0
    return float(np.average(aucs, weights=wts)), len(aucs)


def evaluate(sym, horizon=1, thr_ticks=1.0, n_shuffle=20):
    _, tick = SYMS[sym]
    print(f"\n{'='*84}\n{sym}   horizon={horizon}  dead_zone={thr_ticks}tick   特征数={len(FEATURES)}\n{'='*84}")

    d = build(load_symbol(SYMS[sym][0]))
    d = add_labels(d, horizon)
    d = d[d["pos"] >= WARMUP].reset_index(drop=True)

    fwd_tick = d["fwd_px"].values / tick
    d = d.copy()
    d["fwd_tick"] = fwd_tick
    ts = d["ts"].values.astype("datetime64[ns]")
    end = d["label_end_ts"].values.astype("datetime64[ns]")

    X = d[FEATURES].values.astype(np.float64)
    finite_X = np.isfinite(X).all(axis=1)

    thr = thr_ticks * np.sqrt(horizon)
    y_all = np.where(d["fwd_tick"].values >= thr, 1.0,
                     np.where(d["fwd_tick"].values <= -thr, 0.0, np.nan))

    m = finite_X & np.isfinite(y_all)
    Xm, ym, tsm, endm = X[m], y_all[m], ts[m], end[m]
    tr, va, te = split(tsm, endm)
    print(f"  样本 {len(ym)} | train={tr.sum()} val={va.sum()} test={te.sum()} | "
          f"上涨占比={ym.mean():.4f} | {pd.Timestamp(tsm[0]).date()}->{pd.Timestamp(tsm[-1]).date()}")

    # --- 单特征 IC：全部有效样本（不按死区筛），仅训练期 ---
    ic_base = finite_X & np.isfinite(d["fwd_px"].values) & (ts < TRAIN_END)
    fwd_c = d["fwd_px"].values
    ics = []
    for name in FEATURES:
        xv = d[name].values
        mm = ic_base & np.isfinite(xv)
        if mm.sum() < 100 or xv[mm].std() < 1e-12:
            continue
        ics.append((name, np.corrcoef(xv[mm], fwd_c[mm])[0, 1], int(mm.sum())))
    ics.sort(key=lambda t: -abs(t[1]))
    print(f"  IC（全部有效样本 n={ics[0][2] if ics else 0}，非死区筛选）top5:")
    for nm, ic, _ in ics[:5]:
        print(f"      {nm:10s} IC={ic:+.5f}")

    # --- 模型 ---
    sc = StandardScaler().fit(Xm[tr])
    Xs = sc.transform(Xm)
    lr = LogisticRegression(C=0.05, max_iter=400).fit(Xs[tr], ym[tr])
    p_va = lr.predict_proba(Xs[va])[:, 1]
    p_te = lr.predict_proba(Xs[te])[:, 1]
    y_va, y_te = ym[va], ym[te]

    # --- 分季度稳定性 ---
    print("  分季度 test AUC（检查是否只在某一段时间有效）:")
    q = pd.PeriodIndex(pd.to_datetime(tsm[te]), freq="Q")
    for period in sorted(set(q)):
        mk = np.array(q == period)
        if mk.sum() < 300 or y_te[mk].min() == y_te[mk].max():
            continue
        print(f"      {period}  n={mk.sum():6d}  AUC={roc_auc_score(y_te[mk], p_te[mk]):.4f}")

    # --- 置信子集：基线改为同子集多数类 ---
    k = int(len(p_te) * 0.2)
    order = np.argsort(-np.abs(p_te - 0.5))
    top = order[:k]
    acc_top = ((p_te[top] > 0.5) == (y_te[top] == 1)).mean()
    base_top = max(y_te[top].mean(), 1 - y_te[top].mean())
    print(f"  最高置信 20%: acc={acc_top:.4f}  同子集多数类基线={base_top:.4f}  "
          f"增益={acc_top - base_top:+.4f}")

    a_te = roc_auc_score(y_te, p_te)
    a_va = roc_auc_score(y_va, p_va)
    wd_auc, n_days = within_day_auc(tsm[te], y_te, p_te)
    lo, med, hi = block_boot_auc(tsm[te], y_te, p_te)
    print(f"  LogReg: val AUC={a_va:.4f} | test AUC={a_te:.4f}  "
          f"block-bootstrap 95%CI=[{lo:.4f}, {hi:.4f}]")
    print(f"          日内 AUC（{n_days} 个交易日加权）={wd_auc:.4f} "
          f"← 扣除日间基准率漂移后的真实排序能力")
    print(f"          test logloss={log_loss(y_te, np.clip(p_te,1e-6,1-1e-6)):.4f}  "
          f"预测均值={p_te.mean():.4f} 实际上涨={y_te.mean():.4f}")

    # --- 打乱标签对照：重复 N 次 ---
    rng = np.random.default_rng(11)
    null = []
    for _ in range(n_shuffle):
        ysh = ym[tr].copy()
        rng.shuffle(ysh)
        m2 = LogisticRegression(C=0.05, max_iter=300).fit(Xs[tr], ysh)
        null.append(roc_auc_score(y_te, m2.predict_proba(Xs[te])[:, 1]))
    null = np.array(null)
    pct = (null < a_te).mean() * 100
    print(f"  打乱标签对照 ×{n_shuffle}: 均值={null.mean():.4f} "
          f"95%区间=[{np.percentile(null,2.5):.4f}, {np.percentile(null,97.5):.4f}] "
          f"| 真实 AUC 位于 {pct:.0f} 分位")
    return dict(sym=sym, auc_test=a_te, auc_val=a_va, lo=lo, hi=hi,
                null_mean=null.mean(), pct=pct, wd_auc=wd_auc)


if __name__ == "__main__":
    syms = sys.argv[1:] if len(sys.argv) > 1 else ["MES", "MNQ", "MGC", "CL"]
    res = [evaluate(s) for s in syms]
    print(f"\n{'='*84}\n汇总\n{'='*84}")
    print(f"{'品种':<6}{'val AUC':>9}{'test AUC':>10}{'日内AUC':>10}{'95%CI(block)':>22}"
          f"{'零假设均值':>12}{'分位':>7}")
    for r in res:
        ci = f"[{r['lo']:.4f}, {r['hi']:.4f}]"
        print(f"{r['sym']:<6}{r['auc_val']:>9.4f}{r['auc_test']:>10.4f}{r['wd_auc']:>10.4f}"
              f"{ci:>22}{r['null_mean']:>12.4f}{(str(int(r['pct'])) + '%'):>7}")
