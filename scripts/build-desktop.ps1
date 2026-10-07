[CmdletBinding()]
param(
    [string]$Version = "0.3.6-desktop-preview",
    [string]$Commit = "none",
    [string]$BuildDate = "unknown",
    [ValidateSet("amd64", "arm64")][string]$Architecture = "amd64",
    [string]$OutputDirectory = "dist/desktop/v0.3.6",
    [string]$WailsExecutable = ""
)
$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $ProjectRoot
try {
    if ($WailsExecutable) { $WailsExecutable = (Resolve-Path -LiteralPath $WailsExecutable).Path }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "共享业务测试失败" }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw "共享业务静态检查失败" }
    node --test scripts/test-desktop.mjs scripts/test-desktop-ui.mjs scripts/test-web.mjs
    if ($LASTEXITCODE -ne 0) { throw "页面测试失败" }
    go run ./scripts/desktop-icon
    if ($LASTEXITCODE -ne 0) { throw "图标生成失败" }
    Push-Location desktop
    try {
        go test ./...
        if ($LASTEXITCODE -ne 0) { throw "桌面模块测试失败" }
        go vet ./...
        if ($LASTEXITCODE -ne 0) { throw "桌面模块静态检查失败" }
        $Ldflags = "-s -w -X github.com/itrunswap/Kivo/internal/version.Version=$Version -X github.com/itrunswap/Kivo/internal/version.Commit=$Commit -X github.com/itrunswap/Kivo/internal/version.Date=$BuildDate"
        # 不清理历史产物；框架生成 DPI 清单与图标，WebView2 缺失时打开微软安装页面。
        # 可使用预先安装的固定版本 CLI 离线构建，避免 go run 每次查询远端模块元数据。
        if ($WailsExecutable) {
            $WailsVersion = & $WailsExecutable version
            if ($LASTEXITCODE -ne 0 -or "$WailsVersion" -notmatch '^v?2\.15\.0(?:\s|$)') { throw "需要 Wails v2.15.0 CLI" }
            & $WailsExecutable build -s -skipbindings -trimpath -webview2 browser -platform "windows/$Architecture" -ldflags $Ldflags
        } else {
            go run github.com/wailsapp/wails/v2/cmd/wails@v2.15.0 build -s -skipbindings -trimpath -webview2 browser -platform "windows/$Architecture" -ldflags $Ldflags
        }
        if ($LASTEXITCODE -ne 0) { throw "Wails 桌面构建失败" }
        Push-Location $ProjectRoot
        try {
            go run ./scripts/desktop-licenses
            if ($LASTEXITCODE -ne 0) { throw "桌面依赖许可收集失败" }
        }
        finally { Pop-Location }
        $OutputRoot = [IO.Path]::GetFullPath((Join-Path $ProjectRoot $OutputDirectory))
        New-Item -ItemType Directory -Force -Path $OutputRoot | Out-Null
        $Target = Join-Path $OutputRoot "kivo-desktop-windows-$Architecture.exe"
        Copy-Item -LiteralPath "build/bin/kivo-desktop.exe" -Destination $Target -Force
        $PackageRoot = Join-Path $OutputRoot "windows-$Architecture"
        New-Item -ItemType Directory -Force -Path $PackageRoot | Out-Null
        Copy-Item -LiteralPath $Target -Destination (Join-Path $PackageRoot "kivo-desktop.exe") -Force
        # 配置结构同步升级时一并提供 CLI，避免用户继续用旧 CLI 写回新配置。
        Push-Location $ProjectRoot
        try {
            $OldGOARCH = $env:GOARCH
            try {
                $env:GOARCH = $Architecture
                go build -trimpath -ldflags $Ldflags -o (Join-Path $PackageRoot "kivo.exe") ./cmd/kivo
                if ($LASTEXITCODE -ne 0) { throw "配套 CLI 构建失败" }
            } finally { $env:GOARCH = $OldGOARCH }
        } finally { Pop-Location }
        Copy-Item -LiteralPath (Join-Path $ProjectRoot "docs/DESKTOP.md") -Destination (Join-Path $PackageRoot "桌面端使用说明.md") -Force
        Copy-Item -LiteralPath (Join-Path $ProjectRoot "docs/INSTALL.md") -Destination (Join-Path $PackageRoot "安装与升级说明.md") -Force
        Copy-Item -LiteralPath (Join-Path $ProjectRoot "THIRD_PARTY_NOTICES.md") -Destination $PackageRoot -Force
        Copy-Item -LiteralPath (Join-Path $ProjectRoot "desktop/THIRD_PARTY_LICENSES.txt") -Destination $PackageRoot -Force
        Copy-Item -LiteralPath (Join-Path $ProjectRoot "LICENSE") -Destination $PackageRoot -Force
        Compress-Archive -LiteralPath $PackageRoot -DestinationPath (Join-Path $OutputRoot "kivo-desktop-windows-$Architecture.zip") -Force
        $Hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $OutputRoot "kivo-desktop-windows-$Architecture.zip")).Hash.ToLowerInvariant()
        Set-Content -LiteralPath (Join-Path $OutputRoot "SHA256SUMS-windows-$Architecture") -Encoding utf8NoBOM -Value "$Hash  kivo-desktop-windows-$Architecture.zip"
        Write-Host "桌面程序：$Target"
    }
    finally { Pop-Location }
}
finally { Pop-Location }
