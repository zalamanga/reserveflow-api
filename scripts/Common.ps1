$ErrorActionPreference = 'Stop'
$ProjectRoot = Split-Path -Parent $PSScriptRoot
function Invoke-Checked {
    param([string[]]$CommandArgs)
    & docker @CommandArgs
    if ($LASTEXITCODE -ne 0) { throw "Docker command failed with exit code $LASTEXITCODE" }
}
function Initialize-Local {
    & node (Join-Path $PSScriptRoot 'init.mjs')
    if ($LASTEXITCODE -ne 0) { throw 'Credential initialization failed' }
}
