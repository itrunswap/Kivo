[CmdletBinding()]
param([switch]$Direct)

# 只向官方字体服务发送源码中的公开界面字符，不读取用户配置或订阅。
# 运行时无需此脚本或网络。更新文案后，开发者可重新生成离线 UI 字体子集。
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
$fontDirectory = Join-Path $projectRoot 'internal/server/assets/fonts'
$fontTarget = Join-Path $fontDirectory 'noto-sans-sc-ui.woff2'
$sourceText = (Get-Content (Join-Path $projectRoot 'internal/server/assets/index.html') -Raw) +
              (Get-Content (Join-Path $projectRoot 'internal/server/assets/app.js') -Raw)
$chinese = -join ($sourceText.ToCharArray() | Where-Object { [int]$_ -ge 0x4e00 -and [int]$_ -le 0x9fff } | Sort-Object -Unique)
$uiCharacters = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789.,:;!?/()[]+-·—…，。：！？（）' + $chinese
$fontQuery = 'https://fonts.googleapis.com/css2?family=Noto+Sans+SC:wght@300..700&display=swap&text=' + [Uri]::EscapeDataString($uiCharacters)
$requestOptions = @{ TimeoutSec = 60 }
if ($Direct) { $requestOptions.NoProxy = $true }
$css = Invoke-WebRequest -Uri $fontQuery @requestOptions -Headers @{
    'User-Agent' = 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36'
}
$fontMatch = [regex]::Match($css.Content, 'url\((https://fonts\.gstatic\.com/[^)]+)\)')
if (-not $fontMatch.Success) { throw '官方响应中没有预期的 WOFF2 字体地址。' }
New-Item -ItemType Directory -Force -Path $fontDirectory | Out-Null
$temporaryFont = Join-Path $fontDirectory ('ui-' + [Guid]::NewGuid().ToString('N') + '.tmp')
try {
    Invoke-WebRequest -Uri $fontMatch.Groups[1].Value @requestOptions -OutFile $temporaryFont
    $fontBytes = [IO.File]::ReadAllBytes($temporaryFont)
    if ($fontBytes.Length -lt 4 -or [Text.Encoding]::ASCII.GetString($fontBytes,0,4) -ne 'wOF2') {
        throw '字体响应不是 WOFF2；保留原有字体。'
    }
    # 下载及校验成功后才替换，失败不会损坏已提交的资源。OFL.txt 保持原始许可。
    Move-Item -LiteralPath $temporaryFont -Destination $fontTarget -Force
    Write-Output "已更新离线字体：$($fontBytes.Length) 字节；请运行 Web 与 Go 测试并重新编译。"
}
finally {
    if (Test-Path -LiteralPath $temporaryFont) { Remove-Item -LiteralPath $temporaryFont }
}
