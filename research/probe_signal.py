"""
信号探针 v1（已被 probe_v2.py 取代，仅保留用于对照）

【状态】保留原因是它复现了最早的 4 品种 AUC 数字。v2 修正了以下问题：
  1. 特征改用白名单（本文件原用黑名单，_high/_low 曾静默混入）
  2. 训练/验证/测试边界按 label_end_ts 做 purge
  3. 单特征 IC 在全部有效样本上计算（不按死区筛）
  4. 最高置信子集的基线改为同一子集内的多数类
  5. AUC 置信区间改按交易日 block bootstrap
  6. 打乱标签对照重复 20 次
  → 优先使用 probe_v2.py

原始说明
--------
信号探针：在 data/ 的 5 分钟 K 线上，用最朴素的模型测「下一根涨跌」到底有没有可学的信号。

目的不是做出模型，而是拿到一组诚实的上界估计：
  1. 单特征与未来收益的相关系数（IC）
  2. 逻辑回归 / GBDT 在严格时序切分下的 AUC、logloss
  3. 打乱标签的对照组（AUC 应≈0.5，否则说明特征里有未来信息泄漏）

数据缺口的处理：
  - 相邻 bar 间隔 != 300 秒即认为开启新 session（日维护 65 分钟、周末 2945 分钟）
  - 所有滚动特征只在 session 内部计算（靠 pos>=w-1 的掩码保证不跨缺口）
  - 样本要求：段内位置 >= 96，且下一根（标签）仍在同一 session 内
"""
import os
import sys
import glob
import numpy as np
import pandas as pd
from sklearn.linear_model import LogisticRegression
from sklearn.ensemble import HistGradientBoostingClassifier
from sklearn.preprocessing import StandardScaler
from sklearn.metrics import roc_auc_score, log_loss

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DATA = os.path.join(ROOT, "data")
BAR_SEC = 300
WARMUP = 96          # 段内至少要有这么多根才开始取样
EPS = 1e-12

SYMS = {
    "MES": ("CME_MINI_MES1!", 0.25),
    "MNQ": ("CME_MINI_MNQ1!", 0.25),
    "MGC": ("COMEX_MINI_MGC1!", 0.1),
    "CL": ("NYMEX_CL1!", 0.01),
}
TRAIN_END = "2026-01-01"
VAL_END = "2026-05-01"      # 之后为 test


def load_symbol(folder):
    files = sorted(glob.glob(os.path.join(DATA, folder, "*.csv")))
    frames = []
    for f in files:
        try:
            frames.append(pd.read_csv(f))
        except Exception:
            continue
    df = pd.concat(frames, ignore_index=True)
    df["ts"] = pd.to_datetime(df["time"], format="%Y-%m-%d %H:%M")
    df = df.sort_values("ts").drop_duplicates("ts").reset_index(drop=True)
    return df[["ts", "open", "high", "low", "close", "volume"]]


