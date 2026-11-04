# Запускает сервер в фоне из корня проекта (так он находит .env). Записи по умолчанию лежат в recordings\ проекта;
# чтобы использовать другую папку: $env:MIC_REC_DIR = 'путь' перед запуском.
param([switch]$Foreground)
$root = Resolve-Path (Join-Path $PSScriptRoot '..')
$exe = Join-Path $root 'server\mic_server.exe'
if (-not (Test-Path $exe)) { & (Join-Path $PSScriptRoot 'build-server.ps1') }
Get-Process mic_server -ErrorAction SilentlyContinue | Stop-Process -Force
if ($Foreground) { Set-Location $root; & $exe; return }
Start-Process -FilePath $exe -WorkingDirectory $root -WindowStyle Hidden -RedirectStandardError (Join-Path $root 'server.log')
Start-Sleep 2
Get-Content (Join-Path $root 'server.log') -Tail 4
Write-Host 'http://127.0.0.1:8801'
