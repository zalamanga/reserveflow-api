param([switch]$NoBuild)
. (Join-Path $PSScriptRoot 'Common.ps1')
Initialize-Local
Push-Location $ProjectRoot
try {
    $common = @('compose','-f','compose.yml','-p','reserveflow')
    if (-not $NoBuild) { Invoke-Checked ($common + @('build','api')) }
    Invoke-Checked ($common + @('up','-d','--wait','--wait-timeout','120'))
    Write-Host 'API ready: http://127.0.0.1:8099/openapi.json'
} finally { Pop-Location }
