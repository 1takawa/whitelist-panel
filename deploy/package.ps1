# Builds the release bundle in ..\dist: frontend, Linux binary and the installer.
#   powershell -File deploy\package.ps1 [-Version 0.9.0]
param([string]$Version = "0.9.0")
$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
Set-Location $root

$goBin = Join-Path $env:LOCALAPPDATA 'Programs\go\bin'
if (Test-Path $goBin) { $env:Path = "$goBin;$env:Path" }

Push-Location frontend
npm ci
npm run build
Pop-Location

go vet ./...
go test ./...

New-Item -ItemType Directory -Force dist | Out-Null
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
try {
  go build -trimpath -ldflags "-s -w -X main.version=$Version" -o dist\anypanel .\cmd\anypanel
} finally {
  Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED -ErrorAction SilentlyContinue
}
# The installer must keep LF line endings: bash on Linux fails on CRLF.
$sh = [IO.File]::ReadAllText("deploy\install.sh") -replace "`r`n", "`n"
[IO.File]::WriteAllText((Join-Path $root "dist\install.sh"), $sh)
$sh2 = [IO.File]::ReadAllText("internal\nodeinstall\install-node.sh") -replace "`r`n", "`n"
[IO.File]::WriteAllText((Join-Path $root "dist\install-node.sh"), $sh2)

Get-ChildItem dist | Select-Object Name, @{n='MB';e={[math]::Round($_.Length/1MB,1)}}
Write-Host "`nCopy dist\ to the server and run:  bash install.sh --panel-domain P --reality-domain R"
