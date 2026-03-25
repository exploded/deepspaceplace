@echo off
setlocal enabledelayedexpansion

REM Check for .env file
if not exist .env (
    echo ERROR: .env file not found. Copy .env.example to .env and configure.
    exit /b 1
)

REM Load environment variables from .env
for /f "usebackq tokens=1,* delims==" %%a in (".env") do (
    set "line=%%a"
    if not "!line:~0,1!"=="#" (
        set "%%a=%%b"
    )
)

echo Building deepspaceplace...
go build -o deepspaceplace.exe ./cmd/server/
if %ERRORLEVEL% neq 0 (
    echo BUILD FAILED
    exit /b 1
)

echo Starting deepspaceplace on port %PORT%...
deepspaceplace.exe
