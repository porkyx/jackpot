$ErrorActionPreference='Stop'
$releaseSource=Join-Path $PSScriptRoot 'pre-release.ps1'
$tokens=$null;$parseErrors=$null
$ast=[Management.Automation.Language.Parser]::ParseFile($releaseSource,[ref]$tokens,[ref]$parseErrors)
if($parseErrors.Count -ne 0){throw 'Release source does not parse'}
$loops=@($ast.FindAll({param($node) $node -is [Management.Automation.Language.ForEachStatementAst] -and $node.Variable.VariablePath.UserPath -eq 'gate'},$true))
$mandatory=@($loops|Where-Object {$_.Condition.Extent.Text.Contains("'humanReview'")})
$physical=@($loops|Where-Object {$_.Condition.Extent.Text.Contains("'nativeIME'")})
if($mandatory.Count -ne 1 -or $physical.Count -ne 1){throw 'Release gate bodies are not uniquely identified'}
function Gate-Body($loop){$text=$loop.Body.Extent.Text;return [scriptblock]::Create($text.Substring(1,$text.Length-2))}
$requiredBody=Gate-Body $mandatory[0]
$physicalBody=Gate-Body $physical[0]
$cases=@(
    @{name='actual-pass';value=@{status='passed';evidence='observed';approvedBy='';};accept=$true},
    @{name='explicit-user-exclusion';value=@{status='excluded_by_user';evidence='user scope';approvedBy='user'};accept=$true},
    @{name='exclusion-without-user';value=@{status='excluded_by_user';evidence='scope';approvedBy='agent'};accept=$false},
    @{name='exclusion-empty-approval';value=@{status='excluded_by_user';evidence='scope';approvedBy=''};accept=$false},
    @{name='exclusion-no-evidence';value=@{status='excluded_by_user';evidence='';approvedBy='user'};accept=$false},
    @{name='exclusion-whitespace';value=@{status='excluded_by_user';evidence=' ';approvedBy='user'};accept=$false},
    @{name='pass-no-evidence';value=@{status='passed';evidence='';approvedBy='user'};accept=$false},
    @{name='pending-is-not-pass';value=@{status='pending';evidence='waiting';approvedBy='user'};accept=$false},
    @{name='unknown-status';value=@{status='unknown';evidence='present';approvedBy='user'};accept=$false},
    @{name='missing-gate';value=$null;accept=$false}
)
$checked=0
foreach($gate in @('nativeIME','nativeSleep')){
    foreach($case in $cases){
        $acceptance=[pscustomobject]@{gates=@{$gate=$case.value}}
        $accepted=$true
        try{& $physicalBody}catch{$accepted=$false}
        if($accepted -ne $case.accept){throw ('Physical gate mismatch: '+$gate+'/'+$case.name)}
        $checked++
    }
}
foreach($gate in @('fuzz','mutation','resources','performance','rollback','humanReview')){
    foreach($case in @($cases[0],$cases[1],$cases[6],$cases[7],$cases[9])){
        $acceptance=[pscustomobject]@{gates=@{$gate=$case.value}}
        $accepted=$true
        try{& $requiredBody}catch{$accepted=$false}
        if($accepted -ne ($case.name -eq 'actual-pass')){throw ('Mandatory gate mismatch: '+$gate+'/'+$case.name)}
        $checked++
    }
}
Write-Output ('Release gate assertions passed: '+$checked)
