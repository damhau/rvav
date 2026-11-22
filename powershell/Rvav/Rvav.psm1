#Requires -Version 7.2

# rvav's PowerShell module (ADR-0053): the catalog, requests, deployments
# and their day-2 actions, and approvals, through rvav's API with an API
# token.

$script:Connection = $null
$script:FinalStatus = 'succeeded', 'failed', 'rejected', 'cancelled'

function Connect-Rvav {
    <#
    .SYNOPSIS
    Connects to rvav with an API token.
    .DESCRIPTION
    Connects the module's cmdlets to an rvav as the user an API token belongs to. Without -Server or -Token, it takes
    RVAV_URL and RVAV_TOKEN from the environment, as rvavctl does. Make an API token in rvav's web app, under your
    profile's API tokens. An http:// address of another machine is refused, as the token would cross the network
    unencrypted, unless -AllowUnencryptedAuthentication.
    .EXAMPLE
    Connect-Rvav -Server https://rvav.corp.example -Token (Read-Host -AsSecureString -Prompt 'API token')
    .EXAMPLE
    $env:RVAV_URL = 'https://rvav.corp.example'; $env:RVAV_TOKEN = $token; Connect-Rvav
    #>
    [CmdletBinding()]
    [OutputType('Rvav.Connection')]
    [Diagnostics.CodeAnalysis.SuppressMessageAttribute('PSAvoidUsingConvertToSecureStringWithPlainText', '',
        Justification = 'RVAV_TOKEN is plain text already, as rvavctl reads it.')]
    param(
        # rvav's address, as the web app's: https://rvav.corp.example. RVAV_URL when not given.
        [string] $Server = $env:RVAV_URL,
        # An API token. RVAV_TOKEN when not given.
        [securestring] $Token,
        # Sends the token to an http:// address of another machine, unencrypted: for a test rvav only.
        [switch] $AllowUnencryptedAuthentication
    )
    try {
        if (-not $Server) {
            throw 'Give -Server, rvav''s address, or set RVAV_URL.'
        }
        $address = $null
        if (-not [uri]::TryCreate($Server, [UriKind]::Absolute, [ref] $address) -or $address.Scheme -notin 'http', 'https') {
            throw "$Server isn't an rvav address: give it as https://rvav.corp.example."
        }
        if ($address.Scheme -eq 'http' -and -not $address.IsLoopback -and -not $AllowUnencryptedAuthentication) {
            throw "$Server would get your token unencrypted: use its https:// address, or add -AllowUnencryptedAuthentication."
        }
        if (-not $Token) {
            if (-not $env:RVAV_TOKEN) {
                throw 'Give -Token, an API token made in rvav, or set RVAV_TOKEN.'
            }
            $Token = ConvertTo-SecureString -String $env:RVAV_TOKEN -AsPlainText -Force
        }
        $previous = $script:Connection
        $script:Connection = [pscustomobject]@{
            Server     = ($Server.TrimEnd('/') -replace '/api/v1$', '')
            Token      = $Token
            UserId     = $null
            User       = $null
        }
        try {
            $me = Invoke-RvavApi -Path me
        } catch {
            $script:Connection = $previous
            throw
        }
        $script:Connection.UserId = $me.user.id
        $script:Connection.User = $me.user.name
        [pscustomobject]@{
            PSTypeName = 'Rvav.Connection'
            Server     = $script:Connection.Server
            UserId     = $me.user.id
            User       = $me.user.name
        }
    } catch {
        $PSCmdlet.ThrowTerminatingError($_)
    }
}

function Disconnect-Rvav {
    <#
    .SYNOPSIS
    Forgets the connection Connect-Rvav made.
    #>
    [CmdletBinding()]
    param()
    $script:Connection = $null
}

