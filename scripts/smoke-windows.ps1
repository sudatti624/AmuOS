#Requires -RunAsAdministrator
[CmdletBinding()]
param([Parameter(Mandatory=$true)][string]$Package, [Parameter(Mandatory=$true)][string]$UpdatePackage)
$ErrorActionPreference = 'Stop'
$OutputEncoding = [Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
Set-StrictMode -Version Latest
if ((Get-Service AmuOSControlPlane,AmuOSAgent -ErrorAction SilentlyContinue)) { throw 'Smoke test requires both AmuOS service names to be unused.' }
$Package = (Resolve-Path -LiteralPath $Package).Path
$UpdatePackage = (Resolve-Path -LiteralPath $UpdatePackage).Path
$work = Join-Path $PWD ('dist/windows-smoke-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $work -Force | Out-Null
$manage = Join-Path $Package 'packaging/Manage-AmuOS.ps1'
$cpRoot = Join-Path $work 'control plane'
$agentRoot = Join-Path $work 'agent'
$certDir = Join-Path $work 'cert'
function Checked([string]$File, [string[]]$Arguments) {
 $output = & $File @Arguments
 if ($LASTEXITCODE -ne 0) { throw "$File failed" }; return $output
}
function FreePort {
 $listener = [Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
 $listener.Start(); $port = $listener.LocalEndpoint.Port; $listener.Stop(); return $port
}
$port = FreePort; $agentPort = FreePort
while ($port -eq $agentPort) { $agentPort = FreePort }
$origin = "https://localhost:$port"
$agentOrigin = "https://localhost:$agentPort"
$cookie = Join-Path $work 'cookies'
$script:csrf = ''
function Api([string]$Method, [string]$Path, $Body = $null, [int]$Expected = 200) {
 $arguments = @('-ksS', '-b', $cookie, '-c', $cookie, '-H', "Origin: $origin", '-H', 'Content-Type: application/json', '-H', ('X-CSRF-Token: '+$script:csrf), '-H', ('Idempotency-Key: smoke-'+[Guid]::NewGuid()), '-X', $Method, '-w', "`n%{http_code}", ($origin+$Path))
 if ($null -ne $Body) { $bodyPath = Join-Path $work 'body.json'; [IO.File]::WriteAllText($bodyPath, ($Body | ConvertTo-Json -Compress), [Text.UTF8Encoding]::new($false)); $arguments += @('--data-binary', ('@'+$bodyPath)) }
 $raw = (Checked curl.exe $arguments) -join "`n"
 $split = $raw.LastIndexOf("`n"); $code = [int]$raw.Substring($split+1)
 if ($code -ne $Expected) { throw "HTTP $Method $Path returned $code, expected $Expected" }
 $text = $raw.Substring(0,$split)
 if ($text) { return $text | ConvertFrom-Json }
}
function Wait-Healthy {
 for ($i=0; $i -lt 100; $i++) { & curl.exe -ksf "$origin/healthz" *> $null; if ($LASTEXITCODE -eq 0) { return }; Start-Sleep -Milliseconds 200 }
 throw 'Control Plane did not become healthy.'
}
function Login {
 $script:csrf = (Api POST '/api/v1/auth/login' @{username='owner';password='correct horse battery staple'}).csrf_token
}
try {
 Checked (Join-Path $PWD 'dist/devcert.exe') @('--dir',$certDir) | Out-Null
 & $manage -Action Install -Component ControlPlane -Package $Package -Root $cpRoot -Certificate (Join-Path $certDir 'cert.pem') -PrivateKey (Join-Path $certDir 'key.pem') -Server $origin -Address "127.0.0.1:$port" -AgentAddress "127.0.0.1:$agentPort"
 if (!$?) { throw 'Control Plane installation failed' }
 Start-Service AmuOSControlPlane; Wait-Healthy
 $rejected = $false
 try { & $manage -Action Install -Component ControlPlane -Package $Package -Root $cpRoot } catch { $rejected = $true }
 if (!$rejected) { throw 'Duplicate installation was accepted' }
 $rejected = $false
 try { & $manage -Action Update -Component ControlPlane -Package $UpdatePackage -Root $cpRoot } catch { $rejected = $true }
 if (!$rejected -or (Get-Service AmuOSControlPlane).Status -ne 'Running') { throw 'Update without a backup recipient did not fail before stopping' }
 $token = (Get-Content -LiteralPath (Join-Path $cpRoot 'state/setup-token') -Raw).Trim()
 Api POST '/api/v1/setup' @{token=$token;username='owner';password='correct horse battery staple'} 201 | Out-Null
 Remove-Item -LiteralPath (Join-Path $cpRoot 'state/setup-token')
 Login
 $hostId = (Api POST '/api/v1/hosts' @{name='windows-service-host'} 201).id
 $code = (Api POST "/api/v1/hosts/$hostId/enrollment" @{} 201).code
 & $manage -Action Install -Component Agent -Package $Package -Root $agentRoot -CAFile (Join-Path $certDir 'cert.pem') -Server $origin -AgentServer $agentOrigin
 if (!$?) { throw 'Agent installation failed' }
 $codeFile = Join-Path $work 'code'
 [IO.File]::WriteAllText($codeFile, $code)
 $agentExe = Join-Path $agentRoot 'bin/amuos-agent.exe'
 $agentArgs = @('--state-dir',(Join-Path $agentRoot 'state'),'--server',$origin,'--agent-server',$agentOrigin,'--ca-file',(Join-Path $agentRoot 'ca.pem'))
 Checked $agentExe ($agentArgs+@('--enroll-code-file',$codeFile)) | Out-Null
 Remove-Item -LiteralPath $codeFile
 Start-Service AmuOSAgent
 $detail = $null
 for ($i=0; $i -lt 100; $i++) { $detail = Api GET "/api/v1/hosts/$hostId"; if ($detail.info -and $detail.latest -and $detail.latest.seq -ge 2) { break }; Start-Sleep -Milliseconds 300 }
 if (!$detail.info -or $detail.info.os -ne 'Windows' -or $null -eq $detail.latest.memory -or $null -eq $detail.latest.disk) { throw 'Native service monitoring did not arrive' }
 if ($null -eq $detail.latest.cpu -or $null -eq $detail.latest.disk_read -or $null -eq $detail.latest.disk_write) { throw 'CPU or disk I/O counters did not arrive after the baseline' }
 if (@(Get-NetAdapter -Physical).Count -gt 0 -and ($null -eq $detail.latest.net_rx -or $null -eq $detail.latest.net_tx)) { throw 'Network counters did not arrive after the baseline' }
 $task = Api POST "/api/v1/hosts/$hostId/tasks" @{operation='collect'} 201
 for ($i=0; $i -lt 100; $i++) { $result = Api GET ("/api/v1/tasks/"+$task.id); if ($result.state -eq 'succeeded') { break }; Start-Sleep -Milliseconds 300 }
 if ($result.state -ne 'succeeded') { throw 'Service collect task failed' }
 # Services must use virtual non-administrator accounts and separate ACLs.
 foreach ($component in @('ControlPlane','Agent')) {
  $name = 'AmuOS'+$component
  $service = Get-CimInstance Win32_Service -Filter "Name='$name'"
  if ($service.StartName -ne ('NT SERVICE\'+$name)) { throw 'Unexpected service identity' }
 }
 foreach ($path in @((Join-Path $cpRoot 'state'),(Join-Path $agentRoot 'state'),(Join-Path $cpRoot 'tls.key'))) {
  $acl = Get-Acl -LiteralPath $path
  foreach ($ace in $acl.Access) { if ($ace.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value -in @('S-1-1-0','S-1-5-32-545','S-1-5-11')) { throw "Public ACL on $path" } }
 }
 foreach ($rootPath in @($cpRoot,$agentRoot)) {
  $acl = Get-Acl -LiteralPath (Join-Path $rootPath 'bin')
  foreach ($ace in $acl.Access) {
   if ($ace.IdentityReference.Value -like 'NT SERVICE\*' -and ($ace.FileSystemRights -band [Security.AccessControl.FileSystemRights]::Write)) { throw 'Service can write its binary' }
  }
 }
 Restart-Service AmuOSAgent
 Restart-Service AmuOSControlPlane; Wait-Healthy
 Login
 Api GET "/api/v1/hosts/$hostId" | Out-Null
 $cpExe = Join-Path $cpRoot 'bin/amuos-controlplane.exe'
 $keyFile = Join-Path $work 'recovery.key'
 $recipient = (Checked $cpExe @('--generate-recovery-key',$keyFile)) -join ''
 & $manage -Action Update -Component Agent -Root $agentRoot -Package $UpdatePackage
 if (!$?) { throw 'Agent update failed' }
 & $manage -Action Rollback -Component Agent -Root $agentRoot
 if (!$?) { throw 'Agent rollback failed' }
 & $manage -Action Update -Component ControlPlane -Root $cpRoot -Package $UpdatePackage -BackupRecipient $recipient
 if (!$?) { throw 'Control Plane update failed' }; Wait-Healthy
 & $manage -Action Rollback -Component ControlPlane -Root $cpRoot -RecoveryKeyFile $keyFile
 if (!$?) { throw 'Control Plane rollback failed' }; Wait-Healthy
 Api GET '/api/v1/me' $null 401 | Out-Null
 Login
 Api GET "/api/v1/hosts/$hostId" | Out-Null
 Write-Output "PASS: Windows services, monitoring, collect, ACL, restart, update and rollback. Evidence retained in $work"
} finally {
 foreach ($component in @('Agent','ControlPlane')) {
  if (Get-Service ('AmuOS'+$component) -ErrorAction SilentlyContinue) {
   $rootPath = if ($component -eq 'Agent') { $agentRoot } else { $cpRoot }
   & $manage -Action Uninstall -Component $component -Root $rootPath
  }
 }
}
