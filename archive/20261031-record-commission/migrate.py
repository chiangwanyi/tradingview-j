"""一次性迁移：给 trade_record 补 commission 列，并按「平仓腿 / 开仓腿」回填历史数据。

同时回填 trade.trade_record_id（反手归旧记录）。可重复执行（先清零再算）。
"""
import shutil
import sqlite3
from decimal import Decimal, ROUND_HALF_UP

DB = "db/tradingview-j.db"
BAK = "db/tradingview-j.db.bak-20261031-record-commission"
ZERO = Decimal("0.00")


def money(v):
    return Decimal(v).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)


shutil.copy2(DB, BAK)
print("backup ->", BAK)

con = sqlite3.connect(DB)
con.execute("PRAGMA foreign_keys = ON")

cols = [r[1] for r in con.execute("PRAGMA table_info(trade_record)")]
if "commission" not in cols:
    con.execute("ALTER TABLE trade_record ADD COLUMN commission TEXT NOT NULL DEFAULT '0.00'")
    print("ALTER trade_record ADD COLUMN commission")
else:
    print("column commission already exists")

con.execute("UPDATE trade_record SET commission = '0.00'")
con.execute("UPDATE trade SET trade_record_id = NULL")

recs = [
    {"id": r[0], "account": r[1], "start": r[2], "end": r[3]}
    for r in con.execute("select id, account_id, start_seq, end_seq from trade_record")
]
fees = {r["id"]: ZERO for r in recs}

for account in {r["account"] for r in recs}:
    pos = Decimal(0)
    rows = list(con.execute(
        "select id, seq, side, quantity, commission from trade t "
        "where account_id = ? order by seq", (account,)))
    for tid, seq, side, qty, comm in rows:
        qty = Decimal(qty)
        comm = Decimal(comm)
        signed = qty if side == "BUY" else -qty
        if pos == 0 or (pos > 0) == (signed > 0):
            close_leg = ZERO
        else:
            close_leg = min(abs(pos), abs(signed))
        fee_close = money(comm * close_leg / qty) if close_leg else ZERO

        # -1（未收尾）当作无穷大
        covering = [r for r in recs
                    if r["account"] == account and r["start"] <= seq
                    and (r["end"] == -1 or seq <= r["end"])]
        covering.sort(key=lambda r: r["start"])

        if len(covering) == 1:
            rec = covering[0]
            fees[rec["id"]] += comm
            owner = rec["id"]
        elif len(covering) >= 2:
            # 反手：这笔成交同时是旧记录的终点和新记录的起点
            old, new = covering[0], covering[-1]
            fees[old["id"]] += fee_close
            fees[new["id"]] += comm - fee_close
            owner = old["id"]
        else:
            owner = None

        con.execute("UPDATE trade SET trade_record_id = ? WHERE id = ?", (owner, tid))
        pos += signed

for rid, fee in fees.items():
    con.execute("UPDATE trade_record SET commission = ? WHERE id = ?", (str(money(fee)), rid))

con.commit()
print("\n结果：")
print("区间 | 毛利 | 手续费 | 净额")
for r in con.execute("select start_seq, end_seq, realized_pnl, commission "
                     "from trade_record order by start_seq"):
    net = money(Decimal(r[2]) - Decimal(r[3])) if r[2] is not None else None
    print(r, net)
print("\n成交归属：")
for r in con.execute("select seq, side, quantity, commission, trade_record_id "
                     "from trade t order by seq"):
    print(r)
con.close()