function Invoke-RvavApi {
    <#
    .SYNOPSIS
    Calls rvav's API, for what the other cmdlets don't do.
    .DESCRIPTION
    Sends a request to rvav's API (/api/v1) as the connected user and returns its answer. A call rvav refuses is an
    error with rvav's reasons, as is a call that gets no answer within -TimeoutSec. The API's description is at
    /api/v1/openapi.json on your rvav; the release's api-reference.html is its readable reference.
    .EXAMPLE
    Invoke-RvavApi -Path admin/approval-policies
    .EXAMPLE
    Invoke-RvavApi -Method POST -Path requests/REQ-10042/cancel
    #>
    [CmdletBinding()]
    param(
        [ValidateSet('GET', 'POST', 'PUT', 'PATCH', 'DELETE')]
        [string] $Method = 'GET',
        # The path after /api/v1/: deployments/dep_01J....
        [Parameter(Mandatory, Position = 0)]
        [string] $Path,
        # Query parameters; a list gives a parameter several times.
        [hashtable] $Query,
        # The request's body, sent as JSON.
        [object] $Body,
        # Sends an Idempotency-Key, as calls that make requests need.
        [switch] $Idempotent,
        # How long rvav may take to answer, in seconds, as rvavctl waits.
        [ValidateRange(1, [int]::MaxValue)]
        [int] $TimeoutSec = 120
    )
    try {
        $c = $script:Connection
        if (-not $c) {
            throw 'Not connected to rvav: run Connect-Rvav first.'
        }
        $uri = $c.Server + '/api/v1/' + $Path.TrimStart('/')
        if ($Query) {
            $pairs = foreach ($key in $Query.Keys | Sort-Object) {
                foreach ($value in @($Query[$key])) {
                    if ($null -ne $value -and "$value" -ne '') {
                        [uri]::EscapeDataString($key) + '=' + [uri]::EscapeDataString([string]$value)
                    }
                }
            }
            if ($pairs) {
                $uri += '?' + ($pairs -join '&')
            }
        }
        $headers = @{
            Authorization = 'Bearer ' + (ConvertFrom-SecureString -SecureString $c.Token -AsPlainText)
            Accept        = 'application/json'
        }
        if ($Idempotent) {
            $headers['Idempotency-Key'] = [guid]::NewGuid().ToString()
        }
        $call = @{
            Method             = $Method
            Uri                = $uri
            Headers            = $headers
            UserAgent          = 'rvav-powershell/' + $MyInvocation.MyCommand.Module.Version
            SkipHttpErrorCheck = $true
            StatusCodeVariable = 'status'
            TimeoutSec         = $TimeoutSec
        }
        if ($PSBoundParameters.ContainsKey('Body')) {
            $call.Body = [System.Text.Encoding]::UTF8.GetBytes((ConvertTo-Json -InputObject $Body -Depth 32 -Compress))
            $call.ContentType = 'application/json'
        }
        try {
            $answer = Invoke-RestMethod @call
        } catch [System.Net.Http.HttpRequestException] {
            throw "Can't reach rvav at $($c.Server): $($_.Exception.Message)"
        } catch [System.Threading.Tasks.TaskCanceledException] {
            throw "rvav at $($c.Server) didn't answer within $TimeoutSec seconds."
        }
        if ($status -ge 400) {
            throw (Format-Problem -Answer $answer -Status $status)
        }
        $answer
    } catch {
        $PSCmdlet.ThrowTerminatingError($_)
    }
}

function Format-Problem {
    param($Answer, [int] $Status)
    if ($Answer -isnot [pscustomobject] -or -not $Answer.PSObject.Properties['title']) {
        return "rvav answered $Status"
    }
    $msg = $Answer.title
    if ($Answer.PSObject.Properties['detail'] -and $Answer.detail -and $Answer.detail -ne $Answer.title) {
        $msg += ': ' + $Answer.detail
    }
    if ($Answer.PSObject.Properties['errors']) {
        $msg += Format-FieldError $Answer.errors
    }
    $msg
}

function Format-FieldError($Errors) {
    -join @(foreach ($e in $Errors) {
            $field = $e.field -replace '^/inputs/', '' -replace '^/', ''
            "`n  " + $(if ($field) { $field + ': ' }) + $e.message
        })
}

function Format-Preview($Preview) {
    $parts = @()
    if ($Preview.names) {
        $parts += 'VMs ' + (@($Preview.names) -join ', ')
    }
    if ($Preview.approval -and $Preview.approval.required) {
        $parts += 'approval needed: ' + (@($Preview.approval.policies | ForEach-Object reason) -join '; ')
    }
    if ($parts) {
        ' (' + ($parts -join '; ') + ')'
    }
}

