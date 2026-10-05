param([switch]$SkipBuild)
. (Join-Path $PSScriptRoot 'phase9-common.ps1')
if(!$SkipBuild){Phase9Docker @('build','-t','flowforge-phase9-experiment',$phase9Root)|Out-Null}
$e=Phase9New 2 $true
$socket=$null
function WorkerMetrics($Experiment){
 $text='';foreach($id in @(Phase9Compose $Experiment @('ps','-q','worker'))){$port=Phase9Docker @('port',$id,'9091/tcp');try{$text+=(Invoke-WebRequest ('http://'+$port+'/metrics') -TimeoutSec 3).Content+"`n"}catch{}}
 return $text
}
try{
 Phase9Start $e
 $apiID=Phase9Compose $e @('ps','-q','api');$promID=Phase9Compose $e @('ps','-q','prometheus');$grafID=Phase9Compose $e @('ps','-q','grafana');$collector=Phase9Compose $e @('ps','-q','otel-collector');$redisID=Phase9Compose $e @('ps','-q','redis')
 $prom='http://'+(Phase9Docker @('port',$promID,'9090/tcp'));$graf='http://'+(Phase9Docker @('port',$grafID,'3000/tcp'))
 Phase9Wait {$t=(Invoke-RestMethod ($prom+'/api/v1/targets') -TimeoutSec 3).data.activeTargets;@($t|Where-Object {$_.health -eq 'up' -and $_.labels.job -eq 'workers'}).Count -eq 2 -and @($t|Where-Object {$_.health -eq 'up' -and $_.labels.job -eq 'api'}).Count -eq 1} 40 'Prometheus DNS discovers both scaled Workers'
 Phase9Wait {try{(Invoke-RestMethod ($graf+'/api/datasources/uid/flowforge-prometheus/health') -TimeoutSec 3).status -eq 'OK'}catch{$false}} 30 'Grafana datasource'
 if(!(Invoke-RestMethod ($graf+'/api/dashboards/uid/flowforge-overview') -TimeoutSec 3).dashboard.panels.Count){throw 'Grafana dashboard missing'}
 $socket=[Net.WebSockets.ClientWebSocket]::new();$connect=[Threading.CancellationTokenSource]::new([TimeSpan]::FromSeconds(5));try{[void]$socket.ConnectAsync([Uri]($e.Api.Replace('http://','ws://')+'/api/v1/ws'),$connect.Token).GetAwaiter().GetResult()}finally{$connect.Dispose()}
 $buffer=[byte[]]::new(2048)
 $receive=$socket.ReceiveAsync([ArraySegment[byte]]::new($buffer),[Threading.CancellationToken]::None)
 Phase9Wait {$receive.IsCompleted} 5 'initial WS hint';[void]$receive.GetAwaiter().GetResult()
 $metrics=(Invoke-WebRequest ($e.Api+'/metrics')).Content
 if((Phase9Metric $metrics 'flowforge_websocket_connections') -ne 1 -or $metrics -match 'idempotency|payload|password|job_id|worker_id|trace_id'){throw 'Metrics connection or confidentiality contract'}
 # A: hard crash, committed recovery metrics and same trace across Attempts.
 $j=Phase9Post $e '{"type":"SLEEP","payload":{"duration_ms":2000},"timeout":30}'
 Phase9Wait {$running=Invoke-RestMethod ($e.Api+'/api/v1/jobs/'+$j.id);$running.status -eq 'RUNNING'} 15 'crash owner'
 $running=Invoke-RestMethod ($e.Api+'/api/v1/jobs/'+$j.id)
 $victim=$null;foreach($w in @(Phase9Compose $e @('ps','-q','worker'))){$logs=(Phase9Docker @('logs',$w) 2>&1)|Out-String;# INFO suppressed; identify owner through DB process registration below.
  $hostname=Phase9Docker @('inspect','--format','{{.Config.Hostname}}',$w)
  # Worker UUID is intentionally not a metric label; temporarily inspect process
  # start via its structured log (experiments use INFO for correlation evidence).
  if($logs.Contains($running.assigned_worker)){$victim=$w;break}
 }
 if(!$victim){throw 'Cannot identify generated owner from structured log'}
 Phase9Owned $e $victim;Phase9Docker @('kill',$victim)|Out-Null
 $final=Phase9Terminal $e $j.id
 $attempts=Invoke-RestMethod ($e.Api+'/api/v1/jobs/'+$j.id+'/attempts')
 if($final.attempt_count -ne 2 -or $attempts.attempts[0].error -ne 'lease_expired'){throw 'Crash recovery history'}
 $wm=WorkerMetrics $e
 if((Phase9Metric $wm 'flowforge_job_recoveries_total') -lt 1 -or $wm -notmatch 'outcome="lease_expired"\} 1'){throw 'Recovery metric missing'}
 # Correlate the old and new execution Attempts through structured trace IDs.
 $traceIDs=[Collections.Generic.List[string]]::new();$numbers=[Collections.Generic.List[int]]::new()
 foreach($w in @(Phase9Compose $e @('ps','-aq','worker'))){
  foreach($line in (Phase9Docker @('logs',$w) 2>&1)){
   try{$log=([string]$line)|ConvertFrom-Json;if($log.job_id -eq $j.id -and $log.event -eq 'job_claimed' -and $log.trace_id){$traceIDs.Add($log.trace_id);$numbers.Add([int]$log.attempt_number)}}catch{}
  }
 }
 if(@($traceIDs|Select-Object -Unique).Count -ne 1 -or !$numbers.Contains(1) -or !$numbers.Contains(2)){throw 'Crash Attempts not correlated through durable trace'}
 Write-Output 'Failure A PASS: hard crash, lease_expired Attempt, survivor success, recovery metrics.'
 Phase9Compose $e @('--profile','observability','up','-d','--scale','worker=2','worker')|Out-Null
 Phase9Wait {@((Invoke-RestMethod ($e.Api+'/api/v1/workers')).workers|Where-Object {$_.status -ne 'OFFLINE'}).Count -eq 2} 20 'replacement registered'
 # B: Redis outage leaves new Job and intent durable; recovery republishes.
 Phase9Owned $e $redisID;Phase9Docker @('stop',$redisID)|Out-Null
 $queued=Phase9Post $e '{"type":"SLEEP","payload":{"duration_ms":25}}'
 Start-Sleep -Seconds 2
 if((Invoke-RestMethod ($e.Api+'/api/v1/jobs/'+$queued.id)).status -ne 'QUEUED'){throw 'Redis outage invented execution'}
 $metrics=(Invoke-WebRequest ($e.Api+'/metrics')).Content
 if($metrics -notmatch 'realtime_events_published_total\{result="error"\} [1-9]'){throw 'Redis telemetry failure missing'}
 Phase9Docker @('start',$redisID)|Out-Null
 Phase9Terminal $e $queued.id|Out-Null
 Write-Output 'Failure B PASS: Redis outage preserved durable QUEUED state; republication recovered; telemetry publish failure counted.'
 # C: disconnect exactly one owner from its generated dependency network.
 $partition=Phase9Post $e '{"type":"SLEEP","payload":{"duration_ms":5000},"timeout":30}'
 Phase9Wait {(Invoke-RestMethod ($e.Api+'/api/v1/jobs/'+$partition.id)).status -eq 'RUNNING'} 15 'partition claim'
 $owner=(Invoke-RestMethod ($e.Api+'/api/v1/jobs/'+$partition.id)).assigned_worker
 $partitioned=$null;foreach($w in @(Phase9Compose $e @('ps','-q','worker'))){$logs=(Phase9Docker @('logs',$w) 2>&1)|Out-String;if($logs.Contains($owner)){$partitioned=$w;break}}
 if(!$partitioned){throw 'Unknown generated partition owner'};Phase9Owned $e $partitioned
 $network=$e.Project+'_default'
 Phase9Docker @('network','disconnect',$network,$partitioned)|Out-Null
 try{Phase9Terminal $e $partition.id 50|Out-Null}finally{Phase9Docker @('network','connect',$network,$partitioned)|Out-Null}
 $history=Invoke-RestMethod ($e.Api+'/api/v1/jobs/'+$partition.id+'/attempts')
 if($history.attempts[0].error -ne 'lease_expired' -or $history.attempts.Count -ne 2){throw 'Partition authority history'}
 $logs=(Phase9Docker @('logs',$partitioned) 2>&1)|Out-String
 if($logs -notmatch 'heartbeat_failed|lease_renew_failed' -or @($logs.Split("`n")|Where-Object {$_ -like ('*'+$partition.id+'*') -and $_ -match '"event":"job_finished"' -and $_ -match '"status":"SUCCEEDED"'}).Count -gt 0){throw 'Partition failure or false success log'}
 Write-Output 'Failure C PASS: generated owner partition, renew/heartbeat failure, expired lease, takeover; no stale success persisted.'
 # D: kill only the generated API Pub/Sub socket. Worker Stream/PG traffic
 # continues, and existing browsers receive DEGRADED -> LIVE.
 $apiIP=Phase9Docker @('inspect','--format',('{{with index .NetworkSettings.Networks "'+$e.Project+'_default"}}{{.IPAddress}}{{end}}'),$apiID)
 $clientLines=Phase9Compose $e @('exec','-T','redis','redis-cli','CLIENT','LIST','TYPE','pubsub')
 $apiClient=@($clientLines|Where-Object {$_ -match ('addr='+[regex]::Escape($apiIP)+':')})
 if($apiClient.Count -ne 1){throw 'Ambiguous generated API PubSub socket'}
 $clientID=([regex]::Match($apiClient[0],'^id=([0-9]+)')).Groups[1].Value
 $receive=$socket.ReceiveAsync([ArraySegment[byte]]::new($buffer),[Threading.CancellationToken]::None)
 Phase9Compose $e @('exec','-T','redis','redis-cli','CLIENT','KILL','ID',$clientID)|Out-Null
 $states=[Collections.Generic.List[string]]::new()
 $until=[DateTime]::UtcNow.AddSeconds(12)
 while([DateTime]::UtcNow -lt $until -and !($states.Contains('DEGRADED') -and $states.Contains('LIVE'))){
  if($receive.IsCompleted){$frame=$receive.GetAwaiter().GetResult();$hint=[Text.Encoding]::UTF8.GetString($buffer,0,$frame.Count)|ConvertFrom-Json;if($hint.event -eq 'system.changed'){$states.Add($hint.hint.status)};$receive=$socket.ReceiveAsync([ArraySegment[byte]]::new($buffer),[Threading.CancellationToken]::None)}else{Start-Sleep -Milliseconds 50}
 }
 if(!$states.Contains('DEGRADED') -or !$states.Contains('LIVE')){throw 'WS subscription state did not recover'}
 $socket.Abort();$socket.Dispose();$socket=$null
 $lost=Phase9Post $e '{"type":"SLEEP","payload":{"duration_ms":25}}'
 Phase9Terminal $e $lost.id|Out-Null
 $snapshot=Invoke-RestMethod ($e.Api+'/api/v1/dashboard/summary')
 if($snapshot.jobs.SUCCEEDED -lt 4){throw 'Missed completion REST snapshot incorrect'}
 $metrics=(Invoke-WebRequest ($e.Api+'/metrics')).Content
 if((Phase9Metric $metrics 'flowforge_realtime_events_dropped_total') -lt 1){throw 'Realtime loss metric missing'}
 Write-Output 'Failure D PASS: isolated API Pub/Sub loss, DEGRADED/LIVE hints, independent business success, missed completion REST snapshot repair, drop metric.'
 # Collector exporter may be absent without changing execution.
 Phase9Docker @('stop',$collector)|Out-Null
 $unexported=Phase9Post $e '{"type":"SLEEP","payload":{"duration_ms":25}}';Phase9Terminal $e $unexported.id|Out-Null
 Phase9Docker @('start',$collector)|Out-Null
 $exported=Phase9Post $e '{"type":"SLEEP","payload":{"duration_ms":25}}';Phase9Terminal $e $exported.id|Out-Null
 Phase9Wait {$traceLogs=(Phase9Docker @('logs',$collector) 2>&1)|Out-String;$traceLogs.Contains($j.id) -and $traceLogs.Contains('attempt.number') -and $traceLogs.Contains('attempt.execute')} 15 'real OTLP Job/Attempt export'
 $query=Invoke-RestMethod ($prom+'/api/v1/query?query='+[Uri]::EscapeDataString('max(flowforge_jobs_current{status="SUCCEEDED"})'))
 if($query.status -ne 'success' -or $query.data.result.Count -ne 1 -or [double]$query.data.result[0].value[1] -lt 4){throw 'Prometheus authoritative success query failed'}
 foreach($expression in @('sum(flowforge_job_attempt_duration_seconds_count)','sum(flowforge_http_request_duration_seconds_count)')){
  $sample=Invoke-RestMethod ($prom+'/api/v1/query?query='+[Uri]::EscapeDataString($expression))
  if($sample.status -ne 'success' -or $sample.data.result.Count -ne 1 -or [double]$sample.data.result[0].value[1] -le 0){throw 'Prometheus histogram observations missing'}
 }
 Write-Output 'Observability PASS: API and scaled Worker UP; Grafana datasource/dashboard provisioned; actual counters/histograms; WS gauge; OTLP Job/Attempt traces; exporter outage does not affect committed success.'
}finally{
 if($socket){$socket.Abort();$socket.Dispose()}
 Phase9Close $e
}
