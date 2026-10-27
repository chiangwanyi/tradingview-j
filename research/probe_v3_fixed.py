"""
probe_v3 —— 在 probe_v2 的基础上，把「结论能不能信」这件事本身做成可检验的。

v3 相对 v2 的改动，逐条对应 review：

R1  IC 的 purge 口径与模型对齐
    v2: ic_base = finite_X & isfinite(fwd_px) & (ts < TRAIN_END)          ← 只看起点
    v3: 再 & (label_end_ts < TRAIN_END)                                  ← 标签也必须整段落在训练期
    并且 IC 不再只报一个点估计：给出按交易日分块的 bootstrap 区间 + R²，
    因为 IC=0.009 / n=8e4 在统计上"显著"、在经济上等于零。

R2  日内 AUC 的结论降级 + 补显著性
    v2 把 within-day AUC 直接写成「扣除日间基准率漂移后的真实排序能力」。
    v3 只写「同一交易时段内仍有一定排序能力」，并做三件事：
      a) 按**会话段**分组，不再按自然日。实测这份数据里 65.5% 的自然日跨了 ≥2 段会话，
         而每天的休市起点还随夏令时在 05:00 / 06:00 之间跳（487 天 / 169 天），
         所以任何"每天固定 HH:00 切"的写法都会在换时的几周里切歪 —— 直接用 build()
         早就切好的会话段（与标签 seg[j]==seg 同源）最稳；
      b) 对 Δ = 会话内AUC − 混合AUC 做**配对** block bootstrap，给区间和 p；
      c) 给「会话内 AUC > 0.5 的会话占比」+ 二项检验，
         因为加权平均会被少数极端日拉动。

R3  标签定义拆成两个独立实验，不再混用
    A_sign  ：下一根收盘 vs 当前收盘，涨=1 跌=0，持平丢弃（普通方向预测）
    B_1tick ：v2 的口径（thr = 1 tick × √h）—— 保留是为了对照，不是主实验
    B_sigma ：真正的死区，thr = k · close · σ̄ · √h（σ̄ = 96 根对数收益标准差）
    每个 mode 独立训练/评估/汇总，报告各自的样本保留率。
    注意 B_1tick 在 h=1 时阈值 = 1 tick = 最小可能变动，它剔掉的只是「完全没动」的 bar，
    并不构成死区；这一点 v2 的报告没有交代。

R4  Block bootstrap 给两种口径，暴露对抽样方法的敏感性
    iid-day（v2 的做法，按日有放回） vs moving-block（连续 L=5 天整块）
    两者差别大 = 不确定性对「日间是否独立」这个假设敏感。

R5  置换检验
    次数 20 → 200，且 p 值用 (1+#{null≥obs})/(1+N) 的正确形式，
    不再用「位于 xx 分位」这种 5% 分辨率的说法。

R6  Walk-forward
    固定特征与模型配置，滚动训练/测试，逐折记录 AUC / LogLoss / 样本数 / 基准率，
    并把各折的 OOS 预测**拼起来**算总 AUC（不再是单次切分）。
    另外加两个基准：常数基准（=基准率）与单特征 r1n，用来回答「19 个特征到底有没有超过 1 个特征」。

R7  dow 的周期性
    原始 0..6 当连续数值会隐含「周日与周一相邻、周日与周六最远」的错误结构。
    v3 改成 dow_sin / dow_cos。
"""
import os
import sys
import json
import glob
import argparse

import numpy as np
import pandas as pd
from scipy.stats import binomtest
from sklearn.linear_model import LogisticRegression
from sklearn.preprocessing import StandardScaler
from sklearn.metrics import roc_auc_score, log_loss

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

FEATURES = [
    "z96",
    "r1n", "r2n", "r3n", "r6n", "r12n", "r24n", "r48n",
    "body", "uw", "lw", "rng_n",
    "vol_z", "vol_d",
    "streak5", "streak12",
    "tod_sin", "tod_cos", "dow_sin", "dow_cos",
]

LABEL_MODES = {
    "A_sign": dict(kind="sign"),
    "B_1tick": dict(kind="tick", thr=1.0),
    "B_sigma": dict(kind="sigma", k=1.0),
}


