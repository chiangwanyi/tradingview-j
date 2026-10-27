package replay

// 周期切换：把 5 分钟 K 线在**读的时候**聚合成更大的周期。
//
// 一条硬边界：**聚合结果不落盘、不进实例存档、不进交易记录**。
// 它是一份「同一批已揭示数据 + 一个周期」的纯函数结果 —— 谁都能在任意时刻
// 重新算一遍，没有任何东西需要被保存。所以这里没有任何写路径：
// 不建目录、不写文件、不改 Instance、不动游标。
//
// 聚合规则（每一条都只有一个答案，不做可配置项）：
//
//  1. **分桶按钟点**：桶起点 = 时间向下取整到周期倍数（当天 00:00 起算）。
//     10/15/30/60/120/180/240 全都整除 1440，所以桶天然不跨日 ——
//     不需要「隔夜怎么算」这种特例，也不受时区影响（只用文本里的 HH:MM）。
//  2. 桶的 `time` = **桶内第一根 5 分钟 K 线的真实时间**，不是算出来的整点。
//     这一点是有代价也必须要这样的：`time` 是协议里 K 线的唯一标识，
//     标注锚点、成交、语音全都存这个字符串 —— 它必须真的是 5 分钟数据里的一根，
//     否则切回 5 分钟全对不上（守则会拒掉「不存在的时间」）。
//     副作用：开盘不在整点时（CME 18:00 开盘），4 小时桶的标签就是 18:00 这种
//     真实时间，而不是 16:00 这种空想整点。
//  3. OHLCV：open = 桶内第一根的开盘，high/low = 桶内极值，close = 最后一根的收盘，
//     volume = 求和。
//  4. 缺口**不补**：某个桶里一根 5 分钟都没有（休市、跳空、数据断了），就没有这根 K 线。
//     不造「平盘假 K 线」—— 那会让人以为那段时间有成交。
//  5. 最后一个桶通常是**没走完的**（只走到游标那根）：照样给出，就是正在形成的这根，
//     和看图软件里的当前 K 线一个意思。
//  6. EMA20 在聚合序列上**重新算**（不是把 5 分钟那条搬下来）：周期变了，
//     「20 周期均线」的含义也跟着变。口径复用 computeEMA（只有一份实现）。
//
// 价格不在这里重新取整：各字段本来就是 5 分钟数据里的原值（加载时已 roundPrice）。

// Timeframe 是一个可选的 K 线周期。
type Timeframe struct {
	ID      string // 协议里的取值，也是界面上的按钮文字
	Minutes int    // 一根 K 线覆盖多少分钟
}

// Timeframes 是全部可选周期，顺序即界面上的排列顺序。5m 是数据的原始周期。
var Timeframes = []Timeframe{
	{"5m", 5},
	{"10m", 10},
	{"15m", 15},
	{"30m", 30},
	{"60m", 60},
	{"2h", 120},
	{"3h", 180},
	{"4h", 240},
}

// ParseTimeframe 把协议里的周期串（"15m"）变成 Timeframe。
// 只认上面这张表 —— 界面和后端各写一套「认识哪些周期」迟早会漂。
func ParseTimeframe(id string) (Timeframe, bool) {
	for _, tf := range Timeframes {
		if tf.ID == id {
			return tf, true
		}
	}
	return Timeframe{}, false
}

// Aggregate 把按时间升序的 5 分钟 K 线聚合成 minutes 分钟一根。
//
// 永远返回新切片（空数据时是空切片而不是 nil，JSON 里是 [] 不是 null）。
func Aggregate(bars []Bar, minutes int) []Bar {
	out := make([]Bar, 0, len(bars))
	if minutes <= 0 {
		return out
	}
	started := false
	curDay := ""
	curBucket := 0
	for _, b := range bars {
		day, mins, ok := splitBarTime(b.Time)
		if !ok {
			// 时间解析不了：CSV 在加载时已经校验过，走到这里说明数据被手工改坏了。
			// 丢掉这一根（它连桶都归不进去），不猜一个位置。
			continue
		}
		bucket := mins / minutes * minutes
		if !started || day != curDay || bucket != curBucket {
			out = append(out, Bar{
				Time:   b.Time, // 桶里第一根的真实时间（见文件头第 2 条）
				Open:   b.Open,
				High:   b.High,
				Low:    b.Low,
				Close:  b.Close,
				Volume: b.Volume,
			})
			started, curDay, curBucket = true, day, bucket
			continue
		}
		last := &out[len(out)-1]
		if b.High > last.High {
			last.High = b.High
		}
		if b.Low < last.Low {
			last.Low = b.Low
		}
		last.Close = b.Close
		last.Volume += b.Volume
	}
	computeEMA(out)
	return out
}

// splitBarTime 把 "2026-10-16 09:35" 拆成日期和「当天第几分钟」。
// 只做文本切片，不经过 time.Parse —— 这里要的只是分桶，不需要时区。
func splitBarTime(t string) (day string, minutes int, ok bool) {
	if len(t) < 16 || t[10] != ' ' || t[13] != ':' {
		return "", 0, false
	}
	h := int(t[11]-'0')*10 + int(t[12]-'0')
	m := int(t[14]-'0')*10 + int(t[15]-'0')
	if t[11] < '0' || t[11] > '9' || t[12] < '0' || t[12] > '9' ||
		t[14] < '0' || t[14] > '9' || t[15] < '0' || t[15] > '9' ||
		h > 23 || m > 59 {
		return "", 0, false
	}
	return t[:10], h*60 + m, true
}
