"""
目标定义对比：「先到谁」(first touch) vs close-to-close 方向。

「先到谁」= 从当前收盘价出发，未来 H 根内先碰到 close+k*tick 还是 close-k*tick。
同一根 bar 内两侧都碰到 = 歧义，丢弃（保守处理，不猜先后）。
"""
import sys
import os
import numpy as np
from sklearn.linear_model import LogisticRegression
from sklearn.preprocessing import StandardScaler
from sklearn.metrics import roc_auc_score, log_loss

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from probe_signal import assemble, SYMS, WARMUP, TRAIN_END, VAL_END

EXCLUDE = ("ts", "pos", "fwd_px", "fwd_tick", "_close", "_high", "_low", "_seg_start")


def touch_label(d, k_ticks, horizon, tick):
    n = len(d)
    c = d["_close"].values
    seg_start = d["_seg_start"].values.astype(np.int64)
    high = d["_high"].values
    low = d["_low"].values
    up = c + k_ticks * tick
    dn = c - k_ticks * tick
    y = np.full(n, np.nan)
    idx = np.arange(n)
    for j in range(1, horizon + 1):
        t = idx + j
        ok = (t < n) & (seg_start[np.clip(t, 0, n - 1)] == seg_start) & ~np.isfinite(y)
        if not ok.any():
            break
        ti, si = t[ok], idx[ok]
        hit_up = high[ti] >= up[si]
        hit_dn = low[ti] <= dn[si]
        y[si[hit_up & ~hit_dn]] = 1.0
        y[si[hit_dn & ~hit_up]] = 0.0
        y[si[hit_up & hit_dn]] = np.nan
    return y


def run(sym, cases):
    _, tick = SYMS[sym]
    print(f"\n{'='*88}\n{sym}  目标定义对比（LogReg，严格时序切分）\n{'='*88}")
    print(f"  {'定义':<18} {'样本':>8} {'涨占比':>8} {'val AUC':>8} {'test AUC':>9} {'test logloss':>13}")
    d = assemble(sym)
    d = d[d["pos"] >= WARMUP].reset_index(drop=True)
    feat_cols = [c for c in d.columns if c not in EXCLUDE]
    X = d[feat_cols].values.astype(np.float64)
    ts = d["ts"].values
    Xok = np.isfinite(X).all(axis=1)

    for k, hz in cases:
        y = touch_label(d, k, hz, tick)
        m = Xok & np.isfinite(y)
        Xm, ym, tm = X[m], y[m], ts[m]
        tr = tm < np.datetime64(TRAIN_END)
        va = (tm >= np.datetime64(TRAIN_END)) & (tm < np.datetime64(VAL_END))
        te = tm >= np.datetime64(VAL_END)
        if tr.sum() < 1000 or va.sum() < 200 or te.sum() < 200:
            continue
        sc = StandardScaler().fit(Xm[tr])
        Xs = sc.transform(Xm)
        lr = LogisticRegression(C=0.05, max_iter=400).fit(Xs[tr], ym[tr])
        pv = lr.predict_proba(Xs[va])[:, 1]
        pt = lr.predict_proba(Xs[te])[:, 1]
        av = roc_auc_score(ym[va], pv) if len(np.unique(ym[va])) > 1 else float("nan")
        at = roc_auc_score(ym[te], pt) if len(np.unique(ym[te])) > 1 else float("nan")
        ll = log_loss(ym[te], np.clip(pt, 1e-6, 1 - 1e-6))
        print(f"  先到 {k}tick/≤{hz:2d}根   {m.sum():>8} {ym.mean():>8.4f} "
              f"{av:>8.4f} {at:>9.4f} {ll:>13.4f}")


if __name__ == "__main__":
    sym = sys.argv[1] if len(sys.argv) > 1 else "MGC"
    run(sym, [(1, 3), (1, 6), (2, 6), (2, 12), (3, 12), (3, 24)])
