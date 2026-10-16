@echo off
rem ---------------------------------------------------------------------------
rem This script must stay PURE ASCII, and CRLF. Both are load-bearing:
rem   CRLF  - cmd.exe re-reads a .cmd by byte offset; a bare LF shifts every
rem           line and the leading characters get eaten.
rem   ASCII - multi-byte characters make cmd's byte offsets disagree with the
rem           code page it decodes them with; the symptom is the TAIL of a
rem           line being run on its own:
rem           'xxx' is not recognized as an internal or external command
rem           Measured 2026-10-16: the old script had 237 non-ASCII bytes and
rem           worked; the first rewrite had 1071 and broke halfway through.
rem So: no Chinese in here, not even in comments. Only this script is English,
rem the program itself still prints Chinese. See docs/testing.md.
rem ---------------------------------------------------------------------------
chcp 65001 >nul
cd /d "%~dp0"

rem ==== version: the one and only source is the VERSION file of this folder ====
rem set /p reads the FIRST line only, so VERSION must stay a single line.
set "VER="
set /p VER=<VERSION
if not defined VER goto :nover

echo.
echo === Building tv-capture-v%VER%.exe ===
echo.

where go >nul 2>nul
if errorlevel 1 goto :nogo

rem -- 1/2 resource object: this is what fills the exe's Details tab ----
rem rsrc_windows_amd64.syso is a compile-time snapshot, so it gets regenerated
rem here on every build, and it is neither hand-edited nor committed (.gitignore).
rem amd64 only - add another --arch if you ever build arm64/386 Windows.
where go-winres >nul 2>nul
if errorlevel 1 goto :nowinres
go-winres make --in winres\winres.json --out rsrc --arch amd64 --file-version=%VER% --product-version=%VER%
if errorlevel 1 goto :failed

rem -- 2/2 build: inject the version and put it in the file name ----
rem The exe lands in THIS folder, beside config.json / data / raw, so it reads and
rem writes exactly the same data as before - outDir and rawDir are relative to the
rem exe's own folder (see internal/config). There is no dist folder any more and
rem nothing gets copied around.
go build -ldflags "-X main.version=%VER%" -o "tv-capture-v%VER%.exe" .
if errorlevel 1 goto :failed

echo Build OK: tv-capture-v%VER%.exe
echo   - version %VER% comes from the VERSION file: injected with -X main.version
echo     and also written into the exe file properties
echo   - built in place, so the exe reads config.json from this folder and uses
echo     the data\ and raw\ folders here as well
echo.
pause
exit /b 0

:nover
echo Cannot read the VERSION file - it should hold one line, e.g. 0.1.0.
echo.
pause
exit /b 1

:nogo
echo 'go' was not found. Install Go and make sure it is on PATH.
echo.
pause
exit /b 1

:nowinres
echo 'go-winres' was not found - it is what writes the version into the exe
echo file properties. Install it once:
echo     go install github.com/tc-hib/go-winres@latest
echo then make sure %%USERPROFILE%%\go\bin is on PATH and run this again.
echo.
pause
exit /b 1

:failed
echo.
echo Build failed - see the error above.
echo.
pause
exit /b 1