function Get-Page {
    param([string] $Path, [hashtable] $Query, [int] $First)
    $q = @{} + $Query
    $n = 0
    do {
        $q.limit = [math]::Min(200, $First - $n)
        $page = Invoke-RvavApi -Path $Path -Query $q
        foreach ($item in $page.items) {
            $item
            if (++$n -ge $First) {
                return
            }
        }
        $q.cursor = if ($page.PSObject.Properties['next_cursor']) { $page.next_cursor }
    } while ($q.cursor)
}

function Add-TypeName {
    param([Parameter(ValueFromPipeline)] $InputObject, [string] $Name, [hashtable] $Property)
    process {
        if ($null -eq $InputObject) {
            return
        }
        $InputObject.PSObject.TypeNames.Insert(0, 'Rvav.' + $Name)
        if ($Property) {
            foreach ($key in $Property.Keys) {
                $InputObject | Add-Member -NotePropertyName $key -NotePropertyValue $Property[$key] -Force
            }
        }
        $InputObject
    }
}

function Get-PathSegment([string] $Value) {
    [uri]::EscapeDataString($Value)
}

function Get-RvavCatalogItem {
    <#
    .SYNOPSIS
    Gets the catalog items a project may request.
    .EXAMPLE
    Get-RvavCatalogItem -ProjectId prj_web
    .EXAMPLE
    Get-RvavCatalogItem -ProjectId prj_web -Item rhel9-server | Select-Object -ExpandProperty versions
    #>
    [CmdletBinding(DefaultParameterSetName = 'List')]
    [OutputType('Rvav.CatalogItem')]
    param(
        # The project whose catalog it is.
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [string] $ProjectId,
        # An item, by its slug or ID.
        [Parameter(Mandatory, Position = 0, ParameterSetName = 'One')]
        [Alias('Slug')]
        [string] $Item,
        # Words the items' names, summaries or tags have.
        [Parameter(ParameterSetName = 'List')]
        [string] $Search,
        [Parameter(ParameterSetName = 'List')]
        [string] $Category,
        [Parameter(ParameterSetName = 'List')]
        [ValidateRange(1, [int]::MaxValue)]
        [int] $First = 200
    )
    process {
        try {
            $items = if ($PSCmdlet.ParameterSetName -eq 'One') {
                Invoke-RvavApi -Path ('catalog/items/' + (Get-PathSegment $Item)) -Query @{ project_id = $ProjectId }
            } else {
                Get-Page -Path catalog/items -Query @{ project_id = $ProjectId; q = $Search; category = $Category } -First $First
            }
            $items | Add-TypeName -Name CatalogItem -Property @{ ProjectId = $ProjectId }
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function New-RvavRequest {
    <#
    .SYNOPSIS
    Requests a catalog item.
    .DESCRIPTION
    Requests an item in a project's environment with the inputs given: rvav fills in the others from the item's form,
    as the web app starts it. With -Wait, it waits for the request to end, approvals included. With -WhatIf, rvav
    checks the request without making it: what rvav would refuse is an error, and its policies' warnings are
    warnings.
    .EXAMPLE
    New-RvavRequest -CatalogItem rhel9-server -ProjectId prj_web -Environment dev -Inputs @{ size = 'M'; cluster = 'GVA-DEV-CL01' } -Wait
    .EXAMPLE
    Get-RvavCatalogItem -ProjectId prj_web -Item rhel9-server | New-RvavRequest -Environment prd -Justification 'CHG-1234'
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType('Rvav.Request')]
    param(
        # The item, by its slug or ID.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [Alias('Slug')]
        [string] $CatalogItem,
        [Parameter(Mandatory, ValueFromPipelineByPropertyName)]
        [string] $ProjectId,
        # The environment's key: dev.
        [Parameter(Mandatory)]
        [string] $Environment,
        # The form's inputs, by field name. Those left out take the form's initial values and defaults; $null empties one.
        [hashtable] $Inputs = @{},
        # The item's version, 1.2: the newest the project may request when not given.
        [string] $Version,
        # The deployment's name: rvav names it when not given.
        [string] $Name,
        [string] $Description,
        # Why, for the approvers.
        [string] $Justification,
        # Goes ahead despite the warnings of soft policies.
        [switch] $AcceptWarnings,
        # Waits for the request to end.
        [switch] $Wait,
        # With -Wait, gives up after this long.
        [timespan] $Timeout = [timespan]::Zero
    )
    process {
        try {
            $it = Invoke-RvavApi -Path ('catalog/items/' + (Get-PathSegment $CatalogItem)) -Query @{ project_id = $ProjectId }
            $versionId = $it.version.id
            if ($Version -and $Version -ne $it.version.number) {
                $offered = @($it.versions | Where-Object number -EQ $Version)
                if (-not $offered) {
                    $numbers = @($it.version.number) + @($it.versions | ForEach-Object number) | Select-Object -Unique
                    throw "$($it.name) has no version $Version the project may request: it offers $($numbers -join ', ')."
                }
                $versionId = $offered[0].id
            }
            $draft = [ordered]@{
                catalog_item_id = $it.id
                version_id      = $versionId
                project_id      = $ProjectId
                environment     = $Environment
                inputs          = $Inputs
            }
            foreach ($p in @{ Name = 'deployment_name'; Description = 'deployment_description'; Justification = 'justification' }.GetEnumerator()) {
                if ($PSBoundParameters.ContainsKey($p.Key)) {
                    $draft[$p.Value] = $PSBoundParameters[$p.Key]
                }
            }
            if ($AcceptWarnings) {
                $draft.accept_warnings = $true
            }
            $target = "$($it.name) in $ProjectId, $Environment"
            $preview = $null
            if ($WhatIfPreference) {
                $preview = Invoke-RvavApi -Method POST -Path requests/preview -Query @{ validate = 'true' } -Body $draft
                $target += Format-Preview $preview
            }
            if (-not $PSCmdlet.ShouldProcess($target, 'Request')) {
                if ($preview -and -not $preview.valid) {
                    Write-Error -Message ('rvav would refuse the request:' + (Format-FieldError $preview.errors)) `
                        -TargetObject $preview -Category InvalidData -ErrorId RequestInvalid
                }
                foreach ($w in @($preview.warnings | Where-Object { $_ })) {
                    Write-Warning $w.message
                }
                return
            }
            $r = Invoke-RvavApi -Method POST -Path requests -Body $draft -Idempotent
            if ($Wait) {
                Wait-RvavRequest -Id $r.id -Timeout $Timeout
            } else {
                $r | Add-TypeName -Name Request
            }
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Get-RvavRequest {
    <#
    .SYNOPSIS
    Gets requests: one by its ID or number, or those you may see.
    .EXAMPLE
    Get-RvavRequest REQ-10042
    .EXAMPLE
    Get-RvavRequest -Mine -Status running, pending_approval
    #>
    [CmdletBinding(DefaultParameterSetName = 'List')]
    [OutputType('Rvav.Request')]
    param(
        # The request's ID or number.
        [Parameter(Mandatory, Position = 0, ParameterSetName = 'One', ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [string[]] $Id,
        # Only yours.
        [Parameter(ParameterSetName = 'List')]
        [switch] $Mine,
        [Parameter(ParameterSetName = 'List')]
        [string] $ProjectId,
        # pending_approval, scheduled, queued, running, succeeded, failed, rejected, cancelling or cancelled.
        [Parameter(ParameterSetName = 'List')]
        [string[]] $Status,
        [Parameter(ParameterSetName = 'List')]
        [string] $Search,
        [Parameter(ParameterSetName = 'List')]
        [ValidateRange(1, [int]::MaxValue)]
        [int] $First = 50
    )
    process {
        try {
            $requests = if ($PSCmdlet.ParameterSetName -eq 'One') {
                foreach ($i in $Id) {
                    Invoke-RvavApi -Path ('requests/' + (Get-PathSegment $i))
                }
            } else {
                Get-Page -Path requests -First $First -Query @{
                    requester = $(if ($Mine) { 'me' }); project_id = $ProjectId; status = $Status; q = $Search
                }
            }
            $requests | Add-TypeName -Name Request
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Wait-RvavRequest {
    <#
    .SYNOPSIS
    Waits for a request to end.
    .DESCRIPTION
    Reads the request until it succeeds, fails, is rejected or is cancelled, and returns it. A request that doesn't
    succeed is also an error, with why: add -ErrorAction Stop to stop a script there. When -Timeout ends the wait,
    it returns the request as it is, and writes an error: rvav goes on with the request.
    .EXAMPLE
    New-RvavRequest ... | Wait-RvavRequest -Timeout (New-TimeSpan -Minutes 30) -ErrorAction Stop
    #>
    [CmdletBinding()]
    [OutputType('Rvav.Request')]
    param(
        # The request's ID or number.
        [Parameter(Mandatory, Position = 0, ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [string] $Id,
        # Gives up after this long: never when not given.
        [timespan] $Timeout = [timespan]::Zero,
        # How long between two reads of the request.
        [timespan] $Interval = (New-TimeSpan -Seconds 2)
    )
    process {
        try {
            $deadline = if ($Timeout -gt [timespan]::Zero) { [datetime]::UtcNow + $Timeout }
            $said = ''
            while ($true) {
                $r = Invoke-RvavApi -Path ('requests/' + (Get-PathSegment $Id)) | Add-TypeName -Name Request
                $now = $r.status
                if ($r.current_step) {
                    $now += ': ' + $r.current_step.title
                }
                if ($now -ne $said) {
                    Write-Verbose "$($r.number) $now"
                    $said = $now
                }
                if ($r.status -in $script:FinalStatus) {
                    Write-Progress -Activity "$($r.number) $($r.title)" -Completed
                    $r
                    if ($r.status -ne 'succeeded') {
                        $why = "$($r.number) $($r.status)"
                        if ($r.error) {
                            $why += ': ' + $r.error.message
                        }
                        Write-Error -Message $why -TargetObject $r -Category OperationStopped -ErrorId RequestDidNotSucceed
                    }
                    return
                }
                Write-Progress -Activity "$($r.number) $($r.title)" -Status $now -PercentComplete $(
                    if ($r.progress.total) { [math]::Min(100, 100 * $r.progress.done / $r.progress.total) } else { 0 })
                if ($deadline -and [datetime]::UtcNow -gt $deadline) {
                    Write-Progress -Activity "$($r.number) $($r.title)" -Completed
                    $r
                    Write-Error -Message "$($r.number) hasn't finished after $Timeout" -TargetObject $r -Category OperationTimeout -ErrorId RequestTimedOut
                    return
                }
                Start-Sleep -Milliseconds ([int]$Interval.TotalMilliseconds)
            }
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Get-RvavDeployment {
    <#
    .SYNOPSIS
    Gets deployments: one by its ID, or those you may see.
    .EXAMPLE
    Get-RvavDeployment -ProjectId prj_web -Environment prd
    .EXAMPLE
    Get-RvavDeployment -Id (Get-RvavRequest REQ-10042).deployment_ids
    #>
    [CmdletBinding(DefaultParameterSetName = 'List')]
    [OutputType('Rvav.Deployment')]
    param(
        [Parameter(Mandatory, Position = 0, ParameterSetName = 'One', ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [Alias('DeploymentId')]
        [string[]] $Id,
        # Only yours.
        [Parameter(ParameterSetName = 'List')]
        [switch] $Mine,
        [Parameter(ParameterSetName = 'List')]
        [string] $ProjectId,
        [Parameter(ParameterSetName = 'List')]
        [string] $Environment,
        # provisioning, ready, updating, failed, deleting or deleted.
        [Parameter(ParameterSetName = 'List')]
        [string[]] $Status,
        [Parameter(ParameterSetName = 'List')]
        [string] $Search,
        [Parameter(ParameterSetName = 'List')]
        [ValidateRange(1, [int]::MaxValue)]
        [int] $First = 50
    )
    process {
        try {
            $deployments = if ($PSCmdlet.ParameterSetName -eq 'One') {
                foreach ($i in $Id) {
                    Invoke-RvavApi -Path ('deployments/' + (Get-PathSegment $i))
                }
            } else {
                Get-Page -Path deployments -First $First -Query @{
                    owner = $(if ($Mine) { 'me' }); project_id = $ProjectId; environment = $Environment; status = $Status; q = $Search
                }
            }
            $deployments | Add-TypeName -Name Deployment
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Get-RvavDeploymentAction {
    <#
    .SYNOPSIS
    Gets the day-2 actions of a deployment and its resources, and whether you may run each now.
    .EXAMPLE
    Get-RvavDeployment dep_01J... | Get-RvavDeploymentAction
    .EXAMPLE
    (Get-RvavDeploymentAction dep_01J... resize -Resource web-dev-app001).form.schema
    #>
    [CmdletBinding()]
    [OutputType('Rvav.Day2Action')]
    param(
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [Alias('Id')]
        [string] $DeploymentId,
        # Only this action, by its key: power_off.
        [Parameter(Position = 1)]
        [string] $Action,
        # Only those on this resource, by its name or ID.
        [string] $Resource
    )
    process {
        try {
            $all = (Invoke-RvavApi -Path ('deployments/' + (Get-PathSegment $DeploymentId) + '/actions')).items
            $all | Where-Object {
                (-not $Action -or $_.key -eq $Action) -and
                (-not $Resource -or $_.resource_id -eq $Resource -or $_.resource_name -eq $Resource)
            } | Add-TypeName -Name Day2Action -Property @{ DeploymentId = $DeploymentId }
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Invoke-RvavDeploymentAction {
    <#
    .SYNOPSIS
    Runs a day-2 action on a deployment or one of its resources.
    .DESCRIPTION
    Runs an action by its key (see Get-RvavDeploymentAction). An action on a VM needs -Resource when the deployment
    has several. Inputs left out take the action's form's values on its target. An action that deletes or can't be
    undone asks first, unless -Force is given.
    .EXAMPLE
    Invoke-RvavDeploymentAction dep_01J... power_off -Resource web-dev-app001 -Wait
    .EXAMPLE
    Invoke-RvavDeploymentAction dep_01J... resize -Inputs @{ cpu_count = 4 } -Justification 'CHG-1234'
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType('Rvav.Request')]
    param(
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [Alias('Id')]
        [string] $DeploymentId,
        # The action's key: power_off.
        [Parameter(Mandatory, Position = 1, ValueFromPipelineByPropertyName)]
        [Alias('Key')]
        [string] $Action,
        # The resource it acts on, by its name or ID.
        [Parameter(ValueFromPipelineByPropertyName)]
        [Alias('resource_id')]
        [string] $Resource,
        # The action's inputs, by field name.
        [hashtable] $Inputs = @{},
        [string] $Justification,
        [switch] $AcceptWarnings,
        [switch] $Wait,
        [timespan] $Timeout = [timespan]::Zero,
        # Runs an action that deletes or can't be undone without asking.
        [switch] $Force
    )
    process {
        try {
            $matching = @(Get-RvavDeploymentAction -DeploymentId $DeploymentId -Action $Action -Resource $Resource)
            if ($matching.Count -eq 0) {
                throw "No action $Action$(if ($Resource) { " on $Resource" }) on $DeploymentId`: see Get-RvavDeploymentAction."
            }
            if ($matching.Count -gt 1) {
                throw "$Action applies to several resources: give -Resource, one of $($matching.resource_name -join ', ')."
            }
            $a = $matching[0]
            if (-not $a.enabled) {
                throw "$($a.name) isn't available: $($a.disabled_reason)"
            }
            $target = if ($a.resource_name) { $a.resource_name } else { $DeploymentId }
            if (-not $PSCmdlet.ShouldProcess($target, $a.name)) {
                return
            }
            if (($a.destructive -or $a.confirm) -and -not $Force) {
                $why = if ($a.confirm) { $a.confirm.message } else { "$($a.name) deletes or can't be undone." }
                if (-not $PSCmdlet.ShouldContinue($why, "$($a.name) $target")) {
                    return
                }
            }
            $draft = [ordered]@{ action = $a.key; inputs = $Inputs }
            if ($a.resource_id) {
                $draft.resource_id = $a.resource_id
            }
            if ($Justification) {
                $draft.justification = $Justification
            }
            if ($AcceptWarnings) {
                $draft.accept_warnings = $true
            }
            $r = Invoke-RvavApi -Method POST -Path ('deployments/' + (Get-PathSegment $DeploymentId) + '/actions') -Body $draft -Idempotent
            if ($Wait) {
                Wait-RvavRequest -Id $r.id -Timeout $Timeout
            } else {
                $r | Add-TypeName -Name Request
            }
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Remove-RvavDeployment {
    <#
    .SYNOPSIS
    Deletes a deployment: its VMs, IP addresses and DNS records.
    .DESCRIPTION
    Runs the deployment's Delete action. It asks first, unless -Force or -Confirm:$false is given.
    .EXAMPLE
    Remove-RvavDeployment dep_01J... -Wait
    .EXAMPLE
    Get-RvavDeployment -ProjectId prj_web -Search old- | Remove-RvavDeployment -Force
    #>
    [CmdletBinding(SupportsShouldProcess, ConfirmImpact = 'High')]
    [OutputType('Rvav.Request')]
    param(
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [Alias('DeploymentId')]
        [string] $Id,
        [string] $Justification,
        [switch] $Wait,
        [timespan] $Timeout = [timespan]::Zero,
        # Deletes without asking.
        [switch] $Force
    )
    process {
        try {
            if ($Force -and -not $PSBoundParameters.ContainsKey('Confirm')) {
                $ConfirmPreference = 'None'
            }
            $d = Invoke-RvavApi -Path ('deployments/' + (Get-PathSegment $Id))
            if (-not $PSCmdlet.ShouldProcess("$($d.name) ($Id)", 'Delete the deployment and its VMs')) {
                return
            }
            $draft = [ordered]@{ action = 'delete'; inputs = @{} }
            if ($Justification) {
                $draft.justification = $Justification
            }
            $r = Invoke-RvavApi -Method POST -Path ('deployments/' + (Get-PathSegment $Id) + '/actions') -Body $draft -Idempotent
            if ($Wait) {
                Wait-RvavRequest -Id $r.id -Timeout $Timeout
            } else {
                $r | Add-TypeName -Name Request
            }
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Get-RvavApproval {
    <#
    .SYNOPSIS
    Gets the approvals waiting for you, or one by its ID.
    .EXAMPLE
    Get-RvavApproval
    .EXAMPLE
    Get-RvavApproval -Status approved, rejected
    #>
    [CmdletBinding(DefaultParameterSetName = 'List')]
    [OutputType('Rvav.Approval')]
    param(
        [Parameter(Mandatory, Position = 0, ParameterSetName = 'One', ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [string[]] $Id,
        # waiting, pending, approved, rejected or cancelled: pending when not given.
        [Parameter(ParameterSetName = 'List')]
        [string[]] $Status,
        # Every approval, for administrators and auditors.
        [Parameter(ParameterSetName = 'List')]
        [switch] $All,
        [Parameter(ParameterSetName = 'List')]
        [ValidateRange(1, [int]::MaxValue)]
        [int] $First = 50
    )
    process {
        try {
            $approvals = if ($PSCmdlet.ParameterSetName -eq 'One') {
                foreach ($i in $Id) {
                    Invoke-RvavApi -Path ('approvals/' + (Get-PathSegment $i))
                }
            } else {
                Get-Page -Path approvals -First $First -Query @{ status = $Status; view = $(if ($All) { 'all' }) }
            }
            $approvals | Add-TypeName -Name Approval
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Send-Decision {
    param([string] $Id, [string] $Decision, [string] $Comment, [hashtable] $Inputs)
    $body = [ordered]@{ decision = $Decision }
    if ($Comment) {
        $body.comment = $Comment
    }
    if ($Inputs) {
        $body.inputs = $Inputs
    }
    Invoke-RvavApi -Method POST -Path ('approvals/' + (Get-PathSegment $Id) + '/decision') -Body $body | Add-TypeName -Name Approval
}

function Approve-RvavApproval {
    <#
    .SYNOPSIS
    Approves a request's approval level waiting for you.
    .EXAMPLE
    Get-RvavApproval | Where-Object { $_.request.number -eq 'REQ-10042' } | Approve-RvavApproval -Comment 'OK for the release'
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType('Rvav.Approval')]
    param(
        [Parameter(Mandatory, Position = 0, ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [string] $Id,
        # A comment for the requester.
        [string] $Comment,
        # New values of the inputs approvers may change.
        [hashtable] $Inputs
    )
    process {
        try {
            if ($PSCmdlet.ShouldProcess($Id, 'Approve')) {
                Send-Decision -Id $Id -Decision approve -Comment $Comment -Inputs $Inputs
            }
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Deny-RvavApproval {
    <#
    .SYNOPSIS
    Rejects a request waiting for your approval, saying why.
    .EXAMPLE
    Deny-RvavApproval apr_01J... -Comment 'Use the shared cluster: see CHG-1234.'
    #>
    [CmdletBinding(SupportsShouldProcess)]
    [OutputType('Rvav.Approval')]
    param(
        [Parameter(Mandatory, Position = 0, ValueFromPipeline, ValueFromPipelineByPropertyName)]
        [string] $Id,
        # Why, for the requester.
        [Parameter(Mandatory)]
        [string] $Comment
    )
    process {
        try {
            if ($PSCmdlet.ShouldProcess($Id, 'Reject')) {
                Send-Decision -Id $Id -Decision reject -Comment $Comment
            }
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Get-RvavIPRange {
    <#
    .SYNOPSIS
    Gets rvav's IP ranges, for networks without EfficientIP, with their free addresses.
    .DESCRIPTION
    Needs the right to see the infrastructure for all of rvav. rvav writes no DNS records for addresses from its ranges.
    .EXAMPLE
    Get-RvavIPRange
    .EXAMPLE
    Get-RvavIPRange -Network dvpg-lab-vl510
    .EXAMPLE
    Get-RvavIPRange -Name Lab* | Get-RvavIPAllocation
    #>
    [CmdletBinding(DefaultParameterSetName = 'List')]
    [OutputType('Rvav.IPRange')]
    param(
        [Parameter(Mandatory, Position = 0, ParameterSetName = 'One', ValueFromPipeline)]
        [string[]] $Id,
        # Ranges by name; wildcards work.
        [Parameter(ParameterSetName = 'List')]
        [string] $Name,
        # Only the ranges of this port group or NSX segment.
        [Parameter(ParameterSetName = 'List')]
        [string] $Network
    )
    process {
        try {
            $ranges = if ($PSCmdlet.ParameterSetName -eq 'One') {
                foreach ($i in $Id) {
                    Invoke-RvavApi -Path ('admin/ip-ranges/' + (Get-PathSegment $i))
                }
            } else {
                (Invoke-RvavApi -Path admin/ip-ranges).items | Where-Object {
                    (-not $Name -or $_.name -like $Name) -and (-not $Network -or $_.network -eq $Network)
                }
            }
            $ranges | Add-TypeName -Name IPRange
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

function Get-RvavIPAllocation {
    <#
    .SYNOPSIS
    Gets the addresses an IP range gave, with the VM that holds each.
    .EXAMPLE
    Get-RvavIPRange -Name Lab | Get-RvavIPAllocation
    .EXAMPLE
    Get-RvavIPAllocation -RangeId ipr_01J9 -Address 10.51.0.12
    #>
    [CmdletBinding()]
    [OutputType('Rvav.IPAllocation')]
    param(
        # The range, by ID: a range from Get-RvavIPRange gives it.
        [Parameter(Mandatory, Position = 0, ValueFromPipelineByPropertyName)]
        [Alias('Id')]
        [string] $RangeId,
        # Only this address.
        [string] $Address
    )
    process {
        try {
            (Invoke-RvavApi -Path ('admin/ip-ranges/' + (Get-PathSegment $RangeId) + '/allocations')).items |
                Where-Object { -not $Address -or $_.address -eq $Address } |
                Add-TypeName -Name IPAllocation -Property @{ RangeId = $RangeId }
        } catch {
            $PSCmdlet.ThrowTerminatingError($_)
        }
    }
}

Export-ModuleMember -Function Connect-Rvav, Disconnect-Rvav, Invoke-RvavApi, Get-RvavCatalogItem, New-RvavRequest,
    Get-RvavRequest, Wait-RvavRequest, Get-RvavDeployment, Get-RvavDeploymentAction, Invoke-RvavDeploymentAction,
    Remove-RvavDeployment, Get-RvavApproval, Approve-RvavApproval, Deny-RvavApproval, Get-RvavIPRange, Get-RvavIPAllocation
