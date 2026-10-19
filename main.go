// tradingview-j —— 把 TradingView 图表页收到的 K 线旁路抓下来，按标的+日期落成 CSV。
//
// 使用方式只有一种：双击（或终端里运行）这个 exe。程序**不接受任何命令行参数**，
// 所有配置都在 exe 同目录的 config.json 里。
//
// 运行起来是一个五选一的菜单：
//
//	1  启动抓取  —— 等 Chrome 的调试端口 -> 等 TradingView 图表页 -> 被动监听 WebSocket 帧
//	                -> 解出 5 分钟 K 线 -> 覆盖写 <数据目录>/<标的>/<yyyy-MM-dd>.csv
//	2  分析数据  —— 只读地扫一遍数据目录，报每个标的每天有多少根、缺口在哪
//	3  网页版分析 —— 同一个统计结果起在 127.0.0.1 的本地网页上（可排序、可筛选）
//	4  复盘      —— 本地网页 + WebSocket：逐根回放、画标注、模拟下单（不写回 CSV）
//	5  退出
//
// 抓取停下来（Ctrl+C）之后是**回到菜单**，不是退出程序 —— 想接着抓就再选 1。
// 网页服务同理：Ctrl+C 停止服务并回到菜单。
//
// 这个文件刻意只做「启动」一件事：横幅、菜单循环、三个菜单项的编排都在
// internal/cli；协议与落盘在 internal/ 的其他包。
package main

import (
	"os"

	"tradingview-j/internal/cli"
)

// version 是程序版本号，**唯一来源是根目录的 VERSION 文件**。
//
// 这个字面量只是「没有构建脚本参与」时的兜底值（IDE 里直接跑、手敲 go build）；
// build.cmd 会用 -ldflags "-X main.version=<VERSION 的内容>" 覆盖它，
// 并拿同一个值去命名产物、写进 exe 的文件属性。三处对不上时 version_test.go
// 会当场报错 —— 所以**升版本只需要改 VERSION 这一个文件**。
//
// 它必须留在 main 包：-X 只能设置包级字符串变量，而 build.cmd 里写死的是
// -X main.version（version_test.go 会把这一条钉住）。所以是把这个值**传进去**，
// 而不是把启动逻辑留在这里。
var version = "0.7.0"

func main() {
	os.Exit(cli.Run(version))
}
