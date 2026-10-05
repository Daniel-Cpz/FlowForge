# Independent real API/Worker acceptance. Generated resources only; retained DB
# and normal service containers/images are never upgraded or replaced.
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path $PSScriptRoot -Parent
Set-Location -LiteralPath $taskRoot
$taskSuffix = [DateTime]::UtcNow.ToString('yyyyMMddHHmmss') + '_' + [Guid]::NewGuid().ToString('N').Substring(0,8)
$taskDatabase = 'ff_phase7_smoke_' + $taskSuffix
$taskStream = 'flowforge:test:phase7:' + $taskSuffix
$taskOverride = Join-Path $taskRoot ('tmp/phase7-smoke-' + $taskSuffix + '.yml')
$taskApi = 'ff-phase7-api-' + $taskSuffix
$taskCpu = 'ff-phase7-cpu-' + $taskSuffix
$taskGpu = 'ff-phase7-gpu-' + $taskSuffix
$taskCreated = $false
$taskContainers = @()
$taskHttp = [Net.Http.HttpClient]::new()
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
 if ($taskDatabase -notmatch '^ff_phase7_smoke_[0-9]{14}_[0-9a-f]{8}$') { throw 'Unsafe smoke database identifier' }
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d',$taskDatabase,'-A','-t','-v','ON_ERROR_STOP=1','-c',$Query)
}
function RetainedAudit {
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-A','-t','-v','ON_ERROR_STOP=1','-c',"SELECT json_build_object('legacy_duplicate_groups',(SELECT count(*) FROM (SELECT idempotency_key FROM jobs WHERE idempotency_key IS NOT NULL GROUP BY idempotency_key HAVING count(*)>1) d),'schema_version',(SELECT max(version) FROM schema_migrations))")
}
function AwaitCondition([scriptblock]$Predicate,[int]$Seconds,[string]$Step) {
 $taskDeadline=[DateTime]::UtcNow.AddSeconds($Seconds)
 while ([DateTime]::UtcNow -lt $taskDeadline) { if (& $Predicate) {return}; Start-Sleep -Milliseconds 100 }
 throw ('Timed out: '+$Step)
}
function Post([string]$Path,[string]$Body,[int]$Code=201) {
 $taskContent=[Net.Http.StringContent]::new($Body,[Text.Encoding]::UTF8,'application/json')
 try {
  $taskResponse=$taskHttp.PostAsync($taskBase+$Path,$taskContent).GetAwaiter().GetResult()
  try {if([int]$taskResponse.StatusCode -ne $Code){throw ('HTTP command failed: '+$Path)};return ($taskResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult()|ConvertFrom-Json)} finally {$taskResponse.Dispose()}
 } finally {$taskContent.Dispose()}
}
$taskBefore=RetainedAudit
try {
 New-Item -ItemType Directory -Force -Path (Join-Path $taskRoot 'tmp') | Out-Null
 @"
services:
  migrate:
    image: flowforge-phase7-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
  api:
    image: flowforge-phase7-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
      FLOWFORGE_WORKER_CAPABILITIES: ""
  worker:
    image: flowforge-phase7-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
      FLOWFORGE_WORKER_CONCURRENCY: "1"
"@ | Set-Content -LiteralPath $taskOverride -Encoding ascii
 RunDocker @('build','-t','flowforge-phase7-smoke','.') | Out-Null
 if ($taskDatabase -notmatch '^ff_phase7_smoke_[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe smoke database identifier'}
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('CREATE DATABASE '+$taskDatabase)) | Out-Null
 $taskCreated=$true
 Compose @('run','--rm','--no-deps','migrate','/app/migrate','up') | Out-Null
 $taskContainers += $taskApi
 Compose @('run','-d','--no-deps','--name',$taskApi,'-p','127.0.0.1::8080','api') | Out-Null
 $taskPort=RunDocker @('port',$taskApi,'8080/tcp')
 $taskBase='http://'+$taskPort
 AwaitCondition {try {(Invoke-RestMethod -Uri ($taskBase+'/ready')).status -eq 'ready'} catch {$false}} 30 'isolated API readiness'
 $taskContainers += $taskCpu
 Compose @('run','-d','--no-deps','--name',$taskCpu,'-e','FLOWFORGE_WORKER_CAPABILITIES=CPU,cpu','worker') | Out-Null
 AwaitCondition {[int](Sql "SELECT count(*) FROM workers WHERE capabilities='{cpu}' AND status IN ('ONLINE','IDLE','BUSY')") -eq 1} 15 'CPU worker canonical registration'
 $taskDue=(Sql "SELECT (clock_timestamp()+interval '5 seconds') AT TIME ZONE 'UTC'").Trim().Replace(' ','T')+'Z'
 $taskDelayed=Post '/api/v1/jobs' (@{type='SLEEP';payload=@{duration_ms=0};scheduled_at=$taskDue;required_capabilities=@(' CPU ');timeout=1}|ConvertTo-Json -Compress)
 if([int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskDelayed.id)' AND scheduled_at>clock_timestamp() AND attempt_count=0 AND status='QUEUED'") -ne 1){throw 'Delayed Job executed before due'}
 if([int](Sql "SELECT count(*) FROM job_attempts WHERE job_id='$($taskDelayed.id)'") -ne 0){throw 'Delayed Job consumed Attempt before due'}
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskDelayed.id)' AND status='SUCCEEDED' AND attempt_count=1 AND started_at>=scheduled_at") -eq 1} 15 'DB-time delayed eligibility and execution'
 Write-Output 'Smoke PASS: future Job has zero Attempts before due; executes after DB-time due; scheduled wait exceeds timeout without consuming execution deadline.'
 $taskHigh=Post '/api/v1/jobs' '{"type":"SLEEP","payload":{"duration_ms":0},"priority":100,"required_capabilities":["gpu"]}'
 $taskLow=Post '/api/v1/jobs' '{"type":"SLEEP","payload":{"duration_ms":0},"priority":0,"required_capabilities":["cpu"]}'
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskLow.id)' AND status='SUCCEEDED'") -eq 1} 15 'lower compatible CPU Job despite higher GPU Job'
 if([int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskHigh.id)' AND status='QUEUED' AND attempt_count=0") -ne 1){throw 'Incapable worker consumed GPU Attempt'}
 $taskContainers += $taskGpu
 Compose @('run','-d','--no-deps','--name',$taskGpu,'-e','FLOWFORGE_WORKER_CAPABILITIES=gpu','worker') | Out-Null
 AwaitCondition {[int](Sql "SELECT count(*) FROM workers WHERE capabilities='{gpu}' AND status IN ('ONLINE','IDLE','BUSY')") -eq 1} 15 'GPU worker registration'
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs j JOIN workers w ON w.worker_id=j.assigned_worker WHERE j.id='$($taskHigh.id)' AND j.status='SUCCEEDED' AND j.attempt_count=1 AND w.capabilities @> ARRAY['gpu']") -eq 1} 60 'capable worker arrives through 30s durable reconciliation'
 Write-Output 'Smoke PASS: CPU Worker executes compatible lower priority Job; GPU Job remains QUEUED/no Attempt until fresh GPU Worker arrives; succeeds through ordinary reconciliation.'
 $taskSchedule=Post '/api/v1/schedules' '{"type":"SLEEP","payload":{"duration_ms":0},"interval_seconds":4}'
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE schedule_id='$($taskSchedule.id)' AND status='SUCCEEDED'") -ge 2} 15 'two recurring occurrences under concurrent worker schedulers'
 if([int](Sql "SELECT count(*) FROM (SELECT scheduled_for FROM jobs WHERE schedule_id='$($taskSchedule.id)' GROUP BY scheduled_for HAVING count(*)>1) d") -ne 0){throw 'Duplicate recurring occurrence'}
 $taskCancelled=Post ('/api/v1/schedules/'+$taskSchedule.id+'/cancel') '' 200
 if($taskCancelled.status -ne 'CANCELLED'){throw 'Schedule cancellation not durable'}
 $taskAgain=Post ('/api/v1/schedules/'+$taskSchedule.id+'/cancel') '' 200
 if($taskAgain.status -ne 'CANCELLED'){throw 'Repeated schedule cancellation failed'}
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE schedule_id='$($taskSchedule.id)' AND status<>'SUCCEEDED'") -eq 0} 10 'already materialized Jobs settle normally after schedule cancellation'
 $taskOccurrences=[int](Sql "SELECT count(*) FROM jobs WHERE schedule_id='$($taskSchedule.id)'")
 $taskStates=Sql "SELECT json_agg(json_build_object('id',id,'status',status,'attempt_count',attempt_count) ORDER BY id) FROM jobs WHERE schedule_id='$($taskSchedule.id)'"
 Start-Sleep -Seconds 9
 if([int](Sql "SELECT count(*) FROM jobs WHERE schedule_id='$($taskSchedule.id)'") -ne $taskOccurrences){throw 'Cancelled schedule produced future occurrence'}
 if((Sql "SELECT json_agg(json_build_object('id',id,'status',status,'attempt_count',attempt_count) ORDER BY id) FROM jobs WHERE schedule_id='$($taskSchedule.id)'") -ne $taskStates){throw 'Schedule cancellation changed existing Jobs'}
 Write-Output 'Smoke PASS: at least two unique occurrences with two concurrent schedulers; repeated cancel idempotent; no new occurrences across two intervals; materialized Jobs unchanged.'
 $taskEvidence=Sql "SELECT json_build_object('schema_version',(SELECT max(version) FROM schema_migrations),'jobs',(SELECT count(*) FROM jobs),'attempts',(SELECT count(*) FROM job_attempts),'succeeded',(SELECT count(*) FROM jobs WHERE status='SUCCEEDED'),'occurrences',(SELECT count(*) FROM jobs WHERE schedule_id IS NOT NULL),'workers',(SELECT count(*) FROM workers))"
} finally {
 $taskHttp.Dispose()
 foreach ($taskContainer in $taskContainers) {
  $taskExisting=& docker ps -a --filter ('name=^/'+$taskContainer+'$') --format '{{.Names}}'
  if ($LASTEXITCODE -ne 0) {throw 'Could not inspect scoped smoke cleanup'}
  if ($taskExisting -eq $taskContainer) {RunDocker @('rm','-f',$taskContainer) | Out-Null}
 }
 if ($taskCreated) {
  if ($taskDatabase -notmatch '^ff_phase7_smoke_[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe cleanup database identifier'}
  RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('DROP DATABASE '+$taskDatabase)) | Out-Null
 }
 if ($taskStream -notmatch '^flowforge:test:phase7:[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe cleanup stream'}
 RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli DEL "$1"','sh',$taskStream) | Out-Null
 $taskRemaining=RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli EXISTS "$1"','sh',$taskStream)
 if([int]$taskRemaining -ne 0){throw 'Smoke stream cleanup failed'}
 if(Test-Path -LiteralPath $taskOverride){Remove-Item -LiteralPath $taskOverride}
 if($taskCreated){$taskRemaining=RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-A','-t','-c',("SELECT count(*) FROM pg_database WHERE datname='$taskDatabase'"));if([int]$taskRemaining -ne 0){throw 'Smoke DB cleanup failed'}}
 $taskAfter=RetainedAudit
 if($taskBefore -ne $taskAfter){throw 'Retained database audit changed'}
}
Write-Output ('FlowForge Phase 7 Smoke PASS: '+$taskEvidence)
Write-Output ('Retained audit unchanged: '+$taskAfter)
Write-Output 'Cleanup PASS: generated containers/database/key removed; normal development services/data preserved.'
