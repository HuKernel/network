# 构建单文件可执行程序：前端 dist 内嵌进 Go 二进制，产物在 release\lan-share.exe
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $MyInvocation.MyCommand.Path
$frontend = Join-Path $root 'frontend'
$backend = Join-Path $root 'backend'
$release = Join-Path $root 'release'
$goExe = 'C:\Program Files\Go\bin\go.exe'
if (-not (Test-Path $goExe)) { $goExe = 'go' }

Push-Location $frontend
npm run build
Pop-Location
if ($LASTEXITCODE -ne 0) { throw 'frontend build failed' }

$web = Join-Path $backend 'cmd\lan-share\web'
Remove-Item $web -Recurse -Force -ErrorAction SilentlyContinue
New-Item -ItemType Directory -Path $web | Out-Null
Copy-Item (Join-Path $frontend 'dist\*') $web -Recurse

New-Item -ItemType Directory -Path $release -Force | Out-Null
Push-Location $backend
$env:GOPROXY = 'https://goproxy.cn,direct'
& $goExe build -trimpath -ldflags '-s -w' -o (Join-Path $release 'lan-share.exe') .\cmd\lan-share
Pop-Location
if ($LASTEXITCODE -ne 0) { throw 'backend build failed' }

Write-Host "构建完成: $release\lan-share.exe"
Write-Host '拷到任意 Windows/Linux 机器运行，其他设备浏览器访问 http://<主机IP>:8080'
