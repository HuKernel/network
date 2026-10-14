$projectRoot = Split-Path -Parent $MyInvocation.MyCommand.Path
$backendDir = Join-Path $projectRoot "backend"
$frontendDir = Join-Path $projectRoot "frontend"
$goExe = "C:\Program Files\Go\bin\go.exe"

if (-not (Test-Path $goExe)) {
  Write-Error "Go not found: $goExe"
  exit 1
}

if (-not (Test-Path (Join-Path $backendDir "go.mod"))) {
  Write-Error "Backend go.mod not found: $backendDir"
  exit 1
}

if (-not (Test-Path (Join-Path $frontendDir "package.json"))) {
  Write-Error "Frontend package.json not found: $frontendDir"
  exit 1
}

$backendCmd = @"
Set-Location '$backendDir'
`$env:GOPROXY='https://goproxy.cn,direct'
& '$goExe' run .\cmd\lan-share
"@

$frontendCmd = @"
Set-Location '$frontendDir'
npm run dev -- --host 0.0.0.0
"@

Start-Process powershell -ArgumentList "-NoExit", "-Command", $backendCmd
Start-Process powershell -ArgumentList "-NoExit", "-Command", $frontendCmd

Write-Host "Backend started: http://localhost:8080"
Write-Host "Frontend started: http://localhost:5173"
Write-Host "First run: open the frontend page and create the admin account."
