# Dot-source only. Every destructive operation checks generated ownership.
$ErrorActionPreference='Stop'
$phase9Root=Split-Path $PSScriptRoot -Parent
function Phase9Docker {
 param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments)
 $result=& docker @Arguments
 if($LASTEXITCODE -ne 0){throw ('Docker failure: '+$Arguments[0])}
 return $result
}
function Phase9Compose {
 param($Experiment,[Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments)
 if($Experiment.Project -notmatch '^ffp9-[0-9]{14}-[0-9a-f]{8}$'){throw 'Unsafe experiment project'}
 Phase9Docker (@('compose','--env-file',$Experiment.EnvFile,'-p',$Experiment.Project,'-f',(Join-Path $phase9Root 'observability/experiment.yml'))+$Arguments)
}
function Phase9Audit {
 Phase9Docker @('compose','-f',(Join-Path $phase9Root 'docker-compose.yml'),'exec','-T','postgres','psql','-U','flowforge','-d','flowforge','-A','-t','-c',"SELECT json_build_object('duplicates',(SELECT count(*) FROM (SELECT idempotency_key FROM jobs WHERE idempotency_key IS NOT NULL GROUP BY idempotency_key HAVING count(*)>1)d),'schema',(SELECT max(version) FROM schema_migrations))")
}
function Phase9Wait([scriptblock]$Predicate,[int]$Seconds,[string]$Step){
 $until=[DateTime]::UtcNow.AddSeconds($Seconds)
 while([DateTime]::UtcNow -lt $until){if(& $Predicate){return};Start-Sleep -Milliseconds 200}
 throw ('Timeout: '+$Step)
}
function Phase9New([int]$Workers,[bool]$Tracing=$false){
 $suffix=[DateTime]::UtcNow.ToString('yyyyMMddHHmmss')+'-'+[Guid]::NewGuid().ToString('N').Substring(0,8)
 $p='ffp9-'+$suffix;$envFile=Join-Path $phase9Root ('tmp/'+$p+'.env')
 New-Item -ItemType Directory -Force (Join-Path $phase9Root 'tmp')|Out-Null
 [IO.File]::WriteAllText($envFile,"PHASE9_PROJECT=$p`nPHASE9_PASSWORD=$([Guid]::NewGuid().ToString('N'))`nPHASE9_STREAM=flowforge:test:phase9:$suffix`nPHASE9_TRACING=$($Tracing.ToString().ToLowerInvariant())`nPHASE9_LOG_LEVEL=$(if($Tracing){'INFO'}else{'WARN'})`n")
 return [PSCustomObject]@{Project=$p;EnvFile=$envFile;Workers=$Workers;Tracing=$Tracing;Audit=(Phase9Audit)}
}
function Phase9Start($e){
 $args=@('up','-d','--scale',('worker='+$e.Workers));if($e.Tracing){$args=@('--profile','observability')+$args}
 Phase9Compose $e @args|Out-Null
 $api=Phase9Compose $e @('ps','-q','api')
 $e|Add-Member -Force NoteProperty Api ('http://'+(Phase9Docker @('port',$api,'8080/tcp')))
 Phase9Wait {try{(Invoke-RestMethod ($e.Api+'/ready') -TimeoutSec 3).status -eq 'ready'}catch{$false}} 40 'API/schema readiness'
 Phase9Wait {$v=Invoke-RestMethod ($e.Api+'/api/v1/workers') -TimeoutSec 3;@($v.workers|Where-Object {$_.status -ne 'OFFLINE'}).Count -eq $e.Workers} 40 'workers registered'
}
function Phase9Owned($e,[string]$ID){
 if($e.Project -notmatch '^ffp9-[0-9]{14}-[0-9a-f]{8}$'){throw 'Unsafe project'}
 $owner=Phase9Docker @('inspect','--format','{{index .Config.Labels "com.docker.compose.project"}}',$ID)
 if($owner -ne $e.Project){throw 'Container ownership mismatch'}
}
function Phase9Close($e){
 if($e.Project -notmatch '^ffp9-[0-9]{14}-[0-9a-f]{8}$'){throw 'Unsafe cleanup project'}
 $target=[IO.Path]::GetFullPath($e.EnvFile);$allowed=Join-Path ([IO.Path]::GetFullPath((Join-Path $phase9Root 'tmp'))) ($e.Project+'.env');if($target -ne $allowed){throw 'Unsafe cleanup file'}
 $ids=@(Phase9Docker @('ps','-aq','--filter',('label=com.docker.compose.project='+$e.Project)))
 foreach($id in $ids){if($id){Phase9Owned $e $id;$paused=Phase9Docker @('inspect','--format','{{.State.Paused}}',$id);if($paused -eq 'true'){Phase9Docker @('unpause',$id)|Out-Null}}}
 # Volume removal is restricted to this generated project's owned containers;
 # it never invokes down against the retained development Compose project.
 Phase9Compose $e @('--profile','observability','down','--remove-orphans','--volumes')|Out-Null
 $remain=@(Phase9Docker @('ps','-aq','--filter',('label=com.docker.compose.project='+$e.Project)));if($remain.Count -gt 0){throw 'Experiment containers remain'}
 $network=@(Phase9Docker @('network','ls','-q','--filter',('label=com.docker.compose.project='+$e.Project)));if($network.Count -gt 0){throw 'Experiment network remains'}
 if(Test-Path -LiteralPath $target){Remove-Item -LiteralPath $target}
 if($e.Audit -ne (Phase9Audit)){throw 'Retained development audit changed'}
 Write-Output ('Cleanup PASS '+$e.Project+'; retained audit unchanged')
}
function Phase9Post($e,[string]$Body){Invoke-RestMethod ($e.Api+'/api/v1/jobs') -Method Post -ContentType application/json -Body $Body -TimeoutSec 10}
function Phase9Terminal($e,[string]$ID,[int]$Seconds=45){
 Phase9Wait {$j=Invoke-RestMethod ($e.Api+'/api/v1/jobs/'+$ID) -TimeoutSec 3;$j.status -eq 'SUCCEEDED'} $Seconds 'Job success'
 Invoke-RestMethod ($e.Api+'/api/v1/jobs/'+$ID) -TimeoutSec 3
}
function Phase9Metric([string]$Text,[string]$Name){
 $n=0.0;foreach($line in $Text.Split("`n")){if($line -match ('^'+[regex]::Escape($Name)+'(?:\{[^}]*\})? ([0-9.eE+-]+)$')){$n+=[double]::Parse($Matches[1],[Globalization.CultureInfo]::InvariantCulture)}};return $n
}
