# Собирает прошивку и заливает её на плату по USB. Нужен arduino-cli с ядром esp32:esp32.
# Пример: .\scripts\flash-firmware.ps1 -Port COM11   (порт бывает COM11 или COM12: смотрите в диспетчере устройств)
# Дальше можно обновлять по Wi-Fi: .\scripts\ota.ps1
param([string]$Port = 'COM11', [string]$Cli = 'C:\Users\dos\acli\bin\arduino-cli.exe')
if (-not (Test-Path $Cli)) { $Cli = (Get-Command arduino-cli -ErrorAction Stop).Source }
$sketch = Join-Path $PSScriptRoot '..\firmware\mic_stream'
$build = Join-Path $PSScriptRoot '..\firmware\build'
$fqbn = 'esp32:esp32:esp32'
& $Cli compile --fqbn $fqbn --output-dir $build $sketch
if ($LASTEXITCODE -eq 0) { & $Cli upload -p $Port --fqbn $fqbn --input-dir $build $sketch }
