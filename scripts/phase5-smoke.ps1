# Real API/process acceptance on a generated database and stream. Existing
# development API/workers/images/data remain running and are never replaced.
$ErrorActionPreference = 'Stop'
$taskRoot = Split-Path $PSScriptRoot -Parent
Set-Location -LiteralPath $taskRoot
$taskSuffix = [DateTime]::UtcNow.ToString('yyyyMMddHHmmss') + '_' + [Guid]::NewGuid().ToString('N').Substring(0,8)
$taskDatabase = 'ff_phase5_smoke_' + $taskSuffix
$taskStream = 'flowforge:test:phase5:' + $taskSuffix
$taskOverride = Join-Path $taskRoot ('tmp/phase5-smoke-' + $taskSuffix + '.yml')
$taskApi = 'ff-phase5-api-' + $taskSuffix
$taskWorker1 = 'ff-phase5-first-' + $taskSuffix
$taskWorker2 = 'ff-phase5-second-' + $taskSuffix
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
 if ($taskDatabase -notmatch '^ff_phase5_smoke_[0-9]{14}_[0-9a-f]{8}$') { throw 'Unsafe smoke database identifier' }
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
    image: flowforge-phase5-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
  api:
    image: flowforge-phase5-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
  worker:
    image: flowforge-phase5-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
      FLOWFORGE_WORKER_CONCURRENCY: "1"
      FLOWFORGE_RETRY_BASE_SECONDS: "8"
      FLOWFORGE_RETRY_MAX_SECONDS: "8"
