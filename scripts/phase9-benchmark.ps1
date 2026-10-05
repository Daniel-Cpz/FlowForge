param([int]$Count=500,[int]$Repetitions=3,[int]$DurationMS=25,[int]$RequestConcurrency=16,[switch]$SkipBuild)
. (Join-Path $PSScriptRoot 'phase9-common.ps1')
if($Count -lt 500 -or $Count -gt 2000 -or $Repetitions -lt 3 -or $Repetitions -gt 5){throw 'Benchmark requires 500..2000 Jobs and 3..5 repetitions'}
if(!$SkipBuild){Phase9Docker @('build','-t','flowforge-phase9-experiment',$phase9Root)|Out-Null}
$revision=(& git -c safe.directory=C:/Users/Admin/Documents/FlowForge rev-parse HEAD).Trim()
$results=[Collections.Generic.List[object]]::new()
foreach($workers in @(1,4,8,16)){
 for($rep=1;$rep -le $Repetitions;$rep++){
  $e=Phase9New $workers
  try{
   Phase9Start $e
   $warm=Phase9Compose $e @('run','--rm','--no-deps','loadgen','-url','http://api:8080','-count','50','-concurrency',"$RequestConcurrency",'-duration-ms',"$DurationMS",'-max-wait','3m')|ConvertFrom-Json
   if($warm.succeeded -ne 50){throw 'Warmup incomplete'}
   $data=Phase9Compose $e @('run','--rm','--no-deps','loadgen','-url','http://api:8080','-count',"$Count",'-concurrency',"$RequestConcurrency",'-duration-ms',"$DurationMS",'-max-wait','5m')|ConvertFrom-Json
   if($data.succeeded -ne $Count -or $data.error_rate -ne 0){throw 'Benchmark incomplete or Job errors'}
   $row=[PSCustomObject]@{workers=$workers;repetition=$rep;commit=$revision;project=$e.Project;summary=$data}
   $results.Add($row)
   Write-Output ('Benchmark '+$workers+' Workers / repetition '+$rep+': '+[Math]::Round($data.throughput_jobs_per_second,2)+' jobs/s; queue P95 '+[Math]::Round($data.queue_latency.p95_seconds,3)+'s; errors '+$data.error_rate)
   # Persist completed repetitions before cleanup or a later experiment can fail.
   New-Item -ItemType Directory -Force (Join-Path $phase9Root 'docs/benchmarks')|Out-Null
   [IO.File]::WriteAllText((Join-Path $phase9Root 'docs/benchmarks/phase-9-results.json'),($results.ToArray()|ConvertTo-Json -Depth 8))
  }finally{Phase9Close $e}
 }
}
Write-Output 'Phase 9 benchmark matrix PASS (1/4/8/16, three or more repetitions each)'
