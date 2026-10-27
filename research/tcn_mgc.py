"""
tcn_mgc.py —— 小型 TCN 预测 MGC 下一根 5 分钟 K 线「收涨」概率。

输入 64 根 × 20 特征，三个残差块（Conv1D k=3，32 通道，扩张 1/2/4），输出一个 logit。
全部用 NumPy 手写前向 + 反向（本文件自带极简 autograd），不依赖 torch/jax。

数据缺口是本文件的核心约束，不是附加项：
    build() 用 gap != 300s 切出会话段，本文件只在「64 根整窗 + 目标那根」都落在同一段时
    才产样本，并用时间戳再复核一遍。所以 2024-03-08 05:55 那根永远不会去预测 07:00 的涨跌
    （05:55 与 07:00 之间缺了 06:00-06:55 共 12 根，两者不在同一段）。
"""
import os
import sys
import time

import numpy as np

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from probe_v3_fixed import (load_symbol, build, add_labels, FEATURES, SYMS,
                            TRAIN_END, VAL_END)

WIN = 64          # 输入窗口长度（根）
BAR_SEC = 300     # 5 分钟
CH = 32           # TCN 通道数
DILS = (1, 2, 4)


# ---------------------------------------------------------------- 数据

def make_windows(sym="MGC"):
    """返回 (X, y, ts_last, ts_target, info)。X: (N, 20, 64) float32。"""
    raw = load_symbol(SYMS[sym][0])
    d = add_labels(build(raw), 1)                    # 下一根（horizon=1）
    ts = d["ts"].values.astype("datetime64[s]")
    seg = d["_seg_start"].values.astype(np.int64)     # 会话段起点下标
    pos = d["pos"].values.astype(np.int64)            # 段内序号
    close = d["_close"].values.astype(np.float64)
    X_all = d[FEATURES].values.astype(np.float64)
    n = len(d)

    # 整窗连续 = 窗口最后一根的段内序号 >= 63（段内序号逐根 +1，所以等价于 64 根同一段）
    fin = np.isfinite(X_all).all(axis=1)
    csum = np.concatenate([[0], np.cumsum(fin)])
    ends = np.arange(WIN - 1, n - 1)                  # 窗口最后一根的下标（要留出目标那根）
    win_ok = (pos[ends] >= WIN - 1) & ((csum[ends + 1] - csum[ends + 1 - WIN]) == WIN)
    target_ok = seg[ends + 1] == seg[ends]            # 目标那根紧跟窗口，中间没有断档
    lab = close[ends + 1] - close[ends]
    label_ok = lab != 0                               # 持平丢弃，与 probe 的 A_sign 同口径

    sel = ends[win_ok & target_ok & label_ok]         # 用下标值（不是位置），下面全部按下标取
    lab = lab[win_ok & target_ok & label_ok]
    win = sliding(X_all, WIN)                         # (n-WIN+1, 20, WIN)，win[j] 覆盖 j..j+WIN-1

    # 时间戳复核：窗口首尾必须正好差 63 个 bar（数学上等价于 pos>=63，这里核一遍防止口径漂移）
    assert (ts[sel] - ts[sel - (WIN - 1)] == np.timedelta64((WIN - 1) * BAR_SEC, "s")).all()
    assert (ts[sel + 1] - ts[sel] == np.timedelta64(BAR_SEC, "s")).all()

    X = np.ascontiguousarray(win[sel - (WIN - 1)], dtype=np.float32)   # (N, 20, 64)
    # 对齐复核：随手挑两个样本，直接按原始数组重切一遍，防止滑窗下标错位
    for k in (0, len(sel) - 1):
        assert np.array_equal(X[k], X_all[sel[k] - (WIN - 1):sel[k] + 1].T.astype(np.float32))
    ts_last, ts_tgt = ts[sel], ts[sel + 1]
    y = (lab > 0).astype(np.float32)

    # 缺口审查：如果无视会话段直接滚窗，会多出多少个「跨休市」的样本
    naive = int((win_ok & label_ok).sum())
    info = dict(n_bar=n, naive=naive, valid=int(len(sel)), dropped_gap=naive - len(sel))
    return X, y, ts_last, ts_tgt, info


