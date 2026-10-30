. (Join-Path $PSScriptRoot 'Common.ps1')
Push-Location $ProjectRoot
try { Invoke-Checked @('compose','-f','compose.yml','-p','reserveflow','down') }
finally { Pop-Location }