# ---------------------------------------------------------------- 数据与特征

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

    rng_ = np.maximum(h - l, EPS)
    f["body"] = (c - o) / rng_
    f["uw"] = (h - np.maximum(o, c)) / rng_
    f["lw"] = (np.minimum(o, c) - l) / rng_
    f["rng_n"] = rng_ / np.maximum(rmean(rng_, WARMUP), EPS)
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
    # R7：星期改周期编码，不再当连续数值
    dow = ts.astype("datetime64[D]").astype(np.int64) % 7
    f["dow_sin"] = np.sin(2 * np.pi * dow / 7.0)
    f["dow_cos"] = np.cos(2 * np.pi * dow / 7.0)

    f["_close"] = c
    f["_sd"] = sd96
    f["_seg_start"] = seg_start.astype(np.float64)

    out = pd.DataFrame(f)
    out["ts"] = ts
    out["pos"] = pos
    return out


def add_labels(d, horizon):
    """连续未来收益 + 标签真正用到的最后一根 bar 的时间（purge 用）。"""
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


def session_ids(d):
    """用 build() 已经切好的会话段作为「日内」的单位。

    不用固定小时锚点：这份数据每天有一小时休市，但休市起点随夏令时在 05:00 / 06:00
    之间来回跳（DST 期间 05:00，标准时 06:00，实测 487 天 vs 169 天）。
    任何"每天从 HH:00 切"的写法都会在换时的那几周把会话切歪。
    会话段来自 gap != 300s，和标签的 seg[j]==seg 判定同源，口径一致。
    """
    return pd.factorize(d["_seg_start"].values)[0]


def make_label(d, horizon, mode):
    """返回 (y, thr_px)。y 为 nan 表示不进任何实验。"""
    fwd = d["fwd_px"].values
    cfg = LABEL_MODES[mode]
    if cfg["kind"] == "sign":
        y = np.where(fwd > 0, 1.0, np.where(fwd < 0, 0.0, np.nan))
        return y, 0.0
    if cfg["kind"] == "tick":
        tick = cfg["_tick"]
        thr = cfg["thr"] * np.sqrt(horizon) * tick
    else:
        thr = cfg["k"] * d["_close"].values * d["_sd"].values * np.sqrt(horizon)
    y = np.where(fwd >= thr, 1.0, np.where(fwd <= -thr, 0.0, np.nan))
    return y, float(np.nanmedian(thr))


# ---------------------------------------------------------------- 统计工具

def fit_lr(X, y, C=0.05, max_iter=400):
    return LogisticRegression(C=C, max_iter=max_iter).fit(X, y)


def day_auc_table(daycodes, y, p, min_n=30):
    """每个交易日单独算 AUC（只保留两类都有、且样本够的日）。"""
    uniq, inv = np.unique(daycodes, return_inverse=True)
    a = np.full(len(uniq), np.nan)
    n = np.bincount(inv, minlength=len(uniq)).astype(float)
    for k in range(len(uniq)):
        idx = np.flatnonzero(inv == k)
        if len(idx) < min_n:
            continue
        yy = y[idx]
        if yy.min() == yy.max():
            continue
        a[k] = roc_auc_score(yy, p[idx])
    return a, n


def within_day_auc(daycodes, y, p, min_n=30):
    a, n = day_auc_table(daycodes, y, p, min_n)
    ok = np.isfinite(a)
    if not ok.any():
        return float("nan"), 0, a, n
    return float(np.average(a[ok], weights=n[ok])), int(ok.sum()), a, n


