#Requires -Version 5.1
#Requires -RunAsAdministrator
[CmdletBinding()]
param(
 [Parameter(Mandatory=$true)][ValidateSet('Install','Update','Rollback','Uninstall')][string]$Action,
 [Parameter(Mandatory=$true)][ValidateSet('ControlPlane','Agent')][string]$Component,
 [string]$Package,
 [string]$Root,
 [string]$Certificate,
 [string]$PrivateKey,
 [string]$CAFile,
 [string]$Server = 'https://localhost:8443',
 [string]$AgentServer = 'https://localhost:9443',
 [string]$Address = '127.0.0.1:8443',
 [string]$AgentAddress = '127.0.0.1:9443',
 [string]$BackupRecipient,
 [string]$RecoveryKeyFile
)
$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$name = 'AmuOS' + $Component
$exeName = if ($Component -eq 'Agent') { 'amuos-agent.exe' } else { 'amuos-controlplane.exe' }
if (!$Root) { $Root = Join-Path $env:ProgramData $name }
$Root = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Root)
if ($Root -eq [IO.Path]::GetPathRoot($Root) -or $Root.StartsWith('\\')) { throw 'Use a dedicated local NTFS directory.' }
function Assert-LocalPath([string]$Path) {
 $pathItem = [IO.Path]::GetFullPath($Path)
 while ($pathItem) {
  if (Test-Path -LiteralPath $pathItem) {
   if ((Get-Item -LiteralPath $pathItem -Force).Attributes -band [IO.FileAttributes]::ReparsePoint) { throw "Reparse points are not supported: $pathItem" }
  }
  $parent = Split-Path -Parent $pathItem
  if ($parent -eq $pathItem) { break }; $pathItem = $parent
 }
 $drive = [IO.DriveInfo]::new([IO.Path]::GetPathRoot([IO.Path]::GetFullPath($Path)))
 if ($drive.DriveFormat -ne 'NTFS') { throw 'NTFS is required.' }
}
function Invoke-Checked([string]$File, [string[]]$Arguments) {
 & $File @Arguments
 if ($LASTEXITCODE -ne 0) { throw "$File failed with exit code $LASTEXITCODE" }
}
function Set-PrivateDirectory([string]$Path, [string]$ServiceRights) {
 $acl = [Security.AccessControl.DirectorySecurity]::new()
 $acl.SetAccessRuleProtection($true, $false)
 $acl.SetOwner([Security.Principal.SecurityIdentifier]::new('S-1-5-32-544'))
 foreach ($sid in @('S-1-5-18','S-1-5-32-544')) {
  $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.SecurityIdentifier]::new($sid), 'FullControl', 'ContainerInherit,ObjectInherit', 'None', 'Allow'))
 }
 if ($ServiceRights) { $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new([Security.Principal.NTAccount]::new('NT SERVICE', $name), $ServiceRights, 'ContainerInherit,ObjectInherit', 'None', 'Allow')) }
 Set-Acl -LiteralPath $Path -AclObject $acl
}
function Stop-AmuOS {
 $service = Get-Service -Name $name -ErrorAction Stop
 if ($service.Status -ne 'Stopped') { $service.Stop(); $service.WaitForStatus('Stopped', [TimeSpan]::FromSeconds(45)) }
}
function Copy-Payload([string]$Destination) {
 if (!$Package) { throw '-Package must name the extracted release directory.' }
 $source = Join-Path (Resolve-Path -LiteralPath $Package).Path $exeName
 Assert-LocalPath $source
 if (!(Test-Path -LiteralPath $source -PathType Leaf)) { throw "Missing $exeName" }
 Copy-Item -LiteralPath $source -Destination $Destination
}
Assert-LocalPath $Root
$bin = Join-Path $Root 'bin'
$state = Join-Path $Root 'state'
$config = Join-Path $Root 'service.json'
$exe = Join-Path $bin $exeName
$previous = Join-Path $Root 'previous'
$db = Join-Path $state 'amuos.db'
if ($Action -eq 'Install') {
 if ((Test-Path -LiteralPath $Root) -or (Get-Service -Name $name -ErrorAction SilentlyContinue)) { throw 'Installation already exists. Use Update, or choose an empty dedicated root.' }
 New-Item -ItemType Directory -Path $Root | Out-Null
 Set-PrivateDirectory $Root ''
 New-Item -ItemType Directory -Path $bin,$state | Out-Null
 Copy-Payload $exe
 # The virtual service account has no password and is not an administrator.
 $serviceArgs = @()
 if ($Component -eq 'ControlPlane') {
  if (!$Certificate -or !$PrivateKey) { throw '-Certificate and -PrivateKey are required.' }
  Copy-Item -LiteralPath $Certificate -Destination (Join-Path $Root 'tls.crt')
  Copy-Item -LiteralPath $PrivateKey -Destination (Join-Path $Root 'tls.key')
  $serviceArgs = @('--db', $db, '--addr', $Address, '--agent-addr', $AgentAddress, '--origin', $Server, '--tls-cert', (Join-Path $Root 'tls.crt'), '--tls-key', (Join-Path $Root 'tls.key'))
 } else {
  if (!$CAFile) { throw '-CAFile is required.' }
  Copy-Item -LiteralPath $CAFile -Destination (Join-Path $Root 'ca.pem')
  $serviceArgs = @('--state-dir', $state, '--server', $Server, '--agent-server', $AgentServer, '--ca-file', (Join-Path $Root 'ca.pem'))
 }
 # Reject quotes/newlines before constructing the SCM command line.
 foreach ($arg in @($exe) + $serviceArgs) { if ($arg -match '["\r\n]') { throw 'Paths and arguments cannot contain quotes or line breaks.' } }
 $command = (@($exe) + $serviceArgs | ForEach-Object { '"' + $_ + '"' }) -join ' '
 New-Service -Name $name -BinaryPathName $command -StartupType Manual -DisplayName $name | Out-Null
 Invoke-Checked sc.exe @('config', $name, 'obj=', ('NT SERVICE\' + $name))
 Invoke-Checked sc.exe @('sidtype', $name, 'unrestricted')
 if ($Component -eq 'Agent') { Add-LocalGroupMember -SID 'S-1-5-32-558' -Member ('NT SERVICE\' + $name) }
 Set-PrivateDirectory $Root 'ReadAndExecute'
 Set-PrivateDirectory $bin 'ReadAndExecute'
 Set-PrivateDirectory $state 'Modify'
 # Event source uses the standard EventCreate message resource.
 if (![Diagnostics.EventLog]::SourceExists($name)) { [Diagnostics.EventLog]::CreateEventSource($name, 'Application') }
 $eventSource = 'HKLM:\SYSTEM\CurrentControlSet\Services\EventLog\Application\' + $name
 New-ItemProperty -Path $eventSource -Name EventMessageFile -Value (Join-Path $env:SystemRoot 'System32\EventCreate.exe') -PropertyType ExpandString -Force | Out-Null
 New-ItemProperty -Path $eventSource -Name TypesSupported -Value 7 -PropertyType DWord -Force | Out-Null
 @{ Component=$Component; Arguments=$serviceArgs; Version=(& $exe --version) } | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $config -Encoding UTF8
 if ($Component -eq 'ControlPlane') { Invoke-Checked $exe @('--db', $db, '--bootstrap-file', (Join-Path $state 'setup-token')) }
 Set-Service -Name $name -StartupType Automatic
 Write-Output "Installed $name. Complete bootstrap/enrollment before Start-Service $name. State: $state"
 exit
}
if (!(Test-Path -LiteralPath $config)) { throw 'Installation configuration is missing.' }
$settings = Get-Content -LiteralPath $config -Raw | ConvertFrom-Json
if ($settings.Component -ne $Component) { throw 'Component does not match installation.' }
Assert-LocalPath $bin
Assert-LocalPath $state
if ($Action -eq 'Uninstall') {
 Stop-AmuOS
 if ($Component -eq 'Agent') { Remove-LocalGroupMember -SID 'S-1-5-32-558' -Member ('NT SERVICE\' + $name) }
 Invoke-Checked sc.exe @('delete', $name)
 Write-Output "Removed service $name. Files, credentials and backups are retained in $Root. Archive or remove this dedicated directory separately."
 exit
}
if ($Action -eq 'Update') {
 if (Test-Path -LiteralPath $previous) { throw 'Previous update is retained. Archive the previous directory before another update.' }
 if ($Component -eq 'ControlPlane' -and !$BackupRecipient) { throw '-BackupRecipient is required for a stopped, encrypted pre-update backup.' }
 $candidate = Join-Path $bin 'candidate.exe'
 Copy-Payload $candidate
 Stop-AmuOS
 try {
  if ($Component -eq 'ControlPlane') { Invoke-Checked $candidate @('--db', $db, '--check-db') }
  New-Item -ItemType Directory -Path $previous | Out-Null
  Set-PrivateDirectory $previous ''
  if ($Component -eq 'ControlPlane') { Invoke-Checked $exe (@($settings.Arguments) + @('--backup-out', (Join-Path $previous 'before.age'), '--recipient', $BackupRecipient)) }
  Copy-Item -LiteralPath $exe -Destination (Join-Path $previous $exeName)
  Copy-Item -LiteralPath $config -Destination (Join-Path $previous 'service.json')
  Copy-Item -LiteralPath $candidate -Destination $exe -Force
  Remove-Item -LiteralPath $candidate
  $settings.Version = (& $exe --version)
  $settings | ConvertTo-Json -Depth 5 | Set-Content -LiteralPath $config -Encoding UTF8
  Start-Service $name
 } catch { Write-Warning "Update did not finish. Service is stopped; retained files are in $previous. Follow the rollback instructions."; throw }
 exit
}
if (!(Test-Path -LiteralPath (Join-Path $previous $exeName))) { throw 'Previous binary is missing.' }
if ($Component -eq 'ControlPlane' -and !$RecoveryKeyFile) { throw '-RecoveryKeyFile is required. Rollback restores the pre-update database and revokes Agent credentials.' }
Stop-AmuOS
$old = Join-Path $previous $exeName
if ($Component -eq 'ControlPlane') { Invoke-Checked $old @('--db', $db, '--restore-from', (Join-Path $previous 'before.age'), '--recovery-key-file', $RecoveryKeyFile) }
Copy-Item -LiteralPath $old -Destination $exe -Force
Copy-Item -LiteralPath (Join-Path $previous 'service.json') -Destination $config -Force
Start-Service $name
Write-Output 'Rollback completed. Re-enroll Agents after a Control Plane restore. Previous files remain available.'