def sliding(a, w):
    """(n, f) -> (n-w+1, f, w) 的只读滑窗视图。"""
    from numpy.lib.stride_tricks import sliding_window_view
    return sliding_window_view(a, w, axis=0)


def selfcheck_gap_case(ts_last, ts_tgt, info):
    """具体案例：2024-03-08 05:55 与 07:00 之间缺 12 根，两者不能出现在同一个样本里。"""
    t1 = np.datetime64("2024-03-08T05:55", "s")
    t2 = np.datetime64("2024-03-08T07:00", "s")
    assert not ((ts_tgt == t2) & (ts_last < t2)).any(), "05:55 的窗口预测了 07:00"
    # 05:55 是 00:00 段的末尾，段内只有 72 根，够不上 64 根窗口 → 它本身也不会成为样本末尾
    assert not (ts_last == t1).any(), "05:55 居然成了窗口末尾"
    print(f"  [缺口审查] 连续窗口 {info['valid']}；跨休市被剔除 {info['dropped_gap']} 个"
          f"（占无视分段滚窗的 {info['dropped_gap']/max(info['naive'],1):.1%}）")


# ---------------------------------------------------------------- autograd

class N:
    """极简反向传播节点：一次前向建一张图，根节点.backward() 一遍到底。"""
    __slots__ = ("d", "prev", "bw", "g")

    def __init__(self, d, prev=(), bw=None):
        self.d, self.prev, self.bw, self.g = d, prev, bw, None

    def backward(self):
        topo, seen = [], set()
        stack = [(self, False)]
        while stack:
            n, done = stack.pop()
            if done:
                topo.append(n)
                continue
            if id(n) in seen:
                continue
            seen.add(id(n))
            stack.append((n, True))
            for p in n.prev:
                if id(p) not in seen:
                    stack.append((p, False))
        self.g = np.ones_like(self.d)
        for n in reversed(topo):
            if n.bw is None or n.g is None:
                continue
            for p, g in zip(n.prev, n.bw(n.g)):
                if g is None:
                    continue
                p.g = g if p.g is None else p.g + g


def add(a, b):
    assert a.d.shape == b.d.shape
    return N(a.d + b.d, (a, b), lambda g: (g, g))


def relu(a):
    m = a.d > 0
    return N(a.d * m, (a,), lambda g: (g * m,))


def last_step(x):
    """取最后一列（因果卷积末端，信息最全）。"""
    keep = lambda g: (np.concatenate([np.zeros_like(x.d[:, :, :-1]), g], axis=2),)
    return N(x.d[:, :, -1:], (x,), keep)


def conv1d(x, w, b, dil, k=3):
    """因果卷积：out[t] 只看 x[t-(k-1)dil .. t]。"""
    Bn, Ci, L = x.d.shape
    Co = w.d.shape[0]
    assert w.d.shape[1] == Ci * k, "卷积核形状与输入通道不匹配"
    pad = (k - 1) * dil
    Xp = np.zeros((Bn, Ci, L + pad), dtype=x.d.dtype)
    Xp[:, :, pad:] = x.d
    cols = np.empty((Bn, Ci * k, L), dtype=x.d.dtype)
    for c in range(Ci):
        for j in range(k):
            cols[:, c * k + j, :] = Xp[:, c, j * dil: j * dil + L]
    out = np.tensordot(cols, w.d, axes=([1], [1])).transpose(0, 2, 1) + b.d.reshape(1, -1, 1)

    def bw(g):
        dw = np.tensordot(cols, g, axes=([0, 2], [0, 2])).T
        db = g.sum(axis=(0, 2))
        dcols = np.tensordot(g, w.d, axes=([1], [0])).transpose(0, 2, 1)
        dXp = np.zeros_like(Xp)
        for c in range(Ci):
            for j in range(k):
                dXp[:, c, j * dil: j * dil + L] += dcols[:, c * k + j, :]
        return (dXp[:, :, pad:], dw, db)

    return N(out, (x, w, b), bw)


def bce_logits(z, y):
    zz, yy = z.d.reshape(-1), y.reshape(-1)
    loss = float(np.mean(np.logaddexp(0.0, zz) - yy * zz))
    p = 1.0 / (1.0 + np.exp(-zz))

    def bw(g):
        return ((g / len(zz) * (p - yy)).reshape(z.d.shape),)

    return N(np.array(loss, dtype=z.d.dtype), (z,), bw)


