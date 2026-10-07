# anc-onsite 构建脚本：本机编译 + 交叉编译 + 打包。
#
# 为什么用 PowerShell 而不是 Makefile：开发机是 Windows，且这个脚本只在开发期跑、
# 不进交付物（交付物是 dist/ 里各平台的 exe + 模板）。
#
# 用法：
#   pwsh -File build.ps1              # 全部目标
#   pwsh -File build.ps1 -Only local  # 只编本机 windows/amd64
#
# 产出：dist/anc_<version>_<os>_<arch>.zip（内含 exe + templates/ + inventory/ + README.md）

[CmdletBinding()]
param(
    [ValidateSet("all", "local")]
    [string]$Only = "all"
)

$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$Root = $PSScriptRoot
$Dist = Join-Path $Root "dist"

# --- 安全护栏：dist/ 必须落在本工程内，删之前先核对绝对路径 ---
$RootAbs = [System.IO.Path]::GetFullPath($Root)
$DistAbs = [System.IO.Path]::GetFullPath($Dist)
if (-not $DistAbs.StartsWith($RootAbs + [System.IO.Path]::DirectorySeparatorChar)) {
    throw "拒绝执行：dist 路径 $DistAbs 不在工程目录 $RootAbs 内。"
}

# --- 版本号从 main.go 里抠，单一事实来源 ---
$mainGo = Join-Path $Root "main.go"
$verLine = Select-String -Path $mainGo -Pattern '^const version = "(.+)"' | Select-Object -First 1
if (-not $verLine) { throw "在 main.go 里找不到 const version" }
$Version = $verLine.Matches[0].Groups[1].Value
Write-Host "anc 版本：$Version" -ForegroundColor Cyan

# --- 看板前端产物新鲜度 ---
# 产物是入库的静态文件（现场编译 Go 二进制不需要 node），所以「改了 TS 忘了 build」
# 会静默打出旧界面。这里不拦，只把它喊出来。
$UiSrc = Join-Path $Root "boardui\src"
$UiOut = Join-Path $Root "internal\board\ui\app.js"
if (-not (Test-Path -LiteralPath $UiOut)) {
    throw "缺 $UiOut —— 先跑：cd boardui; npm run build"
}
if (Test-Path -LiteralPath $UiSrc) {
    $outTime = (Get-Item -LiteralPath $UiOut).LastWriteTimeUtc
    $stale = @(Get-ChildItem -LiteralPath $UiSrc -Recurse -File | Where-Object { $_.LastWriteTimeUtc -gt $outTime })
    if ($stale.Count -gt 0) {
        Write-Warning "看板前端源码比产物新（$($stale.Count) 个文件）。先跑：cd boardui; npm run build —— 否则打出来的二进制里是旧界面。"
    }
}

# --- 清理旧产出 ---
if (Test-Path -LiteralPath $Dist) {
    Write-Host "清理旧产出：$DistAbs"
    Remove-Item -LiteralPath $Dist -Recurse -Force
}
New-Item -ItemType Directory -Path $Dist -Force | Out-Null

# --- 目标矩阵 ---
$targets = @(
    [pscustomobject]@{ OS = "windows"; Arch = "amd64"; Note = "开发机 / 常见客户 PC" }
    [pscustomobject]@{ OS = "windows"; Arch = "arm64"; Note = "Windows on ARM" }
    [pscustomobject]@{ OS = "darwin";  Arch = "arm64"; Note = "Mac mini M 系列 —— 主力交付形态" }
    [pscustomobject]@{ OS = "darwin";  Arch = "amd64"; Note = "老 Intel Mac" }
    [pscustomobject]@{ OS = "linux";   Arch = "amd64"; Note = "租的云主机 / 信创 x86" }
    [pscustomobject]@{ OS = "linux";   Arch = "arm64"; Note = "ARM 服务器 / 麒麟 ARM" }
)

if ($Only -eq "local") {
    $targets = @($targets | Where-Object { $_.OS -eq "windows" -and $_.Arch -eq "amd64" })
}

$env:CGO_ENABLED = "0"   # 纯静态，无 libc 依赖 —— 客户机器上不装任何东西

$results = @()
foreach ($t in $targets) {
    $os = $t.OS
    $arch = $t.Arch
    $ext = ""
    if ($os -eq "windows") { $ext = ".exe" }
    $binName = "anc$ext"
    $stageName = "anc_${Version}_${os}_${arch}"
    $stage = Join-Path $Dist $stageName
    New-Item -ItemType Directory -Path $stage -Force | Out-Null

    $env:GOOS = $os
    $env:GOARCH = $arch
    $binPath = Join-Path $stage $binName

    $sw = [System.Diagnostics.Stopwatch]::StartNew()
    & go build -trimpath -ldflags "-s -w" -o $binPath $Root
    if ($LASTEXITCODE -ne 0) { throw "go build 失败：$os/$arch" }
    $sw.Stop()

    Copy-Item -Path (Join-Path $Root "templates") -Destination $stage -Recurse -Force
    Copy-Item -Path (Join-Path $Root "inventory") -Destination $stage -Recurse -Force
    Copy-Item -Path (Join-Path $Root "README.md") -Destination $stage -Force

    $zipPath = Join-Path $Dist "$stageName.zip"
    Compress-Archive -Path (Join-Path $stage "*") -DestinationPath $zipPath -Force

    $binMB = [math]::Round((Get-Item -LiteralPath $binPath).Length / 1MB, 2)
    $zipMB = [math]::Round((Get-Item -LiteralPath $zipPath).Length / 1MB, 2)
    $secs = [math]::Round($sw.Elapsed.TotalSeconds, 1)
    $results += [pscustomobject]@{
        目标   = "$os/$arch"
        说明   = $t.Note
        exe_MB = $binMB
        zip_MB = $zipMB
        秒     = $secs
    }
    Write-Host ("  OK {0,-16} exe {1,6} MB   zip {2,6} MB   {3}s" -f "$os/$arch", $binMB, $zipMB, $secs) -ForegroundColor Green
}

# 把本机用的那一个放到 dist/ 根下，方便直接 .\dist\anc.exe
$hostExe = Join-Path $Dist "anc_${Version}_windows_amd64\anc.exe"
if (Test-Path -LiteralPath $hostExe) {
    Copy-Item -LiteralPath $hostExe -Destination (Join-Path $Dist "anc.exe") -Force
}

Write-Host ""
Write-Host "产出：$DistAbs" -ForegroundColor Cyan
$results | Format-Table -AutoSize
