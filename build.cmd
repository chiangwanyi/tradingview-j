@echo off
rem 本文件必须保持 CRLF 行尾（见 .gitattributes）。LF 行尾会让 cmd.exe 吃掉行首字符。
chcp 65001 >nul
cd /d "%~dp0"

echo.
echo === 构建 tv-capture.exe ===
echo.

where go >nul 2>nul
if errorlevel 1 goto :nogo

go build -o tv-capture.exe .
if errorlevel 1 goto :failed

echo 构建完成：tv-capture.exe
echo 直接双击 tv-capture.exe 即可运行（配置读同目录的 config.json）。
echo.
pause
exit /b 0

:nogo
echo 没找到 go 命令。请先安装 Go 并确认 go 在 PATH 里。
echo.
pause
exit /b 1

:failed
echo.
echo 构建失败，请检查上面的报错。
echo.
pause
exit /b 1
