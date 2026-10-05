# Real process smoke, including Vite REST/WS proxy. No browser E2E claim.
# Only generated containers/DB/stream/channel are touched; retained data audited.
$ErrorActionPreference='Stop'
$taskRoot=Split-Path $PSScriptRoot -Parent
Set-Location -LiteralPath $taskRoot
$taskSuffix=[DateTime]::UtcNow.ToString('yyyyMMddHHmmss')+'_'+[Guid]::NewGuid().ToString('N').Substring(0,8)
$taskDatabase='ff_phase8_smoke_'+$taskSuffix
$taskStream='flowforge:test:phase8:'+$taskSuffix
$taskOverride=Join-Path $taskRoot ('tmp/phase8-smoke-'+$taskSuffix+'.yml')
$taskApi='ff-phase8-api-'+$taskSuffix
$taskApi2='ff-phase8-api2-'+$taskSuffix
$taskWeb='ff-phase8-web-'+$taskSuffix
$taskA='ff-phase8-worker-a-'+$taskSuffix
$taskB='ff-phase8-worker-b-'+$taskSuffix
$taskContainers=@()
$taskSockets=@()
$taskCreated=$false
$taskHttp=[Net.Http.HttpClient]::new()
$taskHttp.Timeout=[TimeSpan]::FromSeconds(10)
function RunDocker {
 param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments)
 $result=& docker @Arguments
 if($LASTEXITCODE -ne 0){throw ('Docker command failed: '+$Arguments[0])}
 return $result
}
function Compose {
 param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments)
 RunDocker (@('compose','-f','docker-compose.yml','-f',$taskOverride)+$Arguments)
}
function Sql([string]$Query){
 if($taskDatabase -notmatch '^ff_phase8_smoke_[0-9]{14}_[0-9a-f]{8}$'){throw 'Unsafe generated DB identifier'}
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d',$taskDatabase,'-A','-t','-v','ON_ERROR_STOP=1','-c',$Query)
}
function RetainedAudit {
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-A','-t','-v','ON_ERROR_STOP=1','-c',"SELECT json_build_object('legacy_duplicate_groups',(SELECT count(*) FROM (SELECT idempotency_key FROM jobs WHERE idempotency_key IS NOT NULL GROUP BY idempotency_key HAVING count(*)>1) d),'schema_version',(SELECT max(version) FROM schema_migrations))")
}
function AwaitCondition([scriptblock]$Predicate,[int]$Seconds,[string]$Step){
 $deadline=[DateTime]::UtcNow.AddSeconds($Seconds)
 while([DateTime]::UtcNow -lt $deadline){if(& $Predicate){return};Start-Sleep -Milliseconds 100}
 throw ('Timed out: '+$Step)
}
function Post([string]$Path,[string]$Body,[int]$Code=201){
 $content=[Net.Http.StringContent]::new($Body,[Text.Encoding]::UTF8,'application/json')
 try{$response=$taskHttp.PostAsync($taskBase+$Path,$content).GetAwaiter().GetResult()
  try{if([int]$response.StatusCode -ne $Code){throw ('Command HTTP status '+[int]$response.StatusCode)};return ($response.Content.ReadAsStringAsync().GetAwaiter().GetResult()|ConvertFrom-Json)}finally{$response.Dispose()}
 }finally{$content.Dispose()}
}
function ConnectHints([string]$Base){
 $socket=[Net.WebSockets.ClientWebSocket]::new()
 $deadline=[Threading.CancellationTokenSource]::new([TimeSpan]::FromSeconds(10))
 try{[void]$socket.ConnectAsync([Uri]($Base.Replace('http://','ws://')+'/api/v1/ws'),$deadline.Token).GetAwaiter().GetResult()}finally{$deadline.Dispose()}
 $state=[PSCustomObject]@{Socket=$socket;Buffer=[byte[]]::new(2048);Pending=$null;Hints=[Collections.Generic.List[object]]::new()}
 $state.Pending=$socket.ReceiveAsync([ArraySegment[byte]]::new($state.Buffer),[Threading.CancellationToken]::None)
 return $state
}
function DrainHints($State){
 while($State.Pending.IsCompleted){
  $received=$State.Pending.GetAwaiter().GetResult()
  if($received.MessageType -ne [Net.WebSockets.WebSocketMessageType]::Text -or !$received.EndOfMessage){throw 'Invalid hint frame'}
  $hint=[Text.Encoding]::UTF8.GetString($State.Buffer,0,$received.Count)|ConvertFrom-Json
  if($hint.version -ne 1 -or $hint.event -notin @('job.changed','worker.changed','schedule.changed','system.changed')){throw 'Invalid hint contract'}
  if($State.Hints.Count -ge 2048){$State.Hints.RemoveAt(0)}
  $State.Hints.Add($hint)
  $State.Pending=$State.Socket.ReceiveAsync([ArraySegment[byte]]::new($State.Buffer),[Threading.CancellationToken]::None)
 }
}
function AwaitHint($State,[string]$ID,[string]$Status){
 AwaitCondition {DrainHints $State;@($State.Hints|Where-Object {$_.resource_id -eq $ID -and $_.hint.status -eq $Status}).Count -gt 0} 15 ('WS '+$Status)
}
$taskBefore=RetainedAudit
try {
 New-Item -ItemType Directory -Force -Path (Join-Path $taskRoot 'tmp')|Out-Null
 @"
services:
  migrate:
    image: flowforge-phase8-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
  api:
    image: flowforge-phase8-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
  worker:
    image: flowforge-phase8-smoke
    environment:
      FLOWFORGE_POSTGRES_DB: $taskDatabase
      FLOWFORGE_REDIS_STREAM: $taskStream
      FLOWFORGE_WORKER_CONCURRENCY: "1"
      FLOWFORGE_LEASE_SECONDS: "3"
      FLOWFORGE_RENEW_SECONDS: "1"
      FLOWFORGE_HEARTBEAT_SECONDS: "1"
      FLOWFORGE_OFFLINE_SECONDS: "4"
  dashboard:
    image: flowforge-phase8-dashboard-smoke
    environment:
      FLOWFORGE_API_TARGET: http://${taskApi}:8080
"@|Set-Content -LiteralPath $taskOverride -Encoding ascii
 RunDocker @('build','-t','flowforge-phase8-smoke','.')|Out-Null
 RunDocker @('build','-t','flowforge-phase8-dashboard-smoke','web')|Out-Null
 if($taskDatabase -notmatch '^ff_phase8_smoke_[0-9]{14}_[0-9a-f]{8}$'){throw 'Unsafe generated DB identifier'}
 RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('CREATE DATABASE '+$taskDatabase))|Out-Null
 $taskCreated=$true
 Compose @('run','--rm','--no-deps','migrate','/app/migrate','up')|Out-Null
 foreach($name in @($taskApi,$taskApi2)){
  $taskContainers+=$name
  Compose @('run','-d','--no-deps','--name',$name,'-p','127.0.0.1::8080','api')|Out-Null
  $base='http://'+(RunDocker @('port',$name,'8080/tcp'))
  AwaitCondition {try{(Invoke-RestMethod -Uri ($base+'/ready')).status -eq 'ready'}catch{$false}} 30 'isolated API readiness'
  if($name -eq $taskApi2){$taskSecondBase=$base}
 }
 $taskContainers+=$taskWeb
 Compose @('run','-d','--no-deps','--name',$taskWeb,'-p','127.0.0.1::5173','dashboard')|Out-Null
 $taskBase='http://'+(RunDocker @('port',$taskWeb,'5173/tcp'))
 AwaitCondition {try{(Invoke-WebRequest -Uri $taskBase).Content -like '*FlowForge*'}catch{$false}} 30 'dashboard dev server'
 $primary=ConnectHints $taskBase;$taskSockets+=$primary
 $secondary=ConnectHints $taskSecondBase;$taskSockets+=$secondary
 foreach($s in @($primary,$secondary)){AwaitHint $s '00000000-0000-0000-0000-000000000000' 'LIVE'}
 $initial=Invoke-RestMethod -Uri ($taskBase+'/api/v1/dashboard/summary')
 if($initial.queue_depth -ne 0 -or $initial.jobs.QUEUED -ne 0){throw 'Initial authoritative snapshot incorrect'}
 $taskContainers+=$taskA
 Compose @('run','-d','--no-deps','--name',$taskA,'-e','FLOWFORGE_WORKER_CAPABILITIES=cpu','worker')|Out-Null
 AwaitCondition {[int](Sql "SELECT count(*) FROM workers WHERE capabilities='{cpu}'") -eq 1} 15 'worker A'
 $taskWorkerA=(Sql "SELECT worker_id FROM workers WHERE capabilities='{cpu}'").Trim()
 $taskContainers+=$taskB
 Compose @('run','-d','--no-deps','--name',$taskB,'-e','FLOWFORGE_WORKER_CAPABILITIES=cpu,gpu','worker')|Out-Null
 AwaitCondition {[int](Sql "SELECT count(*) FROM workers WHERE capabilities='{cpu,gpu}'") -eq 1} 15 'worker B'
 $taskWorkerB=(Sql "SELECT worker_id FROM workers WHERE capabilities='{cpu,gpu}'").Trim()
 $simple=Post '/api/v1/jobs' '{"type":"SLEEP","payload":{"duration_ms":1000},"required_capabilities":["cpu"]}'
 foreach($s in @($primary,$secondary)){foreach($status in @('QUEUED','RUNNING','SUCCEEDED')){AwaitHint $s $simple.id $status}}
 if((Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$simple.id)).status -ne 'SUCCEEDED'){throw 'REST success mismatch'}
 Write-Output 'Smoke PASS: Vite proxy and two independent API hubs observe QUEUED/RUNNING/SUCCEEDED; PostgreSQL snapshot agrees.'
 $long=Post '/api/v1/jobs' '{"type":"SLEEP","payload":{"duration_ms":10000},"required_capabilities":["cpu"],"timeout":30}'
 AwaitHint $primary $long.id 'RUNNING'
 $running=Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$long.id)
 $victim=if($running.assigned_worker -eq $taskWorkerA){$taskA}elseif($running.assigned_worker -eq $taskWorkerB){$taskB}else{throw 'Unknown isolated worker owner'}
 RunDocker @('kill',$victim)|Out-Null
 AwaitCondition {$j=Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$long.id);$j.status -eq 'SUCCEEDED' -and $j.attempt_count -eq 2} 40 'kill/lease recovery/new Attempt'
 AwaitHint $primary $long.id 'RETRYING'
 AwaitHint $secondary $long.id 'SUCCEEDED'
 AwaitHint $primary $running.assigned_worker 'OFFLINE'
 $attempts=Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$long.id+'/attempts')
 if($attempts.attempts.Count -ne 2 -or $attempts.attempts[0].error -ne 'lease_expired' -or $attempts.attempts[1].status -ne 'SUCCEEDED'){throw 'Crash Attempt history incorrect'}
 Write-Output 'Smoke PASS: killed claimed worker; survivor recovers lease, records expired Attempt, and succeeds in Attempt 2; worker OFFLINE hint received.'
 $primary.Socket.Abort()
 $missed=Post '/api/v1/jobs' '{"type":"SLEEP","payload":{"duration_ms":50}}'
 AwaitCondition {(Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$missed.id)).status -eq 'SUCCEEDED'} 15 'completion while disconnected'
 $primary=ConnectHints $taskBase;$taskSockets+=$primary
 AwaitHint $primary '00000000-0000-0000-0000-000000000000' 'LIVE'
 $snapshot=Invoke-RestMethod -Uri ($taskBase+'/api/v1/dashboard/summary')
 if($snapshot.jobs.SUCCEEDED -ne 3 -or (Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$missed.id)).status -ne 'SUCCEEDED'){throw 'Reconnect REST resync failed'}
 $dead=Post '/api/v1/jobs' '{"type":"UNSUPPORTED","payload":{}}'
 AwaitHint $primary $dead.id 'DEAD_LETTER'
 $dlq=Invoke-RestMethod -Uri ($taskBase+'/api/v1/dead-letter')
 if(@($dlq.jobs|Where-Object {$_.id -eq $dead.id}).Count -ne 1){throw 'DLQ list missing job'}
 $redrive=Post ('/api/v1/jobs/'+$dead.id+'/retry') '' 200
 if($redrive.status -ne 'QUEUED' -or $redrive.max_attempts -ne 4){throw 'Redrive response incorrect'}
 AwaitCondition {$j=Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$dead.id);$j.status -eq 'DEAD_LETTER' -and $j.attempt_count -eq 2} 15 'redrive permanent executor rejection'
 $attempts=Invoke-RestMethod -Uri ($taskBase+'/api/v1/jobs/'+$dead.id+'/attempts')
 if($attempts.attempts.Count -ne 2){throw 'Redrive history lost'}
 $schedule=Post '/api/v1/schedules' '{"type":"SLEEP","payload":{"duration_ms":0},"interval_seconds":60,"next_run_at":"2099-01-01T00:00:00Z"}'
 AwaitHint $primary $schedule.id 'ACTIVE'
 Post ('/api/v1/schedules/'+$schedule.id+'/cancel') '' 200|Out-Null
 AwaitHint $secondary $schedule.id 'CANCELLED'
 $workers=Invoke-RestMethod -Uri ($taskBase+'/api/v1/workers')
 if($workers.workers.Count -ne 2 -or @($workers.workers|Where-Object {$_.status -eq 'OFFLINE'}).Count -ne 1){throw 'Worker read model status incorrect'}
 foreach($w in $workers.workers){if($w.concurrency -ne 1 -or !$w.last_heartbeat -or $w.capabilities.Count -lt 1){throw 'Worker read model fields missing'}}
 Write-Output 'Smoke PASS: disconnected client repairs missed completion via reconnect REST; DLQ detail/Attempt history/redrive preserved; schedules and Worker capability/status/active read models verified.'
 $taskEvidence=Sql "SELECT json_build_object('schema_version',(SELECT max(version) FROM schema_migrations),'jobs',(SELECT count(*) FROM jobs),'attempts',(SELECT count(*) FROM job_attempts),'succeeded',(SELECT count(*) FROM jobs WHERE status='SUCCEEDED'),'dead_letter',(SELECT count(*) FROM jobs WHERE status='DEAD_LETTER'),'workers',(SELECT count(*) FROM workers))"
}finally{
 foreach($s in $taskSockets){if($s -and $s.Socket){$s.Socket.Abort();$s.Socket.Dispose()}}
 $taskHttp.Dispose()
 foreach($name in $taskContainers){$existing=& docker ps -a --filter ('name=^/'+$name+'$') --format '{{.Names}}';if($LASTEXITCODE -ne 0){throw 'Scoped cleanup inspection failed'};if($existing -eq $name){RunDocker @('rm','-f',$name)|Out-Null}}
 if($taskCreated){if($taskDatabase -notmatch '^ff_phase8_smoke_[0-9]{14}_[0-9a-f]{8}$'){throw 'Unsafe cleanup DB'};RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-v','ON_ERROR_STOP=1','-c',('DROP DATABASE '+$taskDatabase))|Out-Null}
 if($taskStream -notmatch '^flowforge:test:phase8:[0-9]{14}_[0-9a-f]{8}$'){throw 'Unsafe cleanup namespace'}
 RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli DEL "$1"','sh',$taskStream)|Out-Null
 $remaining=RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli EXISTS "$1"','sh',$taskStream)
 if([int]$remaining -ne 0){throw 'Generated stream remains'}
 $subscribers=RunDocker @('compose','exec','-T','redis','sh','-c','if [ -n "$REDIS_PASSWORD" ]; then export REDISCLI_AUTH="$REDIS_PASSWORD"; fi; redis-cli PUBSUB NUMSUB "$1"','sh',($taskStream+':ui:v1'))
 if([int]$subscribers[-1] -ne 0){throw 'Generated UI subscribers remain'}
 if(Test-Path -LiteralPath $taskOverride){Remove-Item -LiteralPath $taskOverride}
 if($taskCreated){$remaining=RunDocker @('compose','exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-A','-t','-c',("SELECT count(*) FROM pg_database WHERE datname='$taskDatabase'"));if([int]$remaining -ne 0){throw 'Generated DB remains'}}
 $taskAfter=RetainedAudit;if($taskBefore -ne $taskAfter){throw 'Retained DB audit changed'}
}
Write-Output ('FlowForge Phase 8 Smoke PASS: '+$taskEvidence)
Write-Output ('Retained audit unchanged: '+$taskAfter)
Write-Output 'Cleanup PASS: generated containers/DB/stream removed, UI subscribers zero. Browser E2E NOT RUN; real WebSocket client used.'