# ---------------------------------------------------------------- 模型

def init_params(rng):
    def he(fan_in, shape):
        return N((rng.standard_normal(shape) * np.sqrt(2.0 / fan_in)).astype(np.float32))

    P = []
    w_in, b_in = he(20, (CH, 20)), N(np.zeros(CH, np.float32))
    P += [(w_in, "in_w"), (b_in, "in_b")]
    for d in DILS:
        for t in (1, 2):
            P.append((he(CH * 3, (CH, CH * 3)), f"w{t}_{d}"))
            P.append((N(np.zeros(CH, np.float32)), f"b{t}_{d}"))
    # 输出层缩 100 倍：初始 logit 接近 0，loss 从 0.693 起（否则残差堆叠会让初始 logit 抖到 ±4）
    P += [(N((he(CH, (1, CH)).d * 0.01).astype(np.float32)), "out_w"),
          (N(np.zeros(1, np.float32)), "out_b")]
    return {name: node for node, name in P}               # name -> N


def forward(P, xb):
    h = relu(conv1d(N(xb), P["in_w"], P["in_b"], dil=1, k=1))
    for d in DILS:
        y = relu(conv1d(h, P[f"w1_{d}"], P[f"b1_{d}"], d))
        y = conv1d(y, P[f"w2_{d}"], P[f"b2_{d}"], d)
        h = relu(add(h, y))
    return conv1d(last_step(h), P["out_w"], P["out_b"], dil=1, k=1)


def predict(P, X, bs=512):
    out = []
    for s in range(0, len(X), bs):
        z = forward(P, X[s:s + bs]).d.reshape(-1).astype(np.float64)
        out.append(1.0 / (1.0 + np.exp(-z)))
    return np.concatenate(out)


def grad_check():
    """conv1d/add/relu/last_step 的反向用数值梯度核一遍。"""
    rng = np.random.default_rng(0)
    x = N(rng.standard_normal((2, 3, 7)))
    w = N(rng.standard_normal((4, 9)))
    b = N(rng.standard_normal(4))
    v = rng.standard_normal((2, 4, 1))

    def run():
        # 两个不同扩张率的卷积相加，一次覆盖 conv1d / add / relu / last_step 的反向
        return last_step(add(relu(conv1d(x, w, b, 2)), relu(conv1d(x, w, b, 1))))

    def loss_of():
        return (run().d * v).sum()

    y = run()
    loss = N(np.array(loss_of()), (y,),
             lambda g: (np.broadcast_to(g * v, y.d.shape),))
    loss.backward()

    eps = 1e-6
    for tgt, nm in ((x, "dx"), (w, "dw"), (b, "db")):
        num = np.zeros_like(tgt.d)
        for i in np.ndindex(tgt.d.shape):
            old = tgt.d[i]
            tgt.d[i] = old + eps
            lp = loss_of()
            tgt.d[i] = old - eps
            lm = loss_of()
            tgt.d[i] = old
            num[i] = (lp - lm) / (2 * eps)
        err = np.abs(num - tgt.g).max()
        assert err < 1e-6, f"{nm} 梯度不对: {err}"
    print("  [梯度检查] conv1d/add/relu/last_step 通过（最大误差 <1e-6）")


# ---------------------------------------------------------------- 训练

def auc(y, p):
    from sklearn.metrics import roc_auc_score
    return float(roc_auc_score(y, p))


def logloss(y, p):
    p = np.clip(p, 1e-7, 1 - 1e-7)
    return float(-np.mean(y * np.log(p) + (1 - y) * np.log(1 - p)))