def build_features(df):
    """全部特征严格因果，且不跨 session。返回 (feature_dict, pos, ts, fwd)。"""
    n = len(df)
    ts = df["ts"].values
    o = df["open"].values.astype(np.float64)
    h = df["high"].values.astype(np.float64)
    l = df["low"].values.astype(np.float64)
    c = df["close"].values.astype(np.float64)
    v = df["volume"].values.astype(np.float64)

    # --- session 分段 ---
    gap = np.diff(ts).astype("timedelta64[s]").astype(np.float64)
    new_seg = np.zeros(n, dtype=bool)
    new_seg[0] = True
    if n > 1:
        new_seg[1:] = gap != BAR_SEC
    seg_start = np.maximum.accumulate(np.where(new_seg, np.arange(n), 0))
    pos = np.arange(n) - seg_start          # 段内偏移，0 起

    # --- 段内安全的滚动统计（cumsum 加速）---
    def roll_mean(x, w):
        P = np.concatenate([[0.0], np.cumsum(x)])
        i = np.arange(n)
        j = i + 1 - w
        out = np.full(n, np.nan)
        ok = (pos >= w - 1) & (j >= 0)
        out[ok] = (P[i[ok] + 1] - P[j[ok]]) / w
        return out

    def roll_std(x, w):
        m = roll_mean(x, w)
        m2 = roll_mean(x * x, w)
        return np.sqrt(np.maximum(m2 - m * m, 0.0))

    r1 = np.zeros(n)
    if n > 1:
        r1[1:] = np.log(c[1:] / c[:-1])
    r1[new_seg] = 0.0                       # 段首占位，配合 pos>=96 掩码

    sd96 = roll_std(r1, WARMUP)
    sd96 = np.maximum(sd96, EPS)

    f = {}
    # 价格位置
    pmean = roll_mean(c, WARMUP)
    psd = np.maximum(roll_std(c, WARMUP), EPS)
    f["z96"] = (c - pmean) / psd
    # 多周期归一化收益
    f["r1n"] = r1 / sd96
    for hh in (2, 3, 6, 12, 24, 48):
        rh = np.full(n, np.nan)
        idx = np.where(pos >= hh)[0]
        rh[idx] = np.log(c[idx] / c[idx - hh])
        f["r%dn" % hh] = rh / (sd96 * np.sqrt(hh))
    # bar 形态
    rng = np.maximum(h - l, EPS)
    f["body"] = (c - o) / rng
    f["uw"] = (h - np.maximum(o, c)) / rng
    f["lw"] = (np.minimum(o, c) - l) / rng
    f["rng_n"] = rng / np.maximum(roll_mean(rng, WARMUP), EPS)
    # 量能
    f["vol_z"] = np.log(v + 1) - np.log(roll_mean(v, WARMUP) + 1)
    vd = np.zeros(n)
    if n > 1:
        vd[1:] = np.log(v[1:] + 1) - np.log(v[:-1] + 1)
    vd[new_seg] = 0.0
    f["vol_d"] = vd
    # 短期一致性
    f["streak5"] = roll_mean(np.sign(r1), 5)
    f["streak12"] = roll_mean(np.sign(r1), 12)
    # 日内节律
    mins = ts.astype("datetime64[m]").astype(np.int64) % 1440
    f["tod_sin"] = np.sin(2 * np.pi * mins / 1440.0)
    f["tod_cos"] = np.cos(2 * np.pi * mins / 1440.0)
    f["dow"] = (ts.astype("datetime64[D]").astype(np.int64) % 7).astype(np.float64)

    # --- 标签：未来收益（未来那根必须在同一 session 内）---
    # 返回 H=1 的标签，其余周期在 assemble 里按需重算
    fwd = np.full(n, np.nan)
    if n > 1:
        nxt_same = ~new_seg[1:]
        fwd[:-1][nxt_same] = c[1:][nxt_same] - c[:-1][nxt_same]

    f["_close"] = c
    f["_high"] = h
    f["_low"] = l
    f["_seg_start"] = seg_start.astype(np.float64)
    return f, pos, ts, fwd


def fwd_horizon(d, h):
    """在段内取 h 根之后的收益；跨 session 的一律丢弃。"""
    c = d["_close"].values
    pos = d["pos"].values
    seg_start = d["_seg_start"].values.astype(np.int64)
    n = len(c)
    out = np.full(n, np.nan)
    # 条件：i+h 仍在同一段内 <=> seg_start[i+h] == seg_start[i]
    j = np.arange(n) + h
    ok = (j < n) & (seg_start[np.clip(j, 0, n - 1)] == seg_start)
    out[ok] = c[j[ok]] - c[ok]
    return out


def assemble(sym):
    folder, tick = SYMS[sym]
    df = load_symbol(folder)
    feats, pos, ts, fwd = build_features(df)
    out = pd.DataFrame(feats)
    out["ts"] = ts
    out["pos"] = pos
    out["fwd_px"] = fwd
    out["fwd_tick"] = fwd / tick
    return out


# ---------- 评估 ----------

def auc(y, p):
    if len(np.unique(y)) < 2:
        return float("nan")
    return roc_auc_score(y, p)


def report(name, y, p):
    """同时给出 AUC / logloss / 准确率 / 高置信度子集准确率。"""
    ll = log_loss(y, np.clip(p, 1e-6, 1 - 1e-6))
    acc = ((p > 0.5) == (y == 1)).mean()
    base = max(y.mean(), 1 - y.mean())       # 多数类基线
    k = int(len(p) * 0.2)
    order = np.argsort(-np.abs(p - 0.5))
    top = order[:k]
    acc_top = ((p[top] > 0.5) == (y[top] == 1)).mean() if k > 0 else float("nan")
    print(f"  {name:26s} AUC={auc(y, p):.4f}  logloss={ll:.4f}  "
          f"acc={acc:.4f}  多数类基线={base:.4f}  最高置信20%acc={acc_top:.4f}")


def calibration(y, p, bins=5):
    edges = np.linspace(0.5, 1.0, bins + 1)
    print("    校准（预测概率桶 -> 实际上涨频率）:")
    for k in range(bins):
        lo, hi = 0.5 + (edges[k] - 0.5), 0.5 + (edges[k + 1] - 0.5)
        m = (p >= lo) & (p < hi + (1e-9 if k == bins - 1 else 0))
        if m.sum() < 50:
            continue
        print(f"      [{lo:.2f},{hi:.2f})  n={m.sum():6d}  预测均值={p[m].mean():.4f}  "
              f"实际上涨={y[m].mean():.4f}  偏离={y[m].mean() - p[m].mean():+.4f}")


