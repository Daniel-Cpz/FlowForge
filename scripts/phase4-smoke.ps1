# Controlled two-process crash/lease smoke. Generated database/key only;
# temporarily replaces development API/workers and restores one normal worker.
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path $PSScriptRoot -Parent
Set-Location -LiteralPath $taskRoot
$taskSuffix = [DateTime]::UtcNow.ToString('yyyyMMddHHmmss') + '_' + [Guid]::NewGuid().ToString('N').Substring(0,8)
$taskDatabase = 'ff_phase4_smoke_' + $taskSuffix
$taskStream = 'flowforge:test:phase4:' + $taskSuffix
$taskOverride = Join-Path $taskRoot ('tmp/phase4-smoke-' + $taskSuffix + '.yml')
$taskCreated=$false; $taskReplaced=$false; $taskPaused=$null; $taskDisconnected=$null; $taskNetwork=$null
function RunDocker {
 param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments)
 $taskOutput = & docker @Arguments
 if ($LASTEXITCODE -ne 0) { throw ('Docker command failed: ' + $Arguments[0]) }
 return $taskOutput
}
function Compose {
 param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments)
 RunDocker (@('compose','-f','docker-compose.yml','-f',$taskOverride)+$Arguments)
}
function Sql([string]$Query) {
 if ($taskDatabase -notmatch '^ff_phase4_smoke_[0-9]{14}_[0-9a-f]{8}$') { throw 'Unsafe smoke database identifier' }
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d',$taskDatabase,'-A','-t','-v','ON_ERROR_STOP=1','-c',$Query)
}
function AwaitCondition([scriptblock]$Predicate,[int]$Seconds,[string]$Step) {
 $taskDeadline=[DateTime]::UtcNow.AddSeconds($Seconds)
 while ([DateTime]::UtcNow -lt $taskDeadline) { if (& $Predicate) {return}; Start-Sleep -Milliseconds 100 }
 throw ('Timed out: '+$Step)
}
function Events([string]$Container) {
 @(RunDocker @('logs',$Container)) | ForEach-Object {try {$_ | ConvertFrom-Json} catch {}}
}
function Owner([string]$Container) { (Events $Container | Where-Object event -eq 'worker_started' | Select-Object -Last 1).worker_id }
function ContainerFor([string]$WorkerID) {
 foreach ($taskContainer in $taskContainers) { if ((Owner $taskContainer) -eq $WorkerID) {return $taskContainer} }
 throw 'Claim owner not one of the two independent processes'
}
function Submit([int]$Duration) {
 $taskBody=@{type='SLEEP';payload=@{duration_ms=$Duration}} | ConvertTo-Json -Compress
 $taskJob=Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs') -Method Post -ContentType 'application/json' -Body $taskBody
 if ($taskJob.status -ne 'QUEUED') {throw 'POST transaction representation changed'}
 return $taskJob.id
}
function RecoveryEvidence([string]$ID,[string]$OldOwner) {
 if ([int](Sql "SELECT count(*) FROM job_attempts WHERE job_id='$ID' AND attempt_number=1 AND worker_id='$OldOwner' AND status='FAILED' AND error='lease_expired' AND result->>'error'='lease_expired' AND finished_at IS NOT NULL") -ne 1) {throw 'Old attempt not closed with lease evidence'}
 if ([int](Sql "SELECT count(*) FROM jobs j JOIN job_attempts a ON a.job_id=j.id AND a.attempt_number=j.attempt_count WHERE j.id='$ID' AND j.status='SUCCEEDED' AND j.attempt_count=2 AND j.assigned_worker<>'$OldOwner' AND a.worker_id=j.assigned_worker AND a.status=j.status AND a.result=j.result AND a.finished_at=j.finished_at") -ne 1) {throw 'Takeover result/attempt mismatch'}
 if ([int](Sql "SELECT count(*) FROM workers WHERE worker_id='$OldOwner' AND status='OFFLINE' AND offline_reason='heartbeat_expired'") -ne 1) {throw 'Lost worker not OFFLINE'}
}
try {
 New-Item -ItemType Directory -Force -Path (Join-Path $taskRoot 'tmp') | Out-Null
 @"
services:
  migrate:
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
  api:
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
  worker:
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
      FLOWFORGE_WORKER_CONCURRENCY: "1"
      FLOWFORGE_LEASE_SECONDS: "3"
      FLOWFORGE_RENEW_SECONDS: "1"
      FLOWFORGE_HEARTBEAT_SECONDS: "1"
      FLOWFORGE_OFFLINE_SECONDS: "4"
      FLOWFORGE_RECOVERY_SECONDS: "1"
"@ | Set-Content -LiteralPath $taskOverride -Encoding ascii
 RunDocker @('compose','up','-d','postgres','redis') | Out-Null
 if ($taskDatabase -notmatch '^ff_phase4_smoke_[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe smoke database identifier'}
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('CREATE DATABASE '+$taskDatabase)) | Out-Null
 $taskCreated=$true; $taskReplaced=$true
 Compose @('up','--build','-d','--scale','worker=2','migrate','api','worker') | Out-Null
 $taskContainers=@(Compose @('ps','-q','worker'))
 if ($taskContainers.Count -ne 2) {throw 'Expected two independent processes'}
 $taskBase='http://'+(Compose @('port','api','8080'))
 AwaitCondition {try {(Invoke-RestMethod -Uri ($taskBase+'/ready')).status -eq 'ready'} catch {$false}} 30 'readiness'
 Write-Output 'Smoke: two independent workers; lease=3s, renew=1s, heartbeat=1s, offline=4s.'
 $taskJob1=Submit 10000
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$taskJob1' AND status='RUNNING' AND lease_expiry>clock_timestamp()") -eq 1} 15 'first claim'
 $taskKilledOwner=Sql "SELECT assigned_worker FROM jobs WHERE id='$taskJob1'"
 $taskKilled=ContainerFor $taskKilledOwner
 RunDocker @('kill','--signal','KILL',$taskKilled) | Out-Null
 if ([int](RunDocker @('inspect','--format','{{.State.ExitCode}}',$taskKilled)) -ne 137) {throw 'Termination was not abrupt'}
 if (@(Events $taskKilled | Where-Object event -eq 'worker_draining').Count -ne 0 -or @(Events $taskKilled | Where-Object event -eq 'job_finished').Count -ne 0) {throw 'Killed worker performed graceful finalization'}
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$taskJob1' AND status='SUCCEEDED' AND attempt_count=2") -eq 1} 25 'SIGKILL takeover and long SLEEP success'
 RecoveryEvidence $taskJob1 $taskKilledOwner
 Write-Output 'Smoke: SIGKILL exit=137; survivor renewed a new 10-second attempt to SUCCEEDED.'
 RunDocker @('start',$taskKilled) | Out-Null
 AwaitCondition {(Owner $taskKilled) -ne $taskKilledOwner -and [int](Sql "SELECT count(*) FROM workers WHERE status IN ('ONLINE','IDLE','BUSY')") -eq 2} 15 'new process identity'
 $taskJob2=Submit 10000
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$taskJob2' AND status='RUNNING'") -eq 1} 15 'paused-owner claim'
 $taskPausedOwner=Sql "SELECT assigned_worker FROM jobs WHERE id='$taskJob2'"
 $taskPaused=ContainerFor $taskPausedOwner
 RunDocker @('pause',$taskPaused) | Out-Null
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$taskJob2' AND status='SUCCEEDED' AND attempt_count=2") -eq 1} 25 'paused owner takeover'
 RecoveryEvidence $taskJob2 $taskPausedOwner
 RunDocker @('unpause',$taskPaused) | Out-Null
 $taskResumed=$taskPaused; $taskPaused=$null
 AwaitCondition {(RunDocker @('inspect','--format','{{.State.Running}}',$taskResumed)) -eq 'false'} 15 'late owner fencing/fatal stop'
 $taskLateExit=[int](RunDocker @('inspect','--format','{{.State.ExitCode}}',$taskResumed))
 if ($taskLateExit -eq 0) {throw 'Stale process reported healthy stop'}
 RecoveryEvidence $taskJob2 $taskPausedOwner
 # A real worker network outage exercises DB/Redis failure while the process
 # remains alive until bounded heartbeat/renew failure drains it.
 RunDocker @('start',$taskResumed) | Out-Null
 AwaitCondition {(Owner $taskResumed) -ne $taskPausedOwner -and [int](Sql "SELECT count(*) FROM workers WHERE status IN ('ONLINE','IDLE','BUSY')") -eq 2} 15 'restart after stale owner failure'
 $taskNetworkJob=Submit 10000
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$taskNetworkJob' AND status='RUNNING'") -eq 1} 15 'network-loss claim'
 $taskNetworkOwner=Sql "SELECT assigned_worker FROM jobs WHERE id='$taskNetworkJob'"
 $taskDisconnected=ContainerFor $taskNetworkOwner
 $taskNetworks=RunDocker @('inspect','--format','{{json .NetworkSettings.Networks}}',$taskDisconnected) | ConvertFrom-Json -AsHashtable
 if ($taskNetworks.Count -ne 1) {throw 'Expected one known Compose network for isolated disconnect'}
 $taskNetwork=@($taskNetworks.Keys)[0]
 RunDocker @('network','disconnect',$taskNetwork,$taskDisconnected) | Out-Null
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$taskNetworkJob' AND status='SUCCEEDED' AND attempt_count=2") -eq 1} 25 'network-loss takeover'
 RecoveryEvidence $taskNetworkJob $taskNetworkOwner
 AwaitCondition {(RunDocker @('inspect','--format','{{.State.Running}}',$taskDisconnected)) -eq 'false'} 15 'network failure bounded process stop'
 $taskNetworkExit=[int](RunDocker @('inspect','--format','{{.State.ExitCode}}',$taskDisconnected))
 if ($taskNetworkExit -eq 0) {throw 'Network-lost worker reported healthy stop'}
 $taskNetworkEvents=@(Events $taskDisconnected | Where-Object worker_id -eq $taskNetworkOwner)
 if (@($taskNetworkEvents | Where-Object { $_.event -in @('heartbeat_failed','lease_renew_failed') }).Count -eq 0 -or @($taskNetworkEvents | Where-Object event -eq 'job_finished').Count -ne 0) {throw 'Network failure fabricated persistence or missed liveness failure'}
 $taskSurvivor=@($taskContainers | Where-Object {$_ -ne $taskDisconnected})[0]
 RunDocker @('network','connect',$taskNetwork,$taskDisconnected) | Out-Null
 $taskDisconnected=$null
 $taskSurvivorOwner=Owner $taskSurvivor
 $taskJob3=Submit 10000
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$taskJob3' AND status='RUNNING'") -eq 1} 15 'graceful active claim'
 RunDocker @('stop','--time','15',$taskSurvivor) | Out-Null
 if ([int](RunDocker @('inspect','--format','{{.State.ExitCode}}',$taskSurvivor)) -ne 0) {throw 'Graceful worker stop failed'}
 if ([int](Sql "SELECT count(*) FROM jobs WHERE id='$taskJob3' AND status='FAILED' AND attempt_count=1 AND result->>'error'='execution_cancelled'") -ne 1) {throw 'Graceful cancellation mistaken for crash'}
 if ([int](Sql "SELECT count(*) FROM workers WHERE worker_id='$taskSurvivorOwner' AND status='OFFLINE' AND offline_reason='graceful_shutdown'") -ne 1) {throw 'Graceful registry evidence missing'}
 $taskFinalEvents=@(Events $taskSurvivor | Where-Object worker_id -eq $taskSurvivorOwner)
 foreach ($taskEvent in @('slot_stopped','dispatcher_stopped','heartbeat_stopped','recovery_stopped','worker_stopped')) {
  if (@($taskFinalEvents | Where-Object event -eq $taskEvent).Count -ne 1) {throw ('Missing joined loop: '+$taskEvent)}
 }
 if ([int](Sql 'SELECT count(*) FROM job_attempts') -ne 7 -or [int](Sql "SELECT count(*) FROM jobs WHERE status='RUNNING'") -ne 0) {throw 'Unexpected attempt total or stranded work'}
 $taskSummary=@{status='PASS';processes=2;concurrency=1;lease_seconds=3;renew_seconds=1;heartbeat_seconds=1;offline_seconds=4;jobs=4;attempts=7;succeeded=3;failed_execution_cancelled=1;lease_expired_attempts=3;abrupt_exit=137;late_owner_exit=$taskLateExit;network_loss_exit=$taskNetworkExit;graceful_exit=0;killed_owner=$taskKilledOwner;paused_owner=$taskPausedOwner;network_lost_owner=$taskNetworkOwner;survivor=$taskSurvivorOwner;database=$taskDatabase;stream=$taskStream}
} finally {
 try {
  if ($null -ne $taskPaused) {RunDocker @('unpause',$taskPaused) | Out-Null}
  if ($null -ne $taskDisconnected -and $null -ne $taskNetwork) {RunDocker @('network','connect',$taskNetwork,$taskDisconnected) | Out-Null}
  if ($taskReplaced) {Compose @('stop','worker','api','migrate') | Out-Null}
  if ($taskCreated) {
   if ($taskDatabase -notmatch '^ff_phase4_smoke_[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe cleanup database identifier'}
   # Network disconnect can leave server-side sockets after the test process
   # exits. All test containers are stopped; terminate ONLY this disposable DB's
   # sessions, never normal application or unrelated database connections.
   RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname='$taskDatabase' AND pid<>pg_backend_pid()")) | Out-Null
   RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('DROP DATABASE '+$taskDatabase)) | Out-Null
   RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli DEL "$1"','sh',$taskStream) | Out-Null
  }
 } finally {
  if (Test-Path -LiteralPath $taskOverride) {Remove-Item -LiteralPath $taskOverride}
  if ($taskReplaced) {RunDocker @('compose','up','-d','--scale','worker=1','migrate','api','worker') | Out-Null}
 }
}
# Emit acceptance PASS only after scoped cleanup and service restoration succeed.
$taskSummary | ConvertTo-Json -Depth 5
