# Builds MyFiles into bin\myfiles.exe with MyGo's packager: a windowed program,
# stripped, with its icon, its manifest and its version in it. -E2E also
# builds bin\myfiles-e2e.exe, which another process can drive through MyGo's
# automation plugin, and the tools that check it on the real desktop (see
# docs\design.md).
param([switch]$E2E)
$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot
$env:CGO_ENABLED = '0'
if (-not (Test-Path third_party\mygo\go.mod) -or -not (Test-Path internal\ghostty\ghostty-vt-windows-amd64.dll)) { & .\tools\setup-mygo.ps1 }
Push-Location third_party\mygo
go run .\cmd\mygo build -skip-installer ..\..\cmd\myfiles
if ($LASTEXITCODE -ne 0) { Pop-Location; exit 1 }
if ($E2E) { go build -o ..\..\bin\mygo-automation.exe .\plugins\automation\cmd\mygo-automation }
Pop-Location
Copy-Item bin\dist\windows-amd64\MyFiles.exe bin\myfiles.exe -Force
if ($E2E) {
    go build -tags e2e -ldflags '-H windowsgui' -o bin\myfiles-e2e.exe .\cmd\myfiles
    go build -o bin\deskcheck.exe .\tools\deskcheck
    go build -o bin\expbench.exe .\tools\expbench
}
Get-Item bin\myfiles.exe | Select-Object Name, @{n='MB';e={[math]::Round($_.Length/1MB,1)}}