def run(sym, target="binary", thr_ticks=1.0, use_cross=None, horizon=1):
    tick = SYMS[sym][1]
    print(f"\n{'='*78}\n{sym}  horizon={horizon} bars  dead_zone={thr_ticks}*sqrt(h) tick  "
          f"cross={use_cross}\n{'='*78}")
    d = assemble(sym)

    if use_cross:
        for other in use_cross:
            od = assemble(other)
            keep = od[["ts"]].copy()
            for col in ("r1n", "r6n", "r24n", "z96", "vol_z"):
                keep[f"{other}_{col}"] = od[col].values
            d = d.merge(keep, on="ts", how="left")
            for col in [c for c in d.columns if c.startswith(other + "_")]:
                d[col] = d[col].fillna(0.0)

    d = d[d["pos"] >= WARMUP].copy()
    d["fwd_tick"] = fwd_horizon(d, horizon) / tick
    d = d[np.isfinite(d["fwd_tick"].values)]

    thr = thr_ticks * np.sqrt(horizon)
    y = np.where(d["fwd_tick"].values >= thr, 1.0,
                 np.where(d["fwd_tick"].values <= -thr, 0.0, np.nan))
    mask = np.isfinite(y)

    # 白名单：只允许明确的特征列进入模型。
    # （原版是黑名单，新增内部列 _high/_low 后会静默混入 —— 已修复，改用 probe_v2）
    feat_cols = ["z96", "r1n", "r2n", "r3n", "r6n", "r12n", "r24n", "r48n",
                 "body", "uw", "lw", "rng_n", "vol_z", "vol_d",
                 "streak5", "streak12", "tod_sin", "tod_cos", "dow"]
    X = d[feat_cols].values.astype(np.float64)
    mask &= np.isfinite(X).all(axis=1)
    X, y, ts = X[mask], y[mask], d["ts"].values[mask]
    print(f"  有效样本 {len(y)}  上涨占比 {y.mean():.4f}  "
          f"特征数 {X.shape[1]}  区间 {pd.Timestamp(ts[0]).date()} -> {pd.Timestamp(ts[-1]).date()}")

    tr = ts < np.datetime64(TRAIN_END)
    va = (ts >= np.datetime64(TRAIN_END)) & (ts < np.datetime64(VAL_END))
    te = ts >= np.datetime64(VAL_END)
    print(f"  切分: train={tr.sum()}  val={va.sum()}  test={te.sum()}")

    # 单特征 IC（对连续 fwd_tick 求相关，仅训练集）
    ft = d.loc[mask, "fwd_tick"].values
    ics = []
    for j, name in enumerate(feat_cols):
        xv = X[tr, j]
        if xv.std() < 1e-12:
            continue
        ics.append((abs(np.corrcoef(xv, ft[tr])[0, 1]), name, np.corrcoef(xv, ft[tr])[0, 1]))
    ics.sort(reverse=True)
    print("  单特征 IC top5 (|corr| 与下一根 tick 收益):")
    for a, nm, raw in ics[:5]:
        print(f"      {nm:12s} IC={raw:+.4f}")

    sc = StandardScaler().fit(X[tr])
    Xs = sc.transform(X)
    lr = LogisticRegression(C=0.05, max_iter=400, solver="lbfgs").fit(Xs[tr], y[tr])
    report("LogReg  [val]", y[va], lr.predict_proba(Xs[va])[:, 1])
    report("LogReg  [test]", y[te], lr.predict_proba(Xs[te])[:, 1])

    gb = HistGradientBoostingClassifier(
        max_iter=300, learning_rate=0.05, max_leaf_nodes=31,
        min_samples_leaf=200, l2_regularization=1.0, early_stopping=False,
        random_state=0).fit(X[tr], y[tr])
    p_va = gb.predict_proba(X[va])[:, 1]
    p_te = gb.predict_proba(X[te])[:, 1]
    report("GBDT    [val]", y[va], p_va)
    report("GBDT    [test]", y[te], p_te)
    calibration(y[te], p_te)

    # 对照组：打乱训练标签
    rng = np.random.default_rng(0)
    y_sh = y[tr].copy()
    rng.shuffle(y_sh)
    gb2 = HistGradientBoostingClassifier(
        max_iter=150, learning_rate=0.05, max_leaf_nodes=31,
        min_samples_leaf=200, l2_regularization=1.0, early_stopping=False,
        random_state=0).fit(X[tr], y_sh)
    report("对照:打乱标签 [test]", y[te], gb2.predict_proba(X[te])[:, 1])
    return d


if __name__ == "__main__":
    which = sys.argv[1] if len(sys.argv) > 1 else "MES"
    hz = int(sys.argv[2]) if len(sys.argv) > 2 else 1
    if which == "cross":
        run("MES", use_cross=["MNQ", "MGC", "CL"], horizon=hz)
    else:
        run(which, horizon=hz)
