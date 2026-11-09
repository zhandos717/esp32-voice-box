# Собирает прошивку и отправляет её на плату по Wi-Fi (через сервер). Плата должна быть подключена к серверу.
# Первый раз с поддержкой OTA прошивку нужно залить по USB: .\scripts\flash-firmware.ps1
param([string]$Cli = 'C:\Users\dos\acli\bin\arduino-cli.exe', [string]$Web = 'http://127.0.0.1:8801')
if (-not (Test-Path $Cli)) { $Cli = (Get-Command arduino-cli -ErrorAction Stop).Source }
$root = Join-Path $PSScriptRoot '..'
$sketch = Join-Path $root 'firmware\mic_stream'
$out = Join-Path $root 'firmware\build'
& (Join-Path $PSScriptRoot 'get-libs.ps1')
& $Cli compile --fqbn esp32:esp32:esp32 --libraries (Join-Path $root 'firmware\libraries') --output-dir $out $sketch
if ($LASTEXITCODE -ne 0) { exit 1 }
$before = (Invoke-RestMethod "$Web/api/status").fw
Write-Host "на плате сейчас: $before"
$r = Invoke-RestMethod -Method Post "$Web/api/ota"
if (-not $r.ok) { Write-Host "ошибка: $($r.error)"; exit 1 }
Write-Host "отправлено $([math]::Round($r.bytes/1024)) КБ, жду перезапуск платы..."
$ok = $false
for ($i = 0; $i -lt 90; $i++) {
    Start-Sleep 2
    try { $s = Invoke-RestMethod "$Web/api/status" -TimeoutSec 3; if ($s.connected -and -not $s.otaBusy -and $s.fw -and $s.fw -ne $before) { $ok = $true; break } } catch {}
}
if ($ok) { Write-Host "готово, на плате: $($s.fw)" } else { Write-Host 'версия на плате не изменилась (смотрите server.log)'; exit 1 }
