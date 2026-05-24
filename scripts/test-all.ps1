#Requires -Version 5.1
<#
.SYNOPSIS
  Run unit tests, check-clean, and write docs/TEST_REPORT.md.
#>
$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$EvidenceDir = Join-Path $RepoRoot "docs\evidence"
$ReportPath = Join-Path $RepoRoot "docs\TEST_REPORT.md"
$UnitLog = Join-Path $EvidenceDir "unit-test.log"

New-Item -ItemType Directory -Path $EvidenceDir -Force | Out-Null
Push-Location $RepoRoot
try {
    Write-Host "=== check-clean ===" -ForegroundColor Cyan
    & "$RepoRoot\scripts\check-clean.ps1"
    if ($LASTEXITCODE -ne 0) { exit $LASTEXITCODE }

    Write-Host "=== Unit tests ===" -ForegroundColor Cyan
    go test ./... -count=1 -v 2>&1 | Tee-Object -FilePath $UnitLog
    $unitExit = $LASTEXITCODE

    $unitFail = Select-String -Path $UnitLog -Pattern "^FAIL"

    $report = @"
# kibana-cli 测试验收报告

生成时间: $(Get-Date -Format 'yyyy-MM-dd HH:mm:ss')

## 汇总

| 套件 | 退出码 | 日志 |
|------|--------|------|
| 单元测试 ``go test ./...`` | $unitExit | [unit-test.log](evidence/unit-test.log) |

## 命令覆盖（单元测试 + mock Kibana）

| 命令 | 单元测试 |
|------|----------|
| ``--version`` | TestRoot_Version |
| ``--help`` | TestRoot_Help |
| ``auth login/logout/status`` | TestAuth_* |
| ``doctor`` | TestDoctor_* |
| ``context`` | TestContext_Mock |
| ``config init/show`` | TestConfig_* |
| ``patterns list/fields`` | TestPatterns_* |
| ``search`` | TestSearch_* |
| ``agg`` | TestAgg_* |
| ``reference`` | TestReference_* |

"@

    if ($unitFail) { $report += "`n### 单元失败`n``````n$($unitFail.Line -join "`n")`n``````n" }

    Set-Content -Path $ReportPath -Value $report -Encoding UTF8
    Write-Host "Report: $ReportPath" -ForegroundColor Green

    if ($unitExit -ne 0) { exit 1 }
} finally {
    Pop-Location
}
