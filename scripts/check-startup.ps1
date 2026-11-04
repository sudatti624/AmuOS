# Verify the real Windows binary without starting a network listener.
$ErrorActionPreference = 'Stop'
$workspace = (Resolve-Path -LiteralPath (Join-Path $PSScriptRoot '..')).Path
$binary = Join-Path $workspace 'dist/controlplane.exe'
if (-not (Test-Path -LiteralPath $binary)) { throw 'Build dist/controlplane.exe first.' }
$cacheRoot = Join-Path $workspace '.cache'
New-Item -ItemType Directory -Path $cacheRoot -Force | Out-Null
$caseRoot = Join-Path $cacheRoot ('startup-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $caseRoot | Out-Null

function Invoke-ExpectedExit {
    param([string[]]$CommandArguments, [int]$ExpectedExit)
    $ErrorActionPreference = 'Continue'
    & $binary @CommandArguments 2>$null | Out-Null
    if ($LASTEXITCODE -ne $ExpectedExit) { throw 'Unexpected startup exit code.' }
}

try {
    $database = Join-Path $caseRoot 'state/amuos.db'
    $tokenPath = Join-Path $caseRoot 'state/setup-token'
    Invoke-ExpectedExit -CommandArguments @('--db', $database) -ExpectedExit 1
    Invoke-ExpectedExit -CommandArguments @('--db', $database, '--tls-cert', (Join-Path $caseRoot 'missing.pem'), '--tls-key', (Join-Path $caseRoot 'missing.key')) -ExpectedExit 1
    if (Test-Path -LiteralPath (Split-Path -Parent $database)) { throw 'Failed TLS startup initialized state.' }
    Invoke-ExpectedExit -CommandArguments @('--db', $database, '--bootstrap-file', $tokenPath) -ExpectedExit 0
    $first = [IO.File]::ReadAllBytes($tokenPath)
    if ($first.Length -ne 44) { throw 'Missing local setup credential.' }
    Invoke-ExpectedExit -CommandArguments @('--db', $database, '--bootstrap-file', $tokenPath) -ExpectedExit 1
    $second = [IO.File]::ReadAllBytes($tokenPath)
    if ([Convert]::ToBase64String($first) -ne [Convert]::ToBase64String($second)) { throw 'Existing credential was overwritten.' }
    Write-Output 'PASS: TLS startup rejection, local bootstrap, and overwrite protection'
}
finally {
    $resolvedCase = (Resolve-Path -LiteralPath $caseRoot).Path
    $resolvedCache = (Resolve-Path -LiteralPath $cacheRoot).Path
    if (-not $resolvedCase.StartsWith($resolvedCache + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) { throw 'Unsafe temporary cleanup path.' }
    Remove-Item -LiteralPath $resolvedCase -Recurse -Force
}