def train(Xtr, ytr, Xv, yv, epochs=6, bs=256, lr=2e-3, seed=0, log=print):
    """每轮看验证集，留下验证 logloss 最小的那一版（否则等于拿过拟合的最后一轮去测）。"""
    P = init_params(np.random.default_rng(seed))
    names = list(P)
    m = {k: np.zeros_like(P[k].d) for k in names}
    v = {k: np.zeros_like(P[k].d) for k in names}
    b1, b2, eps = 0.9, 0.999, 1e-8
    step = 0
    n = len(Xtr)
    rng = np.random.default_rng(seed + 1)
    best, best_ll, best_ep = None, np.inf, -1
    for ep in range(epochs):
        order = rng.permutation(n)
        tot = 0.0
        t0 = time.time()
        for s in range(0, n - bs + 1, bs):
            idx = order[s:s + bs]
            for k in names:
                P[k].g = None
            loss = bce_logits(forward(P, Xtr[idx]), ytr[idx])
            loss.backward()
            step += 1
            tot += float(loss.d) * len(idx)
            for k in names:
                if P[k].g is None:
                    continue
                m[k] = b1 * m[k] + (1 - b1) * P[k].g
                v[k] = b2 * v[k] + (1 - b2) * P[k].g ** 2
                mh = m[k] / (1 - b1 ** step)
                vh = v[k] / (1 - b2 ** step)
                P[k].d -= (lr * mh / (np.sqrt(vh) + eps)).astype(P[k].d.dtype)
        pv = predict(P, Xv)
        ll = logloss(yv, pv)
        if ll < best_ll:
            best_ll, best_ep = ll, ep + 1
            best = {k: P[k].d.copy() for k in names}
        log(f"    epoch {ep+1}/{epochs}  train loss={tot/n:.4f}  "
            f"val logloss={ll:.4f}  val AUC={auc(yv, pv):.4f}  {time.time()-t0:.1f}s")
    for k in names:
        P[k].d = best[k]
    log(f"    取第 {best_ep} 轮（val logloss={best_ll:.4f}）")
    return P


def main():
    log = print
    log("=" * 78)
    log("MGC  64 bars × 20 features  ->  P(next close up)   TCN(32ch, dil 1/2/4)")
    log("NumPy + 手写 autograd")
    log("=" * 78)
    grad_check()

    X, y, ts_last, ts_tgt, info = make_windows("MGC")
    log(f"  原始 bar={info['n_bar']}  可用样本={info['valid']}  上涨占比={y.mean():.4f}")
    selfcheck_gap_case(ts_last, ts_tgt, info)

    tr = ts_tgt < TRAIN_END
    va = (ts_last >= TRAIN_END) & (ts_tgt < VAL_END)
    te = ts_tgt >= VAL_END
    log(f"  切分（标签整段落在各自区间内，label 端 purge）：")
    log(f"    训练 <{TRAIN_END}  n={tr.sum():6d} 上涨率={y[tr].mean():.4f}")
    log(f"    验证 {TRAIN_END}~{VAL_END}  n={va.sum():6d} 上涨率={y[va].mean():.4f}")
    log(f"    测试 >={VAL_END}  n={te.sum():6d} 上涨率={y[te].mean():.4f}")
    assert not (tr & te).any()

    # 只用训练集统计量做标准化，避免把未来分布喂进模型
    mu = X[tr].mean(axis=(0, 2), keepdims=True)
    sd = X[tr].std(axis=(0, 2), keepdims=True) + 1e-8
    Xs = ((X - mu) / sd).astype(np.float32)

    log("  训练：")
    P = train(Xs[tr], y[tr], Xs[va], y[va])

    log("  评估：")
    log(f"    {'split':<10}{'n':>7}{'基准率':>9}{'AUC':>9}{'logloss':>10}{'常数LL':>10}{'准确率':>9}")
    for name, msk in (("train", tr), ("val", va), ("test", te)):
        p = predict(P, Xs[msk])
        yy = y[msk]
        base = float(yy.mean())
        ll_b = logloss(yy, np.full(len(yy), base))
        log(f"    {name:<10}{msk.sum():>7}{base:>9.4f}{auc(yy, p):>9.4f}"
            f"{logloss(yy, p):>10.4f}{ll_b:>10.4f}{(p.round() == yy).mean():>9.4f}")

    np.savez_compressed(os.path.join(os.path.dirname(os.path.abspath(__file__)),
                                     "tcn_mgc_params.npz"),
                        **{k: v.d for k, v in P.items()})
    log("  参数已存 research/tcn_mgc_params.npz")


if __name__ == "__main__":
    main()
