@echo off
rem Build/update procmem.exe in this directory.
setlocal
set CGO_ENABLED=0
cd /d "%~dp0"
go vet . || exit /b 1
go test . || exit /b 1
go build -trimpath -ldflags="-s -w" -o procmem.exe . || exit /b 1
echo OK: %~dp0procmem.exe
