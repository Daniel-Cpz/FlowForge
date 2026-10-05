. (Join-Path $PSScriptRoot 'phase9-common.ps1')
foreach($file in @('phase9-common.ps1','phase9-failure.ps1','phase9-benchmark.ps1')){
 $errors=$null;$tokens=$null
 [Management.Automation.Language.Parser]::ParseFile((Join-Path $PSScriptRoot $file),[ref]$tokens,[ref]$errors)|Out-Null
 if($errors.Count){throw ('Parser error '+$file)}
}
# No Docker actions or deletes occur for malformed project identity.
$called=$false
function Phase9Docker {param([Parameter(ValueFromRemainingArguments=$true)][string[]]$Arguments);$script:called=$true;return @()}
$rejected=$false;try{Phase9Close ([PSCustomObject]@{Project='flowforge';EnvFile='unrelated.env'})}catch{$rejected=$true}
if(!$rejected -or $called){throw 'Unsafe project reached Docker cleanup'}
$rejected=$false;try{Phase9Compose ([PSCustomObject]@{Project='ffp9-../flowforge';EnvFile='unrelated.env'}) @('down')}catch{$rejected=$true}
if(!$rejected -or $called){throw 'Unsafe project reached Compose'}
# A valid generated project still cannot delete an unrelated file.
$rejected=$false;try{Phase9Close ([PSCustomObject]@{Project='ffp9-20261005000000-abcdef12';EnvFile=(Join-Path $phase9Root 'README.md')})}catch{$rejected=$true}
if(!$rejected -or !(Test-Path -LiteralPath (Join-Path $phase9Root 'README.md'))){throw 'Unsafe file path accepted'}
if((Phase9Metric "flowforge_test{result=`"ok`"} 2`nflowforge_test{result=`"err`"} 3`n" 'flowforge_test') -ne 5){throw 'Metric parser'}
Write-Output 'PASS: Phase 9 harness parsers, project/file cleanup guards and metric parser'
