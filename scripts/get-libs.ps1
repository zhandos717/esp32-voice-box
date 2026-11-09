# Скачивает библиотеки прошивки в firmware\libraries (зафиксированная версия, в git не попадает).
$dir = Join-Path $PSScriptRoot '..\firmware\libraries'
New-Item -ItemType Directory -Force $dir | Out-Null
if (-not (Test-Path "$dir\codec-opus")) { git clone -q https://github.com/pschatzmann/codec-opus.git "$dir\codec-opus" }
git -C "$dir\codec-opus" -c advice.detachedHead=false checkout -q c02de63