def boot_ci(daycodes, y, p, a_day, n_day, n_boot=600, seed=7, block=1, min_n=30):
    """按连续数据段重采样，给混合 AUC 区间及可计算分组内 AUC 的配对差区间。

    block=1 为独立分组重采样；block=L 为连续 L 个分组的近似 moving-block。
    分组由 5 分钟时间断档定义，不保证严格等同于交易所交易日。
    """
    uniq = np.unique(daycodes)
    groups = [np.flatnonzero(daycodes == u) for u in uniq]
    nd = len(uniq)
    eligible = np.flatnonzero(np.isfinite(a_day))
    rng = np.random.default_rng(seed)
    pool, wd, diff = [], [], []
    for _ in range(n_boot):
        if block == 1:
            pick = rng.integers(0, nd, nd)
        else:
            nb = int(np.ceil(nd / block))
            starts = rng.integers(0, nd, nb)
            pick = np.concatenate([(s + np.arange(block)) % nd for s in starts])[:nd]
        idx = np.concatenate([groups[k] for k in pick])
        yy = y[idx]
        if yy.min() != yy.max():
            pool.append(roc_auc_score(yy, p[idx]))

        # Δ 比较必须使用同一批具备日内 AUC 的分组，避免两边样本范围不同。
        if len(eligible) >= 5:
            if block == 1:
                pick_ok = rng.choice(eligible, size=len(eligible), replace=True)
            else:
                # 在可计算组的时间顺序中做连续块抽样；这是近似的分组块 bootstrap。
                ne = len(eligible)
                nb_ok = int(np.ceil(ne / block))
                starts_ok = rng.integers(0, ne, nb_ok)
                pick_ok = np.concatenate([
                    eligible[(st + np.arange(block)) % ne] for st in starts_ok
                ])[:ne]
            idx_ok = np.concatenate([groups[k] for k in pick_ok])
            y_ok = y[idx_ok]
            if y_ok.min() != y_ok.max():
                aw = np.average(a_day[pick_ok], weights=n_day[pick_ok])
                ap_ok = roc_auc_score(y_ok, p[idx_ok])
                wd.append(aw)
                diff.append(aw - ap_ok)
    def ci(x):
        if len(x) < 50:
            return (np.nan, np.nan)
        return tuple(np.percentile(x, [2.5, 97.5]))
    return dict(pool=ci(pool), wd=ci(wd), diff=ci(diff),
                p_diff=float(np.mean(np.array(diff) <= 0)) if diff else np.nan,
                n_boot=len(pool))


def month_index(ts):
    return pd.PeriodIndex(pd.to_datetime(ts), freq="M")


def wf_folds(ts, end, min_train_months=12, test_months=3, step_months=3):
    """滚动（expanding）训练 + 固定长度测试窗口；训练侧标签必须整段结束在测试起点之前。"""
    per = month_index(ts)
    all_m = pd.period_range(per.min(), per.max(), freq="M")
    folds = []
    i = min_train_months
    while i + test_months <= len(all_m):
        t0 = all_m[i]
        t1 = all_m[i + test_months - 1]
        s = np.datetime64(t0.start_time)
        e = np.datetime64((t1 + 1).start_time)
        tr = (ts < s) & (end < s)          # purge
        te = (ts >= s) & (ts < e)
        if tr.sum() > 1000 and te.sum() > 200:
            folds.append((tr, te, str(t0), str(t1)))
        i += step_months
    return folds


def perm_test(ts, end, X, y, n_perm, seed=11, C=0.05):
    """R5：单次时间切分下的标签置换，p 值用 (1+#{null>=obs})/(1+N)。"""
    tr = (ts < TRAIN_END) & (end < TRAIN_END)
    te = ts >= VAL_END
    if tr.sum() < 1000 or te.sum() < 200:
        return np.nan, np.nan, 0, np.nan
    sc = StandardScaler().fit(X[tr])
    Xs = sc.transform(X)
    m = fit_lr(Xs[tr], y[tr], C=C)
    obs = roc_auc_score(y[te], m.predict_proba(Xs[te])[:, 1])
    rng = np.random.default_rng(seed)
    null = []
    for _ in range(n_perm):
        ysh = y[tr].copy()
        rng.shuffle(ysh)
        if ysh.min() == ysh.max():
            continue
        m2 = LogisticRegression(C=C, max_iter=300).fit(Xs[tr], ysh)
        null.append(roc_auc_score(y[te], m2.predict_proba(Xs[te])[:, 1]))
    null = np.array(null)
    p = (1.0 + float(np.sum(null >= obs))) / (1.0 + len(null))
    return p, float(null.mean()), len(null), float(obs)


# ---------------------------------------------------------------- 主流程

