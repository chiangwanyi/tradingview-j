package replay

// 每日开盘首根的识别 —— 【新增实例（开盘）】和【跳转到下一天开盘】共用这一套。
//
// 数据按北京时间存，日盘按美国时间开：夏令时 06:00、冬令时 07:00（北京）。
// 差一整点，所以既不能按「0 点」切天，也不该把 6、7 写死 —— 唯一稳定的判据是
// **盘间断档**：缺口右侧第一根就是那一天的第一根。

import "time"

// sessionGap 是盘间断档的下限。实际断档约 65 分钟（04:55/05:55 -> 06:00/07:00），
// 而数据里零星补不上的缺口只有 10~20 分钟 —— 取 30 分钟正好把两者分开。
const sessionGap = 30 * time.Minute

// 时间和 CSV 里那一列一样是明文，算差值不需要时区。
const sessionTimeLayout = "2006-01-02 15:04"

// isSessionOpen 判断 cur 是不是「那一天的第一根」：它和前一根之间隔着盘间断档。
func isSessionOpen(prev, cur string) bool {
	if prev == "" {
		return false
	}
	a, err1 := time.Parse(sessionTimeLayout, prev)
	b, err2 := time.Parse(sessionTimeLayout, cur)
	return err1 == nil && err2 == nil && b.Sub(a) > sessionGap
}

// sessionOpenIdx 在 times 里找第一根开盘首根，找不到返回 -1。
//
// prevLast 是 times 之前那根的时间：缺口可能正好落在两份数据的接缝上
// （周一/假期后的第一天，日文件直接从开盘那根开始）。空串 = 前面没有数据可比，
// 那就把 times 的第一根当开盘根（数据目录的第一天）。
func sessionOpenIdx(times []string, prevLast string) int {
	for i, t := range times {
		prev := prevLast
		if i > 0 {
			prev = times[i-1]
		}
		if prev == "" || isSessionOpen(prev, t) {
			return i
		}
	}
	return -1
}
