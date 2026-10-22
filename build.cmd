@echo off
rem ---------------------------------------------------------------------------
rem One entry, two builds: the replay page first (npm run build inside web\), then
rem the exe. A failure in either stage stops the run here with exit /b 1, so a
rem half-built product is never reported as a success.
rem
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
rem
rem Two promises version_test.go checks on the EXECUTED lines below (rem lines
rem are ignored): the exe stays in this folder, and nothing is copied or moved.
rem The frontend build does not touch either -- it only writes its own output
rem folder under web\, which is served at runtime. Keep that split when editing.
rem ---------------------------------------------------------------------------
chcp 65001 >nul
cd /d "%~dp0"

rem -- optional switch: build the exe only, skip the frontend ----
rem Exists so the old go-only loop is still one keystroke away; without it both
rem stages run.
set "SKIP_WEB=0"
if /i "%~1"=="--no-web" set "SKIP_WEB=1"

rem ==== version: the one and only source is the VERSION file of this folder ====
rem set /p reads the FIRST line only, so VERSION must stay a single line.
set "VER="
set /p VER=<VERSION
if not defined VER goto :nover

echo.
echo === Building tv-capture-v%VER%.exe ===
echo.

rem Checked before the frontend stage on purpose: a missing toolchain should be
rem reported in a second, not after a minute of node work.
where go >nul 2>nul
if errorlevel 1 goto :nogo

rem -- 1/3 frontend: the page menu 3 serves at runtime ----
rem It is a build artifact, not source: the replay server reads it off disk when
rem it starts, so a stale one silently shows yesterday's page. That is why it is
rem built here instead of being left to a note in the report.
if "%SKIP_WEB%"=="1" echo -- 1/3 frontend: skipped, building the exe only.
if "%SKIP_WEB%"=="1" goto :webdone

where node >nul 2>nul
if errorlevel 1 goto :nonode
if not exist "web\package.json" goto :noweb

if exist "web\node_modules" goto :webdeps
echo -- 1/3 frontend: web\node_modules is missing, running npm install once ...
call :run_npm "install"
if errorlevel 1 goto :webdepsfailed

:webdeps
echo -- 1/3 frontend: npm run build ...
call :run_npm "run build"
if errorlevel 1 goto :webfailed
echo -- frontend OK
echo.

:webdone

rem -- 2/3 resource object: this is what fills the exe's Details tab ----
rem rsrc_windows_amd64.syso is a compile-time snapshot, so it gets regenerated
rem here on every build, and it is neither hand-edited nor committed (.gitignore).
rem amd64 only - add another --arch if you ever build arm64/386 Windows.
where go-winres >nul 2>nul
if errorlevel 1 goto :nowinres
go-winres make --in winres\winres.json --out rsrc --arch amd64 --file-version=%VER% --product-version=%VER%
if errorlevel 1 goto :failed

rem -- 3/3 build: inject the version and put it in the file name ----
rem The exe lands in THIS folder, beside config.json / data / raw, so it reads and
rem writes exactly the same data as before - outDir and rawDir are relative to the
rem exe's own folder (see internal/config). Nothing gets copied around.
go build -ldflags "-X main.version=%VER%" -o "tv-capture-v%VER%.exe" .
if errorlevel 1 goto :failed

echo Build OK: tv-capture-v%VER%.exe
echo   - version %VER% comes from the VERSION file: injected with -X main.version
echo     and also written into the exe file properties
echo   - built in place, so the exe reads config.json from this folder and uses
echo     the data\ and raw\ folders here as well
if "%SKIP_WEB%"=="1" echo   - frontend was skipped (--no-web): menu 3 replays whatever was built last time
if "%SKIP_WEB%"=="0" echo   - frontend rebuilt in the same run, so menu 3 replays the current page
echo.
pause
exit /b 0

rem ==== helper: run one npm command inside web\, return its exit code ========
rem A subroutine, because cmd hands control to the npm shim and never comes back
rem here unless the call is explicit. %1 is what follows "npm": "install",
rem "run build". Kept as a quoted argument so the space survives.
:run_npm
pushd "web"
call npm %~1
set "NPM_RC=%errorlevel%"
popd
exit /b %NPM_RC%

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

:nonode
echo 'node' was not found - the replay page is built with npm. Install Node.js
echo and make sure it is on PATH, or run: build.cmd --no-web
echo.
pause
exit /b 1

:noweb
echo web\package.json is missing - the frontend sources are not here, so the
echo replay page cannot be built. Restore web\, or run: build.cmd --no-web
echo.
pause
exit /b 1

:webdepsfailed
echo.
echo Frontend setup failed: npm install returned an error - see above.
echo Nothing was built. Fix the network or the lock file, then run this again.
echo.
pause
exit /b 1

:webfailed
echo.
echo Frontend build failed: npm run build returned an error - see above
echo (a type error in web\src\ fails there too, before vite runs).
echo The exe was NOT rebuilt, so no half-new product was left behind.
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