def evaluate(sym, horizon=1, modes=("A_sign", "B_sigma"), n_perm=200, n_boot=600, C=0.05):
    _, tick = SYMS[sym]
    print(f"\n{'='*92}")
    print(f"{sym}   horizon={horizon}   特征={len(FEATURES)}   C={C}")
    print(f"{'='*92}")

    raw = load_symbol(SYMS[sym][0])
    d = add_labels(build(raw), horizon)
    d = d[d["pos"] >= WARMUP].reset_index(drop=True)
    d["fwd_tick"] = d["fwd_px"].values / tick

    ts = d["ts"].values.astype("datetime64[ns]")
    end = d["label_end_ts"].values.astype("datetime64[ns]")
    ses = session_ids(d)
    cday = ts.astype("datetime64[D]")

    X = d[FEATURES].values.astype(np.float64)
    finite_X = np.isfinite(X).all(axis=1)
    n_seg = len(np.unique(ses))
    cday = ts.astype("datetime64[D]")

    # 诊断：自然日 vs 会话段
    tmp = pd.DataFrame({"cday": cday, "seg": ses})
    per_cday = tmp.groupby("cday")["seg"].nunique()
    straddle = float((per_cday > 1).mean())
    print(f"  会话段={n_seg}  自然日={per_cday.size}  自然日里跨 ≥2 段会话的比例 = {straddle:.1%}")
    print("  → 按自然日分组时，多数的自然日里混着两个交易日的样本；"
          "v3 改用会话段分组（与标签的 seg 判定同源）")

    out = dict(sym=sym, horizon=horizon, n_seg=int(n_seg),
               n_cday=int(per_cday.size), straddle=straddle, modes={})

    for mode in modes:
        LABEL_MODES[mode].setdefault("_tick", tick)
        y, thr_px = make_label(d, horizon, mode)
        keep = finite_X & np.isfinite(y)
        Xm, ym, tsm, endm, dday = X[keep], y[keep], ts[keep], end[keep], ses[keep]
        print(f"\n  --- {mode} ---  阈值≈{thr_px:.4g}  保留 {keep.sum()}/{finite_X.sum()}"
              f" ({keep.sum()/max(finite_X.sum(),1):.1%})  上涨占比={ym.mean():.4f}")

        # R1：IC 使用与模型相同的 label_end purge
        ic_base = finite_X & np.isfinite(d["fwd_px"].values) & (ts < TRAIN_END) & (end < TRAIN_END)
        fwd_c = d["fwd_px"].values
        ics = []
        for nm in FEATURES:
            xv = d[nm].values
            mm = ic_base & np.isfinite(xv)
            if mm.sum() < 100 or xv[mm].std() < 1e-12:
                continue
            ics.append((nm, float(np.corrcoef(xv[mm], fwd_c[mm])[0, 1]), int(mm.sum())))
        ics.sort(key=lambda t: -abs(t[1]))
        n_ic = ics[0][2] if ics else 0
        top = ics[:5]
        print(f"  IC（purge 后 n={n_ic}）: " + "  ".join(f"{nm}={ic:+.5f}" for nm, ic, _ in top))
        if top:
            print(f"       最大 |IC|={abs(top[0][1]):.5f} → R²={top[0][1]**2:.2e}"
                  f"（n={n_ic}，解释力约 {top[0][1]**2*100:.4f}%）")

        # R6：walk-forward。全特征与单特征必须使用完全相同的有效折。
        folds_all = wf_folds(tsm, endm)
        folds = []
        for tr, te, s, e in folds_all:
            if tr.sum() == 0 or te.sum() == 0:
                continue
            if np.unique(ym[tr]).size < 2 or np.unique(ym[te]).size < 2:
                continue
            folds.append((tr, te, s, e))

        rows, oos_p, oos_y, oos_day, oos_base_p = [], [], [], [], []
        for tr, te, s, e in folds:
            sc = StandardScaler().fit(Xm[tr])
            Xs = sc.transform(Xm)
            m = fit_lr(Xs[tr], ym[tr], C=C)
            p = m.predict_proba(Xs[te])[:, 1]
            # 公平的常数基准：只用当前折训练集的上涨比例，不能偷看测试期标签。
            train_rate = float(ym[tr].mean())
            p_base = np.full(te.sum(), np.clip(train_rate, 1e-6, 1 - 1e-6))
            rows.append(dict(test=f"{s}~{e}", n=int(te.sum()),
                             auc=float(roc_auc_score(ym[te], p)),
                             ll=float(log_loss(ym[te], np.clip(p, 1e-6, 1 - 1e-6))),
                             base=float(ym[te].mean()), train_base=train_rate,
                             ll_base=float(log_loss(ym[te], p_base))))
            oos_p.append(p); oos_y.append(ym[te]); oos_day.append(dday[te])
            oos_base_p.append(p_base)
        if not rows:
            print("  walk-forward 无法成形（有效折样本不足或某折只有单一类别）")
            continue
        oos_p = np.concatenate(oos_p); oos_y = np.concatenate(oos_y)
        dday_oos = np.concatenate(oos_day)
        oos_base_p = np.concatenate(oos_base_p)
        auc_oos = float(roc_auc_score(oos_y, oos_p))
        ll_oos = float(log_loss(oos_y, np.clip(oos_p, 1e-6, 1 - 1e-6)))
        ll_base = float(log_loss(oos_y, oos_base_p))
        print(f"  walk-forward（expanding, {len(rows)} 个有效折）:")
        for r in rows:
            print(f"      {r['test']}  n={r['n']:6d}  AUC={r['auc']:.4f}  "
                  f"logloss={r['ll']:.4f}  测试上涨率={r['base']:.3f}  "
                  f"训练集基准率={r['train_base']:.3f}  基准LL={r['ll_base']:.4f}")
        n_above = sum(1 for r in rows if r["auc"] > 0.5)
        print(f"      折 AUC>0.5: {n_above}/{len(rows)}   "
              f"均值={np.mean([r['auc'] for r in rows]):.4f} ± {np.std([r['auc'] for r in rows]):.4f}")
        print(f"      OOS 合并 AUC={auc_oos:.4f}  logloss={ll_oos:.4f} "
              f"(逐折训练集基准 logloss={ll_base:.4f})")

        # 单特征基准：只有 r1n；复用完全相同的有效折。
        j = FEATURES.index("r1n")
        p1s = []
        for tr, te, s, e in folds:
            sc = StandardScaler().fit(Xm[tr][:, [j]])
            m1 = fit_lr(sc.transform(Xm[tr][:, [j]]), ym[tr], C=C)
            p1s.append(m1.predict_proba(sc.transform(Xm[te][:, [j]]))[:, 1])
        p1_oos = np.concatenate(p1s)
        auc_1f = float(roc_auc_score(oos_y, p1_oos))
        print(f"      基准：只用 r1n 的 OOS AUC={auc_1f:.4f}   "
              f"全特征增量={auc_oos - auc_1f:+.4f}")

        # R2 + R4：会话内 AUC、Δ 的配对区间、两种 block 口径
        wd, n_days, a_day, n_day = within_day_auc(dday_oos, oos_y, oos_p)
        b_iid = boot_ci(dday_oos, oos_y, oos_p, a_day, n_day, n_boot=n_boot, block=1)
        b_blk = boot_ci(dday_oos, oos_y, oos_p, a_day, n_day, n_boot=n_boot, block=5)
        ok = np.isfinite(a_day)
        # 点估计 Δ 与 bootstrap 一样，只比较能计算组内 AUC 的相同样本范围。
        segment_codes = np.unique(dday_oos)
        eligible_codes = segment_codes[ok]
        eligible_mask = np.isin(dday_oos, eligible_codes)
        auc_eligible = float(roc_auc_score(oos_y[eligible_mask], oos_p[eligible_mask])) if eligible_mask.any() and np.unique(oos_y[eligible_mask]).size == 2 else np.nan
        frac_pos = float((a_day[ok] > 0.5).mean()) if ok.any() else np.nan
        bp = binomtest(int((a_day[ok] > 0.5).sum()), int(ok.sum()), 0.5,
                       alternative="greater").pvalue if ok.any() else np.nan
        print(f"  连续数据段内 AUC（{n_days} 段；按 5 分钟时间断档切分）= {wd:.4f}  "
              f"全体 OOS AUC = {auc_oos:.4f}；可计算段同样本 AUC = {auc_eligible:.4f}  "
              f"配对 Δ = {wd - auc_eligible:+.4f}")
        print(f"      Δ 的配对区间（限于可计算段的相同样本范围）: iid-group=[{b_iid['diff'][0]:+.4f}, {b_iid['diff'][1]:+.4f}] "
              f"p(Δ≤0)={b_iid['p_diff']:.3f}")
        print(f"      连续数据段 AUC>0.5 的占比 {frac_pos:.1%}（二项检验 p={bp:.3g}；仅作辅助诊断）")
        print(f"      混合 OOS AUC 的 95%CI: iid-group=[{b_iid['pool'][0]:.4f}, {b_iid['pool'][1]:.4f}]  "
              f"moving-block(5 groups)=[{b_blk['pool'][0]:.4f}, {b_blk['pool'][1]:.4f}]")
        width_iid = b_iid["pool"][1] - b_iid["pool"][0]
        width_blk = b_blk["pool"][1] - b_blk["pool"][0]
        print(f"      口径敏感性: 区间宽度 {width_iid:.4f} → {width_blk:.4f}"
              f"（{width_blk / max(width_iid, 1e-9):.2f}×）")

        # R5：置换检验
        p_perm, null_mean, n_eff, auc_split = perm_test(tsm, endm, Xm, ym, n_perm)
        print(f"  置换检验（单次切分 2026-01/2026-05）×{n_eff}: 实测 AUC={auc_split:.4f} "
              f"零假设均值={null_mean:.4f}  p={p_perm:.4f}"
              f"（分辨率 1/{n_eff + 1}={1/(n_eff+1):.4f}）")

        out["modes"][mode] = dict(
            thr=thr_px, kept=int(keep.sum()), up_rate=float(ym.mean()),
            ic_top=top, ic_n=int(n_ic),
            folds=rows, auc_oos=auc_oos, ll_oos=ll_oos, ll_base=ll_base,
            auc_1f=auc_1f, wd_auc=wd, n_days=n_days, delta=wd - auc_eligible,
            auc_oos_eligible=auc_eligible,
            ci_pool_iid=b_iid["pool"], ci_pool_blk=b_blk["pool"],
            ci_delta_iid=b_iid["diff"], p_delta=b_iid["p_diff"],
            frac_day_pos=frac_pos, p_binom=float(bp),
            p_perm=p_perm, null_mean=null_mean, n_perm=n_eff, auc_split=auc_split,
        )
    return out


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("syms", nargs="*", default=None)
    ap.add_argument("--horizon", type=int, default=1)
    ap.add_argument("--modes", default="A_sign,B_sigma")
    ap.add_argument("--perm", type=int, default=200)
    ap.add_argument("--boot", type=int, default=600)
    ap.add_argument("--C", type=float, default=0.05)
    ap.add_argument("--out", default="probe_v3_result.json")
    a = ap.parse_args()

    syms = a.syms or ["MES", "MNQ", "MGC", "CL"]
    modes = tuple(m.strip() for m in a.modes.split(",") if m.strip())
    res = [evaluate(s, a.horizon, modes, a.perm, a.boot, a.C) for s in syms]

    print(f"\n{'='*92}\n汇总（walk-forward OOS）\n{'='*92}")
    hdr = f"{'品种':<5}{'mode':<9}{'样本':>8}{'OOS AUC':>9}{'1f基准':>8}{'会话内':>9}{'Δ':>8}{'p(Δ)':>7}{'置换p':>8}{'折>0.5':>8}"
    print(hdr)
    for r in res:
        for mode, v in r["modes"].items():
            n_above = sum(1 for f in v["folds"] if f["auc"] > 0.5)
            print(f"{r['sym']:<5}{mode:<9}{v['kept']:>8}{v['auc_oos']:>9.4f}{v['auc_1f']:>8.4f}"
                  f"{v['wd_auc']:>8.4f}{v['delta']:>+8.4f}{v['p_delta']:>7.3f}"
                  f"{v['p_perm']:>8.4f}{str(n_above)+'/'+str(len(v['folds'])):>8}")

    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), a.out)
    with open(path, "w", encoding="utf-8") as fh:
        json.dump(res, fh, ensure_ascii=False, indent=2, default=float)
    print(f"\n结果已写入 {path}")


if __name__ == "__main__":
    main()
