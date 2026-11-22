# TestPowerShell's script (O1.2): Lea Martin requests, runs a day-2 action
# and deletes; Marc Keller approves one production request and rejects
# another, and reads an IP range (I1.2). The test runs each request's
# workflow in this order.
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'
$PSStyle.OutputRendering = 'PlainText'
Import-Module $env:RVAV_MODULE -Force
$PSDefaultParameterValues['Wait-RvavRequest:Interval'] = New-TimeSpan -Milliseconds 50

function Assert([bool] $Ok, [string] $What) {
    if (-not $Ok) {
        throw "Not as expected: $What"
    }
}

function Connect-Approver {
    Connect-Rvav -Token (ConvertTo-SecureString -String $env:RVAV_APPROVER_TOKEN -AsPlainText -Force)
}

# The approval of a request, once its workflow asks for it.
function Wait-Approval([string] $Number) {
    $deadline = [datetime]::UtcNow.AddMinutes(1)
    while ([datetime]::UtcNow -lt $deadline) {
        $a = Get-RvavApproval | Where-Object { $_.request.number -eq $Number }
        if ($a) {
            return $a
        }
        Start-Sleep -Milliseconds 50
    }
    throw "No approval of $Number"
}

$me = Connect-Rvav
Assert ($me.UserId -eq 'usr_lmartin') "connected with RVAV_URL and RVAV_TOKEN as $($me.UserId)"

$items = Get-RvavCatalogItem -ProjectId prj_web
Assert ($items.slug -contains 'rhel9-server') "the catalog: $($items.slug -join ', ')"
$item = Get-RvavCatalogItem -ProjectId prj_web -Item rhel9-server
Assert ($item.PSObject.TypeNames[0] -eq 'Rvav.CatalogItem') 'a catalog item is typed'

try {
    New-RvavRequest rhel9-server -ProjectId prj_web -Environment dev -Inputs @{ size = 'XXL' } | Out-Null
    throw 'Not as expected: a size the form does not offer was requested'
} catch {
    Assert ($_.Exception.Message -match 'size: ') "a refused request says why: $($_.Exception.Message)"
}

$placement = @{ cluster = 'GVA-DEV-CL01'; datastore = 'vsan-gva-dev-cl01'; network = 'dvpg-app-dev-vl310' }
$r = $item | New-RvavRequest -Environment dev -Inputs $placement -Justification 'PowerShell, O1.2.' -Wait
Assert ($r.status -eq 'succeeded') "the request is $($r.status)"
Assert ($r.inputs.role -eq 'app' -and $r.inputs.size) "rvav filled in the inputs left out: $($r.inputs | ConvertTo-Json -Compress)"

$d = Get-RvavDeployment -Id $r.deployment_ids[0]
Assert ($d.status -eq 'ready') "the deployment is $($d.status)"
Assert ((Get-RvavDeployment -Mine -ProjectId prj_web).id -contains $d.id) 'my deployments have it'
Assert ((Get-RvavRequest -Mine).number -contains $r.number) 'my requests have it'

$actions = $d | Get-RvavDeploymentAction
$off = @($actions | Where-Object key -EQ power_off)
Assert ($off.Count -eq 1) "power_off: $($actions.key -join ', ')"
$done = $off[0] | Invoke-RvavDeploymentAction -Force -Wait
Assert ($done.status -eq 'succeeded' -and $done.kind -eq 'day2') "power_off is $($done.status)"

Remove-RvavDeployment $d.id -WhatIf
Assert ((Get-RvavDeployment $d.id).status -eq 'ready') '-WhatIf deletes nothing'

$prd = @{ role = 'front'; cluster = 'GVA-PRD-CL02'; datastore = 'vsan-gva-prd-cl02'; network = 'dvpg-web-prd-vl210' }
$approved = New-RvavRequest rhel9-server -ProjectId prj_web -Environment prd -Inputs $prd -Justification 'Fourth front node.'
Assert ($approved.status -eq 'pending_approval') "the production request is $($approved.status)"
Connect-Approver | Out-Null
Wait-Approval $approved.number | Approve-RvavApproval -Comment 'OK for the release.' | Out-Null
$approved = Wait-RvavRequest $approved.id
Assert ($approved.status -eq 'succeeded') "the approved request is $($approved.status)"

Connect-Rvav | Out-Null
$rejected = New-RvavRequest rhel9-server -ProjectId prj_web -Environment prd -Inputs $prd -Justification 'Fifth front node.'
Connect-Approver | Out-Null
$a = Wait-Approval $rejected.number
Deny-RvavApproval $a.id -Comment 'Four are enough.' | Out-Null
$rejected = Wait-RvavRequest $rejected.id -ErrorAction SilentlyContinue -ErrorVariable failed
Assert ($rejected.status -eq 'rejected' -and "$failed" -match 'rejected') "the rejected request is $($rejected.status): $failed"

Connect-Rvav | Out-Null
$gone = Remove-RvavDeployment $d.id -Force -Wait
Assert ($gone.status -eq 'succeeded') "the delete is $($gone.status)"
Assert ((Get-RvavDeployment $d.id).status -eq 'deleted') 'the deployment is deleted'

Connect-Approver | Out-Null
$lab = @(Get-RvavIPRange -Name la*)
Assert ($lab.Count -eq 1 -and $lab[0].name -eq 'Lab' -and $lab[0].free -eq 8) "the IP ranges: $($lab | ConvertTo-Json -Compress)"
Assert ($lab[0].PSObject.TypeNames[0] -eq 'Rvav.IPRange') 'an IP range is typed'
Assert (@(Get-RvavIPRange -Network dvpg-app-dev-vl310).Count -eq 0) 'another network has no range'
$held = @($lab | Get-RvavIPAllocation)
Assert ($held.Count -eq 1 -and $held[0].hostname -eq 'lab-db01' -and $held[0].RangeId -eq $lab[0].id) "the range's addresses: $($held | ConvertTo-Json -Compress)"
Assert (@(Get-RvavIPAllocation $lab[0].id -Address 10.32.20.99).Count -eq 0) 'no VM holds 10.32.20.99'
Assert ((Get-RvavIPRange $lab[0].id).name -eq 'Lab') 'a range by its ID'
'PowerShell script done'
