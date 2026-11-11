# Builds Cairn into bin\cairn.exe: a windowed program, without a console,
# stripped. -E2E also builds bin\cairn-e2e.exe, which another process can
# drive through MyGo's automation plugin (see docs\design.md).
param([switch]$E2E)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$env:CGO_ENABLED = '0'
go build -trimpath -ldflags '-H windowsgui -s -w' -o bin\cairn.exe .\cmd\cairn
if ($E2E) {
    go build -tags e2e -ldflags '-H windowsgui' -o bin\cairn-e2e.exe .\cmd\cairn
    Push-Location third_party\mygo
    go build -o ..\..\bin\mygo-automation.exe .\plugins\automation\cmd\mygo-automation
    Pop-Location
}
Get-Item bin\cairn.exe | Select-Object Name, @{n='MB';e={[math]::Round($_.Length/1MB,1)}}
