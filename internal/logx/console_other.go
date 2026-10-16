//go:build !windows

package logx

// 非 Windows 平台不需要处理控制台代码页：终端本来就是 UTF-8。
func init() {}
