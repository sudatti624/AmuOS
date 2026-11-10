[CmdletBinding()]
param([Parameter(Mandatory=$true)][ValidatePattern('^[a-zA-Z0-9._-]+$')][string]$Version)
$ErrorActionPreference = 'Stop'
$name = 'amuos_' + $Version.TrimStart('v') + '_windows_amd64'
$out = Join-Path $PWD 'dist/windows-release'
$stage = Join-Path $out $name
if (Test-Path -LiteralPath $stage) { throw "Output already exists: $stage" }
New-Item -ItemType Directory -Path $stage -Force | Out-Null
$oldGOOS,$oldGOARCH,$oldCGO = $env:GOOS,$env:GOARCH,$env:CGO_ENABLED
try {
 $env:GOOS='windows'; $env:GOARCH='amd64'; $env:CGO_ENABLED='0'
 foreach ($component in @('controlplane','agent')) {
  & go build -buildvcs=false -trimpath -ldflags "-s -w -X main.version=$Version" -o (Join-Path $stage "amuos-$component.exe") "./cmd/$component"
  if ($LASTEXITCODE -ne 0) { throw 'Build failed' }
 }
 & go run ./scripts/licenses -out (Join-Path $stage 'THIRD_PARTY_LICENSES.md')
 if ($LASTEXITCODE -ne 0) { throw 'License generation failed' }
 Copy-Item -LiteralPath LICENSE,docs/INSTALL-WINDOWS.md,docs/BACKUP.md,docs/FILES.md -Destination $stage
 Copy-Item -LiteralPath packaging/windows -Destination (Join-Path $stage 'packaging') -Recurse
 $zip = Join-Path $out ($name + '.zip')
 if (Test-Path -LiteralPath $zip) { throw 'Archive already exists' }
 Compress-Archive -LiteralPath $stage -DestinationPath $zip
 $hash = (Get-FileHash -LiteralPath $zip -Algorithm SHA256).Hash.ToLowerInvariant()
 [IO.File]::WriteAllText((Join-Path $out 'SHA256SUMS'), "$hash  $name.zip`n", [Text.UTF8Encoding]::new($false))
 Write-Output $zip
} finally { $env:GOOS=$oldGOOS; $env:GOARCH=$oldGOARCH; $env:CGO_ENABLED=$oldCGO }
