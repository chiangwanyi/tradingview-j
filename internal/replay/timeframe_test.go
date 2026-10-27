package replay

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// mkBars 造一串 5 分钟 K 线：时间从 start 开始，每根涨 1 块钱，量恒为 10。
// 只有一个入参是「形态」的地方：第 3 根起高点 +5，用来验极值取的是桶内最大值。
func mkBars(times []string) []Bar {
	out := make([]Bar, 0, len(times))
	for i, t := range times {
		p := 100 + float64(i)
		out = append(out, Bar{Time: t, Open: p, High: p + 1, Low: p - 1, Close: p + 0.5, Volume: 10})
	}
	return out
}

func sameF(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAggregateBuckets(t *testing.T) {
	times := []string{
		"2026-10-16 09:00", "2026-10-16 09:05", "2026-10-16 09:10",
		"2026-10-16 09:15", "2026-10-16 09:20", "2026-10-16 09:25",
		"2026-10-16 09:30", // 半个桶：15 分钟周期下这就是最后一根（正在形成）
	}
	bars := mkBars(times)

	cases := []struct {
		minutes int
		want    []string // 每根聚合 K 线的标签 = 桶里第一根的真实时间
	}{
		{5, times[:]},
		{10, []string{"2026-10-16 09:00", "2026-10-16 09:10", "2026-10-16 09:20", "2026-10-16 09:30"}},
		{15, []string{"2026-10-16 09:00", "2026-10-16 09:15", "2026-10-16 09:30"}},
		{30, []string{"2026-10-16 09:00", "2026-10-16 09:30"}},
		{60, []string{"2026-10-16 09:00"}},
	}
	for _, c := range cases {
		got := Aggregate(bars, c.minutes)
		if len(got) != len(c.want) {
			t.Fatalf("%dm：聚合出 %d 根，期望 %d 根", c.minutes, len(got), len(c.want))
		}
		for i := range c.want {
			if got[i].Time != c.want[i] {
				t.Fatalf("%dm 第 %d 根时间是 %s，期望 %s", c.minutes, i, got[i].Time, c.want[i])
			}
		}
	}

	// 15 分钟第一桶 = 09:00/09:05/09:10 三根
	b := Aggregate(bars, 15)[0]
	if !sameF(b.Open, 100) || !sameF(b.Close, 102.5) || !sameF(b.High, 103) || !sameF(b.Low, 99) || !sameF(b.Volume, 30) {
		t.Fatalf("OHLCV 不对：%+v", b)
	}
	// EMA20 在聚合序列上重算：7 根 5 分钟 → 3 根 15 分钟，都不到 20 根，全是 nil
	for i, x := range Aggregate(bars, 15) {
		if x.Ema20 != nil {
			t.Fatalf("不足 20 根不该有 EMA20（第 %d 根有值）", i)
		}
	}
}

// 缺口不补：09:10 之后直接跳到 10:00（休市/断档），不造中间那两根假 K 线。
func TestAggregateNoGapFill(t *testing.T) {
	bars := mkBars([]string{"2026-10-16 09:05", "2026-10-16 09:10", "2026-10-16 10:00"})
	got := Aggregate(bars, 60)
	if len(got) != 2 {
		t.Fatalf("缺口被补了：%d 根", len(got))
	}
	if got[0].Time != "2026-10-16 09:05" || got[1].Time != "2026-10-16 10:00" {
		t.Fatalf("时间标签不对：%s / %s", got[0].Time, got[1].Time)
	}
	// 桶不跨日：23:55 和次日 00:00 必须分成两根
	cross := mkBars([]string{"2026-10-16 23:55", "2026-10-17 00:00"})
	if n := len(Aggregate(cross, 240)); n != 2 {
		t.Fatalf("跨日被并成 %d 根", n)
	}
}

// EMA20 在聚合序列上重算：300 根 5 分钟（25 小时，跨两天）→ 100 根 15 分钟，
// EMA 必须从第 20 根才开始有值。
// 这条守的是「不要把 5 分钟那条均线搬到聚合序列上」。
func TestAggregateRecomputesEMA(t *testing.T) {
	start := time.Date(2026, 10, 16, 9, 0, 0, 0, time.Local)
	var times []string
	for i := 0; i < 300; i++ {
		times = append(times, start.Add(time.Duration(i*5)*time.Minute).Format("2006-01-02 15:04"))
	}
	got := Aggregate(mkBars(times), 15) // 100 根：够算 EMA
	if len(got) != 100 {
		t.Fatalf("300 根 5 分钟应当聚合成 100 根 15 分钟，实际 %d", len(got))
	}
	first := -1
	for i := range got {
		if got[i].Ema20 != nil {
			first = i
			break
		}
	}
	if first != 19 {
		t.Fatalf("EMA20 应从第 20 根（下标 19）开始，实际 %d", first)
	}
}

func TestParseTimeframe(t *testing.T) {
	if tf, ok := ParseTimeframe("4h"); !ok || tf.Minutes != 240 {
		t.Fatalf("4h 解析错：%+v %v", tf, ok)
	}
	if _, ok := ParseTimeframe("7m"); ok {
		t.Fatal("不认识的周期不该被接受")
	}
}

// 接口本身：只给已揭示的部分，认不出的周期 400、实例不存在 404。
func TestTimeframeEndpoint(t *testing.T) {
	hub, root := newTestHub(t, 8)
	inst := openTestInstance(t, root, "2026-01-06 10:00", 30, 1)
	hub.mu.Lock()
	hub.instances[inst.ID] = inst
	hub.mu.Unlock()
	revealed := inst.Cursor + 1

	srv := httptest.NewServer(NewHandler(hub, t.TempDir()))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/timeframe?instance=" + inst.ID + "&timeframe=30m")
	if err != nil {
		t.Fatalf("请求失败：%v", err)
	}
	defer resp.Body.Close()
	var got TimeframeBarsMsg
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("响应不是 JSON：%v", err)
	}
	if got.Timeframe != "30m" || len(got.Bars) == 0 {
		t.Fatalf("响应不对：%+v", got)
	}
	// 聚合只可能更少，而且**不能剧透**游标之后那几根
	if len(got.Bars) > revealed {
		t.Fatalf("聚合出了 %d 根，已揭示只有 %d 根", len(got.Bars), revealed)
	}
	if last := got.Bars[len(got.Bars)-1].Time; last > inst.Bars[inst.Cursor].Time {
		t.Fatalf("最后一根 %s 越过了游标 %s", last, inst.Bars[inst.Cursor].Time)
	}

	for _, q := range []string{"timeframe=7m", "timeframe=30m&instance=不存在"} {
		r, err := http.Get(srv.URL + "/api/timeframe?" + q)
		if err != nil {
			t.Fatalf("请求失败：%v", err)
		}
		r.Body.Close()
		if r.StatusCode != http.StatusBadRequest && r.StatusCode != http.StatusNotFound {
			t.Fatalf("%s 应当报错，实际 %d", q, r.StatusCode)
		}
	}
}
