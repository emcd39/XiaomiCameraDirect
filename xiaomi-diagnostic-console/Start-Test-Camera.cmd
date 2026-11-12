@echo off
setlocal
pushd "%~dp0"
if errorlevel 1 exit /b 1

set "CONFIG=config.live.json"
if not "%~1"=="" set "CONFIG=%~1"

if not exist "bin\diagconsole-current.exe" (
  echo Missing bin\diagconsole-current.exe. Build the console first with scripts\Build-Windows.ps1.
  popd
  exit /b 2
)

if "%~1"=="" (
  powershell.exe -NoLogo -NoProfile -ExecutionPolicy Bypass -File "%~dp0scripts\Prepare-Test-Camera.ps1"
  if errorlevel 1 (
    echo Could not start or verify the local test gateway.
    echo Check the gateway diagnostic above and the private run log before retrying.
    popd
    exit /b 5
  )
)

if not exist "%CONFIG%" (
  echo Missing config: "%CONFIG%"
  echo Without an argument, this launcher prepares config.live.json automatically.
  echo Or pass a prepared config path: Start-Test-Camera.cmd path\to\config.json
  popd
  exit /b 3
)

echo Starting the live diagnostic console with "%CONFIG%".
echo The console will print its actual loopback URL after a successful bind. Stop with Ctrl+C.
echo The page loads the camera inventory once and prepares the selected C201 session.
echo One arrow click sends one PTZ step through the managed gateway.
echo.

"%~dp0bin\diagconsole-current.exe" -config "%CONFIG%"
set "EXIT_CODE=%ERRORLEVEL%"
popd
exit /b %EXIT_CODE%