"@ | Set-Content -LiteralPath $taskOverride -Encoding ascii
 RunDocker @('build','-t','flowforge-phase5-smoke','.') | Out-Null
 if ($taskDatabase -notmatch '^ff_phase5_smoke_[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe smoke database identifier'}
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('CREATE DATABASE '+$taskDatabase)) | Out-Null
 $taskCreated=$true
 Compose @('run','--rm','--no-deps','migrate','/app/migrate','up') | Out-Null
 $taskContainers += $taskApi
 Compose @('run','-d','--no-deps','--name',$taskApi,'-p','127.0.0.1::8080','api') | Out-Null
 $taskPort=RunDocker @('port',$taskApi,'8080/tcp')
 $taskBase='http://'+$taskPort
 AwaitCondition {try {(Invoke-RestMethod -Uri ($taskBase+'/ready')).status -eq 'ready'} catch {$false}} 30 'isolated API readiness'
 $taskBody='{"type":"SLEEP","payload":{"duration_ms":0},"idempotency_key":"phase5-smoke-key"}'
 $taskPending=@();$taskContents=@()
 foreach ($taskIndex in 1..8) {
  $taskContent=[Net.Http.StringContent]::new($taskBody,[Text.Encoding]::UTF8,'application/json')
  $taskContents += $taskContent
  $taskPending += $taskHttp.PostAsync($taskBase+'/api/v1/jobs',$taskContent)
 }
 $taskCodes=@();$taskIds=@()
 foreach ($taskRequest in $taskPending) {
  $taskResponse=$taskRequest.GetAwaiter().GetResult()
  try {
   $taskCodes += [int]$taskResponse.StatusCode
   $taskJob=$taskResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult() | ConvertFrom-Json
   $taskIds += $taskJob.id
   if ($taskResponse.Headers.Location.OriginalString -ne ('/api/v1/jobs/'+$taskJob.id)) {throw 'Replay Location changed'}
  } finally {$taskResponse.Dispose()}
 }
 foreach ($taskContent in $taskContents) {$taskContent.Dispose()}
 if (@($taskCodes | Where-Object {$_ -eq 201}).Count -ne 1 -or @($taskCodes | Where-Object {$_ -eq 200}).Count -ne 7 -or @($taskIds | Select-Object -Unique).Count -ne 1) {throw 'Concurrent keyed submission contract failed'}
 if ([int](Sql 'SELECT count(*) FROM jobs') -ne 1 -or [int](Sql 'SELECT count(*) FROM job_dispatch') -ne 1) {throw 'Replay created another durable record'}
 $taskResponse=Post ($taskBody.Replace('"duration_ms":0','"duration_ms":1'))
 try {
  if ([int]$taskResponse.StatusCode -ne 409 -or ($taskResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult() | ConvertFrom-Json).error.code -ne 'IDEMPOTENCY_CONFLICT') {throw 'Conflict contract failed'}
 } finally {$taskResponse.Dispose()}
 $taskNoKey=@()
 foreach ($taskIndex in 1..2) {
  $taskResponse=Post '{"type":"SLEEP","payload":{"duration_ms":0}}'
  try {if ([int]$taskResponse.StatusCode -ne 201) {throw 'No-key create failed'}; $taskNoKey += ($taskResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult() | ConvertFrom-Json).id} finally {$taskResponse.Dispose()}
 }
 if ($taskNoKey[0] -eq $taskNoKey[1]) {throw 'No-key incorrectly deduplicated'}
 Write-Output 'Smoke: concurrent keyed requests = 1 created + 7 replayed; 1 Job/outbox; conflict=409; no-key IDs distinct.'
 $taskContainers += $taskWorker1
 Compose @('run','-d','--no-deps','--name',$taskWorker1,'worker') | Out-Null
 $taskResponse=Post '{"type":"SLEEP","payload":{"duration_ms":5000},"max_attempts":2}'
 try {if ([int]$taskResponse.StatusCode -ne 201) {throw 'Retry job create failed'};$taskRetryId=($taskResponse.Content.ReadAsStringAsync().GetAwaiter().GetResult() | ConvertFrom-Json).id} finally {$taskResponse.Dispose()}
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$taskRetryId' AND status='RUNNING' AND attempt_count=1") -eq 1} 15 'attempt 1 claim'
 RunDocker @('stop','--time','15',$taskWorker1) | Out-Null
 if ([int](RunDocker @('inspect','--format','{{.State.ExitCode}}',$taskWorker1)) -ne 0) {throw 'Controlled shutdown failed'}
 if ([int](Sql "SELECT count(*) FROM jobs WHERE id='$taskRetryId' AND status='RETRYING' AND retry_at>clock_timestamp() AND assigned_worker IS NULL AND lease_expiry IS NULL AND attempt_count=1") -ne 1) {throw 'No durable future retry schedule'}
 if ([int](Sql "SELECT count(*) FROM job_attempts WHERE job_id='$taskRetryId' AND attempt_number=1 AND status='FAILED' AND error='execution_cancelled' AND finished_at IS NOT NULL") -ne 1) {throw 'First Attempt evidence missing'}
 $taskContainers += $taskWorker2
 Compose @('run','-d','--no-deps','--name',$taskWorker2,'worker') | Out-Null
 # Check authoritative DB time, not host time. Test catches early promotion.
 if ([int](Sql "SELECT count(*) FROM jobs WHERE id='$taskRetryId' AND status='RETRYING' AND retry_at>clock_timestamp() AND attempt_count=1") -ne 1) {throw 'Restart promoted retry before due'}
 if ([int](Sql "SELECT count(*) FROM job_attempts WHERE job_id='$taskRetryId'") -ne 1) {throw 'Early retry consumed attempt'}
 AwaitCondition {[int](Sql "SELECT count(*) FROM jobs WHERE id='$taskRetryId' AND status='SUCCEEDED' AND attempt_count=2 AND retry_at IS NULL") -eq 1} 25 'due retry/new Attempt success'
 if ([int](Sql "SELECT count(*) FROM job_attempts WHERE job_id='$taskRetryId' AND ((attempt_number=1 AND status='FAILED' AND error='execution_cancelled') OR (attempt_number=2 AND status='SUCCEEDED'))") -ne 2) {throw 'Retry history overwritten'}
 if ([int](Sql "SELECT count(DISTINCT worker_id) FROM job_attempts WHERE job_id='$taskRetryId'") -ne 2) {throw 'Restart did not use a fresh worker'}
 Write-Output 'Smoke: SIGTERM attempt 1 -> durable RETRYING; restart before due retained 1 Attempt; due retry -> attempt 2 SUCCEEDED on fresh owner.'
 $taskEvidence=Sql "SELECT json_build_object('jobs',(SELECT count(*) FROM jobs),'attempts',(SELECT count(*) FROM job_attempts),'succeeded',(SELECT count(*) FROM jobs WHERE status='SUCCEEDED'),'retry_history',(SELECT json_agg(json_build_object('attempt',attempt_number,'status',status,'error',error) ORDER BY attempt_number) FROM job_attempts WHERE job_id='$taskRetryId'))"
} finally {
 $taskHttp.Dispose()
 foreach ($taskContainer in $taskContainers) {
  $taskExisting=& docker ps -a --filter ('name=^/'+$taskContainer+'$') --format '{{.Names}}'
  if ($LASTEXITCODE -ne 0) {throw 'Could not inspect scoped smoke cleanup'}
  if ($taskExisting -eq $taskContainer) {RunDocker @('rm','-f',$taskContainer) | Out-Null}
 }
 if ($taskCreated) {
  if ($taskDatabase -notmatch '^ff_phase5_smoke_[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe cleanup database identifier'}
  RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('DROP DATABASE '+$taskDatabase)) | Out-Null
 }
 if ($taskStream -notmatch '^flowforge:test:phase5:[0-9]{14}_[0-9a-f]{8}$') {throw 'Unsafe cleanup stream'}
 RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli DEL "$1"','sh',$taskStream) | Out-Null
 $taskKeysRemaining = RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli EXISTS "$1"','sh',$taskStream)
 if ([int]$taskKeysRemaining -ne 0) {throw 'Smoke stream cleanup failed'}
 if (Test-Path -LiteralPath $taskOverride) {Remove-Item -LiteralPath $taskOverride}
 if ($taskCreated) {
  $taskRemaining=RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-A','-t','-c',("SELECT count(*) FROM pg_database WHERE datname='$taskDatabase'"))
  if ([int]$taskRemaining -ne 0) {throw 'Smoke database cleanup failed'}
 }
}
Write-Output ('FlowForge Phase 5 Smoke PASS: '+$taskEvidence)
Write-Output 'Cleanup PASS: generated containers/database/key removed; normal development services/data preserved.'
