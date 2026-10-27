"""
给「信号」定性定量：AUC 0.52 到底是真的，还是抽样噪声？

做法：
  1. 每品种训一个 LogReg（配置与 probe_signal 完全一致）
  2. 在 test 段对 AUC 做 bootstrap 重采样（1000 次，每次抽同样本量、有放回）
  3. 看 95% 置信区间是否完全位于 0.5 之上
  4. 同时给「打乱标签」模型的 AUC 分布，作为「纯噪声能达到多少」的参照
"""
import sys
import os
import numpy as np
from sklearn.linear_model import LogisticRegression
from sklearn.preprocessing import StandardScaler
from sklearn.metrics import roc_auc_score

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from probe_signal import assemble, SYMS, WARMUP, TRAIN_END, VAL_END

EXCLUDE = ("ts", "pos", "fwd_px", "fwd_tick", "_close", "_high", "_low", "_seg_start")
rng = np.random.default_rng(42)


def build(sym, thr_ticks=1.0):
    _, tick = SYMS[sym]
    d = assemble(sym)
    d = d[d["pos"] >= WARMUP].reset_index(drop=True)
    cols = [c for c in d.columns if c not in EXCLUDE]
    X = d[cols].values.astype(np.float64)
    y = np.where(d["fwd_tick"].values >= thr_ticks, 1.0,
                 np.where(d["fwd_tick"].values <= -thr_ticks, 0.0, np.nan))
    ts = d["ts"].values
    m = np.isfinite(y) & np.isfinite(X).all(axis=1)
    return X[m], y[m], ts[m]


def fit_predict(X, y, ts):
    tr = ts < np.datetime64(TRAIN_END)
    te = ts >= np.datetime64(VAL_END)
    sc = StandardScaler().fit(X[tr])
    Xs = sc.transform(X)
    lr = LogisticRegression(C=0.05, max_iter=400).fit(Xs[tr], y[tr])
    return y[te], lr.predict_proba(Xs[te])[:, 1], y[tr], Xs[tr]


def boot_auc(y, p, n_boot=1000):
    n = len(y)
    out = np.empty(n_boot)
    for b in range(n_boot):
        idx = rng.integers(0, n, n)
        yy = y[idx]
        if yy.min() == yy.max():
            out[b] = np.nan
            continue
        out[b] = roc_auc_score(yy, p[idx])
    return np.nanpercentile(out, [2.5, 97.5]), np.nanmean(out), np.nanstd(out)


print(f"{'品种':<5} {'test n':>7} {'AUC':>7} {'bootstrap 95% CI':>20} {'SE':>7} "
      f"{'距0.5':>6} {'噪声上界(97.5%)':>15}")
print("-" * 82)

for sym in ["MES", "MNQ", "MGC", "CL"]:
    X, y, ts = build(sym)
    y_te, p_te, y_tr, X_tr = fit_predict(X, y, ts)
    a = roc_auc_score(y_te, p_te)
    (lo, hi), mean, se = boot_auc(y_te, p_te)

    # 噪声参照：打乱训练标签后重训，看能刷到多高
    y_sh = y_tr.copy()
    rng.shuffle(y_sh)
    lr = LogisticRegression(C=0.05, max_iter=400).fit(X_tr, y_sh)
    X_te = X[ts >= np.datetime64(VAL_END)]
    sc = StandardScaler().fit(X[ts < np.datetime64(TRAIN_END)])
    p_sh = lr.predict_proba(sc.transform(X_te))[:, 1]
    a_sh = roc_auc_score(y_te, p_sh)

    print(f"{sym:<5} {len(y_te):>7} {a:>7.4f}    [{lo:.4f}, {hi:.4f}]  {se:>7.5f} "
          f"{a - 0.5:>+6.4f} {a_sh:>15.4f}")
