# Собирает server\mic_server.exe. Go ищется в C:\Users\dos\go-sdk (портативный), иначе в PATH.
$go = 'C:\Users\dos\go-sdk\go\bin\go.exe'
if (-not (Test-Path $go)) { $go = (Get-Command go -ErrorAction Stop).Source }
$env:GOTOOLCHAIN = 'local'
Push-Location (Join-Path $PSScriptRoot '..\server')
try {
    & $go vet ./...
    & $go build -o mic_server.exe .
    if ($LASTEXITCODE -eq 0) { Write-Host 'server\mic_server.exe built' }
} finally { Pop-Location }
