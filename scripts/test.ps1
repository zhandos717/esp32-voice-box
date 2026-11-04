# Проверка без железа: подставная плата и подставной Telegram, отдельные порты и папка tests\run\.
# Реальные записи и токен не используются (MIC_NO_DOTENV).
& (Join-Path $PSScriptRoot 'build-server.ps1')
$env:SERVER_EXE = (Resolve-Path (Join-Path $PSScriptRoot '..\server\mic_server.exe')).Path
python (Join-Path $PSScriptRoot '..\tests\run_tg_test.py')
$env:SERVER_EXE = $null
