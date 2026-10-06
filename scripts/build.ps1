[CmdletBinding()]
param(
    [string]$Version = "dev",
    [string]$Commit = "none",
    [string]$BuildDate = "unknown",
    [string]$OutputDirectory = "dist"
)

$ErrorActionPreference = "Stop"
$ProjectRoot = Split-Path -Parent $PSScriptRoot
$DistRoot = [System.IO.Path]::GetFullPath((Join-Path $ProjectRoot $OutputDirectory))
$Ldflags = "-s -w -X github.com/itrunswap/Kivo/internal/version.Version=$Version -X github.com/itrunswap/Kivo/internal/version.Commit=$Commit -X github.com/itrunswap/Kivo/internal/version.Date=$BuildDate"

Push-Location $ProjectRoot
try {
    # 只检查项目源码；开发者可能把便携 Go 工具链放在被忽略的 .tools 目录。
    $Unformatted = gofmt -l cmd internal scripts
    if ($LASTEXITCODE -ne 0) { throw "gofmt 检查失败" }
    if ($Unformatted) {
        throw "以下 Go 文件尚未格式化：`n$($Unformatted -join "`n")"
    }
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "Go 测试未通过，中止发布" }
    go vet ./...
    if ($LASTEXITCODE -ne 0) { throw "Go 静态检查未通过，中止发布" }

    New-Item -ItemType Directory -Force -Path $DistRoot | Out-Null
    $Targets = @(
        @{ OS = "windows"; Arch = "amd64"; Ext = ".exe" },
        @{ OS = "windows"; Arch = "arm64"; Ext = ".exe" },
        @{ OS = "darwin"; Arch = "amd64"; Ext = "" },
        @{ OS = "darwin"; Arch = "arm64"; Ext = "" },
        @{ OS = "linux"; Arch = "amd64"; Ext = "" },
        @{ OS = "linux"; Arch = "arm64"; Ext = "" }
    )
    foreach ($Target in $Targets) {
        $env:CGO_ENABLED = "0"
        $env:GOOS = $Target.OS
        $env:GOARCH = $Target.Arch
        $Output = Join-Path $DistRoot "kivo-$($Target.OS)-$($Target.Arch)$($Target.Ext)"
        go build -trimpath -ldflags $Ldflags -o $Output ./cmd/kivo
        if ($LASTEXITCODE -ne 0) { throw "构建 $($Target.OS)/$($Target.Arch) 失败" }
        Write-Host "已构建 $Output"
    }

    # 只校验本次六个平台的产物。dist 中可能保留旧预览或历史程序，不能用通配符混入清单。
    $Checksums = $Targets |
        ForEach-Object { "kivo-$($_.OS)-$($_.Arch)$($_.Ext)" } |
        Sort-Object |
        ForEach-Object {
            $Hash = (Get-FileHash -Algorithm SHA256 -LiteralPath (Join-Path $DistRoot $_)).Hash.ToLowerInvariant()
            "$Hash  $_"
        }
    Set-Content -LiteralPath (Join-Path $DistRoot "SHA256SUMS") -Value $Checksums -Encoding utf8NoBOM
    Write-Host "已生成 $(Join-Path $DistRoot 'SHA256SUMS')"
}
finally {
    Remove-Item Env:GOOS -ErrorAction SilentlyContinue
    Remove-Item Env:GOARCH -ErrorAction SilentlyContinue
    Remove-Item Env:CGO_ENABLED -ErrorAction SilentlyContinue
    Pop-Location
}
