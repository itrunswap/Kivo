[CmdletBinding()]
param(
    [ValidateSet("amd64", "arm64")][string]$Architecture = "amd64",
    [string]$OutputDirectory = "dist/desktop/windows"
)
$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$PackageRoot = [IO.Path]::GetFullPath((Join-Path $ProjectRoot (Join-Path $OutputDirectory "windows-$Architecture")))
$OutputRoot = [IO.Path]::GetFullPath((Join-Path $ProjectRoot $OutputDirectory))
foreach ($name in @("kivo-desktop.exe", "kivo.exe", "LICENSE", "THIRD_PARTY_NOTICES.md", "THIRD_PARTY_LICENSES.txt", "桌面端使用说明.md", "安装与升级说明.md")) {
    if (-not (Test-Path -LiteralPath (Join-Path $PackageRoot $name) -PathType Leaf)) { throw "安装包缺少 $name" }
}
$Nsis = Get-Command makensis.exe -ErrorAction SilentlyContinue
$NsisPath = if ($Nsis) { $Nsis.Source } else { Join-Path ${env:ProgramFiles(x86)} 'NSIS\makensis.exe' }
if (-not (Test-Path -LiteralPath $NsisPath -PathType Leaf)) { throw "缺少 NSIS makensis.exe；请安装 NSIS 或使用 ZIP 便携包" }
New-Item -ItemType Directory -Force -Path $OutputRoot | Out-Null
$Installer = Join-Path $OutputRoot "Kivo-Setup-windows-$Architecture.exe"
& $NsisPath "/INPUTCHARSET" "UTF8" "/DPACKAGE_DIR=$PackageRoot" "/DOUTPUT_FILE=$Installer" (Join-Path $PSScriptRoot "windows-installer.nsi")
if ($LASTEXITCODE -ne 0 -or -not (Test-Path -LiteralPath $Installer)) { throw "NSIS 安装包构建失败" }
Write-Host "Windows 一体安装包：$Installer"
