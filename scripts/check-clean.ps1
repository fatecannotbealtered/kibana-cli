#Requires -Version 5.1
# Fails if legacy Elasticsearch-direct artifacts reappear in the repo.
$ErrorActionPreference = "Stop"
$RepoRoot = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)

$patterns = @(
    'ELASTICSEARCH_HOST',
    'ELASTICSEARCH_API_KEY',
    'KIBANA_CLI_TOKEN',
    'internal/api',
    'index list',
    'docker-compose\.e2e',
    'scripts/e2e-up\.ps1',
    'scripts/e2e-seed\.ps1',
    'scripts/e2e.local',
    'integration/cmd_e2e',
    'docs/INTEGRATION.md',
    'KIBANA_E2E_HOST',
    'localhost:9200',
    'es.example.com:9200'
)

$exclude = @(
    'CHANGELOG.md',
    'CONTRIBUTING.md',
    'scripts/check-clean.ps1',
    'scripts/check-clean.sh',
    'docs/evidence'
)

Push-Location $RepoRoot
$failed = $false
foreach ($pat in $patterns) {
    $hits = Get-ChildItem -Recurse -File |
        Where-Object {
            $rel = $_.FullName.Substring($RepoRoot.Length + 1) -replace '\\', '/'
            $skip = $false
            foreach ($ex in $exclude) {
                if ($rel -eq $ex -or $rel -like "$ex*") { $skip = $true; break }
            }
            if ($_.Name -eq 'check-clean.ps1' -or $_.Name -eq 'check-clean.sh') { $skip = $true }
            -not $skip
        } |
        Select-String -Pattern $pat -SimpleMatch -ErrorAction SilentlyContinue
    if ($hits) {
        $failed = $true
        Write-Host "FORBIDDEN pattern '$pat':" -ForegroundColor Red
        $hits | Select-Object -First 8 | ForEach-Object { Write-Host "  $($_.Path):$($_.LineNumber): $($_.Line.Trim())" }
    }
}
Pop-Location

if ($failed) {
    Write-Host "`ncheck-clean FAILED — remove legacy ES-direct references." -ForegroundColor Red
    exit 1
}
Write-Host "check-clean OK" -ForegroundColor Green
exit 0
