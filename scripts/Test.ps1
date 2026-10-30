param([switch]$NoBuild)
. (Join-Path $PSScriptRoot 'Common.ps1')
if (Get-NetTCPConnection -LocalPort 5453 -State Listen -ErrorAction SilentlyContinue) { throw 'Port 5453 is occupied. Test environment was not changed.' }
$previous = $env:TEST_DB_PASSWORD
$randomBytes = New-Object byte[] 32
$rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
$rng.GetBytes($randomBytes)
$rng.Dispose()
$env:TEST_DB_PASSWORD = ([System.BitConverter]::ToString($randomBytes)).Replace('-','').ToLowerInvariant()
Push-Location $ProjectRoot
$started = $false
try {
    $common = @('compose','-f','compose.test.yml','-p','reserveflow-test')
    if (-not $NoBuild) { Invoke-Checked ($common + @('build','tests')) }
    $started = $true
    Invoke-Checked ($common + @('up','-d','--wait','db'))
    Invoke-Checked ($common + @('run','--rm','--no-deps','tests'))
} finally {
    if ($started) { & docker compose -f compose.test.yml -p reserveflow-test down }
    $env:TEST_DB_PASSWORD = $previous
    Pop-Location
}
