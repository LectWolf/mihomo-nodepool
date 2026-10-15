param([switch]$Build)
$ErrorActionPreference = 'Stop'
$projectRoot = Split-Path -Parent $PSScriptRoot
Push-Location $projectRoot
try {
    $env:NODEPOOL_ENV_FILE = Join-Path $projectRoot '.env'
    $executable = Join-Path $projectRoot 'build\nodepool.exe'
    if ($Build -or -not (Test-Path -LiteralPath $executable)) {
        if (-not (Test-Path -LiteralPath 'frontend\node_modules')) {
            npm ci --prefix frontend
            if ($LASTEXITCODE -ne 0) { throw '前端依赖安装失败。' }
        }
        node scripts/build.mjs
        if ($LASTEXITCODE -ne 0) { throw '应用构建失败。' }
    }
    Write-Host '正在启动 Mihomo NodePool，按 Ctrl+C 停止。'
    Write-Host '默认访问地址：http://127.0.0.1:8080；管理员密码见 .env 或 data/admin-password。'
    & $executable
    if ($LASTEXITCODE -ne 0) { throw '应用已异常退出，请查看上方日志。' }
} finally {
    Pop-Location
}
