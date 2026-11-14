# Fetches what MyFiles builds against and does not keep in its repository:
#
#   - MyGo, at the commit MyFiles uses, into third_party\mygo, with
#     MyFiles's changes applied to it (patches\mygo.patch); go.mod points
#     at the result;
#   - the terminal's emulator, Ghostty's libghostty-vt, into
#     internal\ghostty, where the program embeds it from: the build MyGo's
#     terminal plugin names, checked against the hash it names.
#
# Run once after cloning, and again when either changes; what is there
# already is left.
$ErrorActionPreference = 'Stop'
$commit = 'd40e0b7a66d1f7d40ab437861b506afd38f1519c'
$root = Split-Path $PSScriptRoot -Parent
$dir = Join-Path $root 'third_party\mygo'
if (Test-Path (Join-Path $dir 'go.mod')) {
    Write-Host "third_party\mygo is there already; delete it to fetch it again."
} else {
    New-Item -ItemType Directory -Force $dir | Out-Null
    git -C $dir init -q
    git -C $dir remote add origin https://github.com/egoist/mygo.git
    git -C $dir fetch -q --depth 1 origin $commit
    git -C $dir checkout -q FETCH_HEAD
    git -C $dir apply (Join-Path $root 'patches\mygo.patch')
    Write-Host "MyGo $($commit.Substring(0,7)) with MyFiles's patch is in third_party\mygo."
}

$manifest = Get-Content -Raw (Join-Path $dir 'plugins\terminal\mygo-plugin.json') | ConvertFrom-Json
$lib = $manifest.libraries[0].files.'windows-amd64'
$sum = $lib.sha256.ToLower()
$dll = Join-Path $root 'internal\ghostty\ghostty-vt-windows-amd64.dll'
function Test-Sum($path) { (Test-Path $path) -and ((Get-FileHash $path -Algorithm SHA256).Hash.ToLower() -eq $sum) }
if (Test-Sum $dll) {
    Write-Host "internal\ghostty has libghostty-vt already."
} else {
    # MyGo's own tools keep the builds they fetched here.
    $cached = Join-Path $env:LOCALAPPDATA "mygo\natives\$sum\$($lib.name)"
    if (Test-Sum $cached) {
        Copy-Item $cached $dll -Force
    } else {
        Invoke-WebRequest -Uri $lib.url -OutFile $dll -UseBasicParsing
    }
    if (-not (Test-Sum $dll)) {
        Remove-Item $dll -ErrorAction SilentlyContinue
        throw "libghostty-vt from $($lib.url) is not the build the terminal plugin names ($sum)"
    }
    Write-Host "libghostty-vt $($sum.Substring(0,12)) is in internal\ghostty."
}
