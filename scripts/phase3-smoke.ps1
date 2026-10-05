# Controlled local Phase 3 smoke. Uses only a new disposable database and stream.
# Temporarily replaces Compose API/worker containers; restores normal services.
param([ValidateRange(1,32)][int]$Concurrency = 2)
$ErrorActionPreference = 'Stop'
if ($Concurrency -ne 2) { throw 'This acceptance smoke expects C=2; use unit tests for other bounds.' }
$taskRoot = Split-Path $PSScriptRoot -Parent
Set-Location -LiteralPath $taskRoot
$taskSuffix = [DateTime]::UtcNow.ToString('yyyyMMddHHmmss') + '_' + [Guid]::NewGuid().ToString('N').Substring(0,8)
$taskDatabase = 'ff_phase3_smoke_' + $taskSuffix
$taskStream = 'flowforge:test:phase3:' + $taskSuffix
$taskOverride = Join-Path $taskRoot ('tmp/phase3-smoke-' + $taskSuffix + '.yml')
$taskCreated = $false
$taskReplaced = $false
$taskContainers = @()
function RunDocker {
 param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments)
 $taskOutput = & docker @Arguments
 if ($LASTEXITCODE -ne 0) { throw ('Docker command failed: ' + $Arguments[0]) }
 return $taskOutput
}
function Compose {
 param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments)
 RunDocker (@('compose','-f','docker-compose.yml','-f',$taskOverride) + $Arguments)
}
function Sql([string]$Query) {
 # Generated database identifier is validated before any creation or deletion.
 if ($taskDatabase -notmatch '^ff_phase3_smoke_[0-9]{14}_[0-9a-f]{8}$') { throw 'Unsafe smoke database identifier' }
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d',$taskDatabase,'-A','-t','-v','ON_ERROR_STOP=1','-c',$Query)
}
function AwaitCondition([scriptblock]$Predicate,[int]$Seconds,[string]$Step) {
 $taskDeadline = [DateTime]::UtcNow.AddSeconds($Seconds)
 while ([DateTime]::UtcNow -lt $taskDeadline) {
  if (& $Predicate) { return }
  Start-Sleep -Milliseconds 100
 }
 throw ('Timed out: ' + $Step)
}
function PostJobs([int]$Count,[int]$Duration) {
 for ($taskIndex=0; $taskIndex -lt $Count; $taskIndex++) {
  $taskBody = @{ type='SLEEP'; payload=@{duration_ms=$Duration} } | ConvertTo-Json -Compress
  $taskJob = Invoke-RestMethod -Uri ($taskBase + '/api/v1/jobs') -Method Post -ContentType 'application/json' -Body $taskBody
  if ($taskJob.status -ne 'QUEUED') { throw 'POST did not return QUEUED transaction representation' }
 }
}
function StopAndCheck([string]$Container) {
 RunDocker @('stop','--time','15',$Container) | Out-Null
 $taskExit = RunDocker @('inspect','--format','{{.State.ExitCode}}',$Container)
 if ([int]$taskExit -ne 0) { throw 'Worker exited nonzero' }
 $taskLines = @(RunDocker @('logs',$Container))
 $taskEvents = @($taskLines | ForEach-Object { try { $_ | ConvertFrom-Json } catch {} })
 if (@($taskEvents | Where-Object event -eq 'worker_draining').Count -ne 1 -or @($taskEvents | Where-Object event -eq 'worker_stopped').Count -ne 1 -or @($taskEvents | Where-Object event -eq 'slot_stopped').Count -ne $Concurrency -or @($taskEvents | Where-Object event -eq 'dispatcher_stopped').Count -ne 1) { throw 'Missing joined shutdown lifecycle evidence' }
 if (@($taskEvents | Where-Object event -eq 'pool_failed').Count -gt 0) { throw 'Unexpected pool failure' }
 return $taskEvents
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
      FLOWFORGE_WORKER_CONCURRENCY: "$Concurrency"
"@ | Set-Content -LiteralPath $taskOverride -Encoding ascii
 RunDocker @('compose','up','-d','postgres','redis') | Out-Null
 # psql local socket authentication: no password or connection URL in arguments.
 if ($taskDatabase -notmatch '^ff_phase3_smoke_[0-9]{14}_[0-9a-f]{8}$') { throw 'Unsafe smoke database identifier' }
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('CREATE DATABASE ' + $taskDatabase)) | Out-Null
 $taskCreated=$true; $taskReplaced=$true
 Compose @('up','--build','-d','--scale','worker=2','migrate','api','worker') | Out-Null
 $taskContainers = @(Compose @('ps','-q','worker'))
 if ($taskContainers.Count -ne 2) { throw 'Expected exactly two independent workers' }
 $taskPort = Compose @('port','api','8080')
 $taskBase = 'http://' + $taskPort
 AwaitCondition { try { (Invoke-RestMethod -Uri ($taskBase+'/ready')).status -eq 'ready' } catch { $false } } 30 'API readiness'
 Write-Output 'Smoke: two independent workers started, C=2; submitting first eight SLEEP jobs.'
 PostJobs 8 2000
 AwaitCondition { [int](Sql "SELECT count(DISTINCT assigned_worker) FROM jobs WHERE status='RUNNING'") -eq 2 -and [int](Sql "SELECT count(*) FROM jobs WHERE status='RUNNING'") -eq 4 } 15 'two saturated processes'
 $taskInitialOwners = @(Sql "SELECT assigned_worker FROM jobs WHERE status='RUNNING' GROUP BY assigned_worker ORDER BY assigned_worker")
 AwaitCondition { [int](Sql "SELECT count(*) FROM jobs WHERE status='SUCCEEDED'") -eq 8 } 30 'first batch completion'
 $taskEvents1 = @(StopAndCheck $taskContainers[0])
 $taskStoppedOwner = ($taskEvents1 | Where-Object event -eq 'worker_started' | Select-Object -First 1).worker_id
 $taskStoppedClaims = [int](Sql "SELECT count(*) FROM jobs WHERE assigned_worker='$taskStoppedOwner'")
 Write-Output 'Smoke: first worker SIGTERM while idle, exit=0; survivor receives four new jobs.'
 PostJobs 4 100
 AwaitCondition { [int](Sql "SELECT count(*) FROM jobs WHERE status='SUCCEEDED'") -eq 12 } 20 'survivor completion'
 if ([int](Sql "SELECT count(*) FROM jobs WHERE status='SUCCEEDED' AND assigned_worker='$taskStoppedOwner'") -ne $taskStoppedClaims) { throw 'Stopped process claimed new work' }
 PostJobs 4 10000
 AwaitCondition { [int](Sql "SELECT count(*) FROM jobs WHERE status='RUNNING'") -eq 2 -and [int](Sql "SELECT count(*) FROM jobs WHERE status='QUEUED'") -eq 2 } 15 'survivor saturated with queued backlog'
 $taskEvents2 = @(StopAndCheck $taskContainers[1])
 if ([int](Sql "SELECT count(*) FROM jobs WHERE status='FAILED' AND result->>'error'='execution_cancelled'") -ne 2) { throw 'Active SIGTERM did not persist cancellation' }
 if ([int](Sql "SELECT count(*) FROM jobs WHERE status='QUEUED' AND attempt_count=0") -ne 2) { throw 'Unclaimed backlog changed' }
 $taskInconsistent = [int](Sql "SELECT count(*) FROM jobs j JOIN job_attempts a ON a.job_id=j.id WHERE j.status<>a.status OR j.assigned_worker<>a.worker_id OR j.attempt_count<>a.attempt_number OR j.result IS DISTINCT FROM a.result OR j.finished_at IS DISTINCT FROM a.finished_at")
 if ($taskInconsistent -ne 0 -or [int](Sql 'SELECT count(*) FROM job_attempts') -ne 14) { throw 'Job/Attempt mismatch' }
 $taskAllEvents=@($taskEvents1)+@($taskEvents2)
 $taskWorkerEvidence=@()
 foreach ($taskOwner in $taskInitialOwners) {
  $taskOwnerEvents=@($taskAllEvents | Where-Object worker_id -eq $taskOwner)
  $taskClaims=@($taskOwnerEvents | Where-Object event -eq 'job_claimed')
  $taskMaximum = ($taskClaims | Measure-Object -Property active_jobs -Maximum).Maximum
  if ($taskMaximum -ne 2 -or $taskClaims.Count -eq 0 -or @($taskOwnerEvents | Where-Object { $null -ne $_.active_jobs -and ($_.active_jobs -lt 0 -or $_.active_jobs -gt $Concurrency) }).Count -gt 0) { throw 'Concurrency log exceeded bound or missed saturation' }
  $taskWorkerEvidence += @{worker_id=$taskOwner;claims=$taskClaims.Count;observed_max_active=$taskMaximum;exit_code=0}
 }
 $taskSummary=@{status='PASS';processes=2;concurrency=2;jobs=16;attempts=14;succeeded=12;failed_execution_cancelled=2;unclaimed_queued=2;inconsistent_attempts=0;workers=$taskWorkerEvidence;database=$taskDatabase;stream=$taskStream;initial_compose_scale='worker=2';survivor_processed_new_jobs=4;shutdown='first idle SIGTERM; survivor saturated/active SIGTERM; all slots and dispatchers joined'}
 $taskSummary | ConvertTo-Json -Depth 5
} finally {
 try {
 if ($taskReplaced) { Compose @('stop','worker','api','migrate') | Out-Null }
 if ($taskCreated) {
  if ($taskDatabase -notmatch '^ff_phase3_smoke_[0-9]{14}_[0-9a-f]{8}$') { throw 'Unsafe cleanup database identifier' }
  RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('DROP DATABASE ' + $taskDatabase)) | Out-Null
  RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli DEL "$1"','sh',$taskStream) | Out-Null
 }
 } finally {
 if (Test-Path -LiteralPath $taskOverride) { Remove-Item -LiteralPath $taskOverride }
 if ($taskReplaced) { RunDocker @('compose','up','-d','--scale','worker=1','migrate','api','worker') | Out-Null }
 }
}
