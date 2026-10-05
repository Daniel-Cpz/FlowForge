# Real API/process acceptance on a generated database and stream. Existing
# development API/workers/images/data remain running and are never replaced.
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path $PSScriptRoot -Parent
Set-Location -LiteralPath $taskRoot
$taskSuffix = [DateTime]::UtcNow.ToString('yyyyMMddHHmmss') + '_' + [Guid]::NewGuid().ToString('N').Substring(0,8)
$taskDatabase = 'ff_phase6_smoke_' + $taskSuffix
$taskStream = 'flowforge:test:phase6:' + $taskSuffix
$taskOverride = Join-Path $taskRoot ('tmp/phase6-smoke-' + $taskSuffix + '.yml')
$taskApi = 'ff-phase6-api-' + $taskSuffix
$taskWorker1 = 'ff-phase6-first-' + $taskSuffix
$taskWorker2 = 'ff-phase6-second-' + $taskSuffix
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
 if ($taskDatabase -notmatch '^ff_phase6_smoke_[0-9]{14}_[0-9a-f]{8}$') { throw 'Unsafe smoke database identifier' }
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d',$taskDatabase,'-A','-t','-v','ON_ERROR_STOP=1','-c',$Query)
}
function AwaitCondition([scriptblock]$Predicate,[int]$Seconds,[string]$Step) {
 $taskDeadline=[DateTime]::UtcNow.AddSeconds($Seconds)
 while ([DateTime]::UtcNow -lt $taskDeadline) { if (& $Predicate) {return}; Start-Sleep -Milliseconds 100 }
 throw ('Timed out: '+$Step)
}
function Post([string]$Body) {
 $taskContent=[Net.Http.StringContent]::new($Body,[Text.Encoding]::UTF8,'application/json')
 try {return $taskHttp.PostAsync($taskBase+'/api/v1/jobs',$taskContent).GetAwaiter().GetResult()} finally {$taskContent.Dispose()}
}
try {
 New-Item -ItemType Directory -Force -Path (Join-Path $taskRoot 'tmp') | Out-Null
 @"
services:
  migrate:
    image: flowforge-phase6-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
  api:
    image: flowforge-phase6-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
  worker:
    image: flowforge-phase6-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
      FLOWFORGE_WORKER_CONCURRENCY: "1"
      FLOWFORGE_RETRY_BASE_SECONDS: "4"
      FLOWFORGE_RETRY_MAX_SECONDS: "4"
"@ | Set-Content -LiteralPath $taskOverride -Encoding ascii
 RunDocker @('build','-t','flowforge-phase6-smoke','.') | Out-Null
 if ($taskDatabase -notmatch '^ff_phase6_smoke_[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe smoke database identifier'}
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('CREATE DATABASE '+$taskDatabase)) | Out-Null
 $taskCreated=$true
 Compose @('run','--rm','--no-deps','migrate','/app/migrate','up') | Out-Null
 $taskContainers += $taskApi
 Compose @('run','-d','--no-deps','--name',$taskApi,'-p','127.0.0.1::8080','api') | Out-Null
 $taskPort=RunDocker @('port',$taskApi,'8080/tcp')
 $taskBase='http://'+$taskPort
 AwaitCondition {try {(Invoke-RestMethod -Uri ($taskBase+'/ready')).status -eq 'ready'} catch {$false}} 30 'isolated API readiness'
 function NewJob([string]$Body) {
  $taskResponse=Post $Body
  try { if ([int]$taskResponse.StatusCode -ne 201) {throw 'Job create failed'};return ($taskResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult()|ConvertFrom-Json) } finally {$taskResponse.Dispose()}
 }
 function Command([string]$Id,[string]$Action,[int]$Code=200) {
  $taskResponse=$taskHttp.PostAsync($taskBase+'/api/v1/jobs/'+$Id+'/'+$Action,[Net.Http.StringContent]::new('')).GetAwaiter().GetResult()
  try{if([int]$taskResponse.StatusCode -ne $Code){throw ('Command failed: '+$Action)};return ($taskResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult()|ConvertFrom-Json)}finally{$taskResponse.Dispose()}
 }
 $taskBarrier=NewJob '{"type":"SLEEP","payload":{"duration_ms":10000},"priority":10}'
 $taskContainers += $taskWorker1
 Compose @('run','-d','--no-deps','--name',$taskWorker1,'worker') | Out-Null
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskBarrier.id)' AND status='RUNNING'") -eq 1} 15 'long low priority execution barrier'
 $taskLow=NewJob '{"type":"SLEEP","payload":{"duration_ms":250},"priority":0}'
 $taskHigh=NewJob '{"type":"SLEEP","payload":{"duration_ms":0},"priority":100}'
 if([int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskBarrier.id)' AND status='RUNNING'") -ne 1){throw 'High priority preempted running low Job'}
 if([int](Sql "SELECT count(*) FROM jobs WHERE id IN ('$($taskLow.id)','$($taskHigh.id)') AND attempt_count=0") -ne 2){throw 'Claim barrier violated'}
 $taskRequested=Command $taskBarrier.id 'cancel'
 if($taskRequested.status -ne 'RUNNING' -or $null -eq $taskRequested.cancel_requested_at){throw 'No durable running cancellation request'}
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskBarrier.id)' AND status='CANCELLED' AND attempt_count=1 AND retry_at IS NULL") -eq 1} 8 'cooperative cancellation at renewal'
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id IN ('$($taskLow.id)','$($taskHigh.id)') AND status='SUCCEEDED'") -eq 2} 40 'priority claims after barrier release'
 if([int](Sql "SELECT count(*) FROM jobs h JOIN jobs l ON l.id='$($taskLow.id)' WHERE h.id='$($taskHigh.id)' AND h.started_at<l.started_at") -ne 1){throw 'Low priority crossed authoritative claim boundary'}
 if([int](Sql "SELECT count(*) FROM job_attempts WHERE job_id='$($taskBarrier.id)' AND status='CANCELLED' AND error='user_cancelled' AND finished_at IS NOT NULL") -ne 1){throw 'Cancellation Attempt evidence missing'}
 Write-Output 'Smoke: running low non-preempted; durable cancellation -> one CANCELLED Attempt; high claims before queued low at released barrier.'
 $taskTimed=NewJob '{"type":"SLEEP","payload":{"duration_ms":2000},"timeout":1,"max_attempts":2}'
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskTimed.id)' AND status='RETRYING' AND retry_at>clock_timestamp() AND attempt_count=1") -eq 1} 15 'timeout attempt 1 retry schedule'
 if([int](Sql "SELECT count(*) FROM job_attempts WHERE job_id='$($taskTimed.id)' AND status='TIMED_OUT' AND error='execution_timeout' AND finished_at IS NOT NULL") -ne 1){throw 'Timeout outcome missing'}
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskTimed.id)' AND status='DEAD_LETTER' AND attempt_count=2") -eq 1} 15 'timeout exact exhaustion'
 if([int](Sql "SELECT count(*) FROM job_attempts WHERE job_id='$($taskTimed.id)' AND status='TIMED_OUT'") -ne 2){throw 'Timeout history/budget invalid'}
 Write-Output 'Smoke: SLEEP deadline -> TIMED_OUT / durable RETRYING -> second TIMED_OUT / DEAD_LETTER; no N+1.'
 $taskDlq=NewJob '{"type":"UNSUPPORTED","payload":{},"max_attempts":1,"idempotency_key":"phase6-dlq-key"}'
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskDlq.id)' AND status='DEAD_LETTER' AND attempt_count=1") -eq 1} 15 'permanent DLQ'
 $taskList=Invoke-RestMethod -Uri ($taskBase+'/api/v1/dead-letter?limit=100')
 if(@($taskList.jobs|Where-Object {$_.status -ne 'DEAD_LETTER'}).Count -ne 0 -or @($taskList.jobs|Where-Object {$_.id -eq $taskDlq.id}).Count -ne 1){throw 'DLQ list invalid'}
 $taskBefore=Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$taskDlq.id+'/attempts')
 if($taskBefore.attempts.Count -ne 1 -or $taskBefore.attempts[0].error -ne 'unsupported_job_type'){throw 'DLQ inspect evidence missing'}
 $taskRedriven=Command $taskDlq.id 'retry'
 if($taskRedriven.max_attempts -ne 2 -or $taskRedriven.attempt_count -ne 1){throw 'Redrive budget/history invalid'}
 $taskResponse=Post '{"type":"UNSUPPORTED","payload":{},"max_attempts":1,"idempotency_key":"phase6-dlq-key"}'
 try{if([int]$taskResponse.StatusCode -ne 200 -or ($taskResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult()|ConvertFrom-Json).id -ne $taskDlq.id){throw 'Redrive broke keyed replay'}}finally{$taskResponse.Dispose()}
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$($taskDlq.id)' AND status='DEAD_LETTER' AND attempt_count=2 AND max_attempts=2") -eq 1} 15 'redriven Attempt 2'
 $taskAfter=Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$taskDlq.id+'/attempts')
 if($taskAfter.attempts.Count -ne 2 -or $taskAfter.attempts[0].id -ne $taskBefore.attempts[0].id -or $taskAfter.attempts[1].attempt_number -ne 2){throw 'Redrive erased history'}
 Write-Output 'Smoke: DLQ list + Attempts inspect; redrive grants one budget; historical Attempt retained; keyed replay uses original Job.'
 $taskEvidence=Sql "SELECT json_build_object('jobs',(SELECT count(*) FROM jobs),'attempts',(SELECT count(*) FROM job_attempts),'succeeded',(SELECT count(*) FROM jobs WHERE status='SUCCEEDED'),'cancelled',(SELECT count(*) FROM jobs WHERE status='CANCELLED'),'dead_letter',(SELECT count(*) FROM jobs WHERE status='DEAD_LETTER'),'timeouts',(SELECT count(*) FROM job_attempts WHERE status='TIMED_OUT'))"
} finally {
 $taskHttp.Dispose()
 foreach ($taskContainer in $taskContainers) {
  $taskExisting=& docker ps -a --filter ('name=^/'+$taskContainer+'$') --format '{{.Names}}'
  if ($LASTEXITCODE -ne 0) {throw 'Could not inspect scoped smoke cleanup'}
  if ($taskExisting -eq $taskContainer) {RunDocker @('rm','-f',$taskContainer) | Out-Null}
 }
 if ($taskCreated) {
  if ($taskDatabase -notmatch '^ff_phase6_smoke_[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe cleanup database identifier'}
  RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('DROP DATABASE '+$taskDatabase)) | Out-Null
 }
 if ($taskStream -notmatch '^flowforge:test:phase6:[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe cleanup stream'}
 RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli DEL "$1"','sh',$taskStream) | Out-Null
 $taskKeysRemaining = RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli EXISTS "$1"','sh',$taskStream)
 if ([int]$taskKeysRemaining -ne 0) {throw 'Smoke stream cleanup failed'}
 if (Test-Path -LiteralPath $taskOverride) {Remove-Item -LiteralPath $taskOverride}
 if ($taskCreated) {
  $taskRemaining=RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-A','-t','-c',("SELECT count(*) FROM pg_database WHERE datname='$taskDatabase'"))
  if ([int]$taskRemaining -ne 0) {throw 'Smoke database cleanup failed'}
 }
}
Write-Output ('FlowForge Phase 6 Smoke PASS: '+$taskEvidence)
Write-Output 'Cleanup PASS: generated containers/database/key removed; normal development services/data preserved.'
