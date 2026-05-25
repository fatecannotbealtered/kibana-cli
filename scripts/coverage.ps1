#Requires -Version 5.1
<#
.SYNOPSIS
  Run all Go tests with atomic coverage and fail if any package is below 100%.
.DESCRIPTION
  Writes coverage.dat at the repository root (not .out).
#>
$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
$Profile = Join-Path $RepoRoot "coverage.dat"
$Ldflags = "-X github.com/fatecannotbealtered/kibana-cli/cmd.version=dev"

Push-Location $RepoRoot
try {
    if (Test-Path $Profile) { Remove-Item $Profile -Force }

    $packages = @(go list ./... | Where-Object { $_ -notmatch '/vendor/' })
    $rows = @()
    $failed = $false

    foreach ($pkg in $packages) {
        $pkgProfile = (Join-Path $RepoRoot ("coverage-" + ($pkg -replace '[^a-zA-Z0-9]+', '_') + ".dat"))
        if (Test-Path $pkgProfile) { Remove-Item $pkgProfile -Force }
        go test $pkg "-coverprofile=$pkgProfile" -covermode=atomic -ldflags $Ldflags
        if ($LASTEXITCODE -ne 0) {
            Write-Error "go test failed for $pkg (exit $LASTEXITCODE)"
        }
        if (-not (Test-Path $pkgProfile)) {
            Write-Error "coverage profile not written for $pkg"
        }
        $totalLine = (go tool cover "-func=$pkgProfile" | Select-String '^total:') -join ''
        if ($totalLine -notmatch '(\d+\.\d+)%') {
            Write-Error "could not parse coverage for $pkg from: $totalLine"
        }
        $pct = [double]$Matches[1]
        $rows += [pscustomobject]@{ Package = $pkg; Coverage = $pct }
        if ($pct -lt 100.0) { $failed = $true }
        Remove-Item $pkgProfile -Force -ErrorAction SilentlyContinue
    }

    # Combined profile for optional inspection (coverage.dat at repo root)
    go test @packages "-coverprofile=$Profile" -covermode=atomic -ldflags $Ldflags | Out-Null
    if ($LASTEXITCODE -ne 0) {
        Write-Error "combined go test failed (exit $LASTEXITCODE)"
    }

    Write-Host ""
    Write-Host "Per-package coverage:" -ForegroundColor Cyan
    foreach ($row in ($rows | Sort-Object Package)) {
        Write-Host ("  {0,-60} {1,6:N1}%" -f $row.Package, $row.Coverage)
    }

    if ($failed) {
        Write-Host ""
        Write-Host "Packages below 100%:" -ForegroundColor Yellow
        $rows | Where-Object { $_.Coverage -lt 100.0 } | ForEach-Object {
            Write-Host ("  {0} ({1:N1}%)" -f $_.Package, $_.Coverage)
            $pkgProfile = Join-Path $RepoRoot "coverage.dat"
            if (Test-Path $Profile) {
                go tool cover -func=$Profile |
                    Where-Object { $_ -like "*$($_.Package -replace 'github.com/fatecannotbealtered/kibana-cli/','')*" } |
                    Where-Object { $_ -notmatch '\s+100\.0%$' -and $_ -notmatch '^total:' }
            }
        }
        Write-Error "Coverage gate failed: all packages must be 100%"
    }

    Write-Host ""
    Write-Host "All packages at 100% coverage." -ForegroundColor Green
} finally {
    Pop-Location
}
