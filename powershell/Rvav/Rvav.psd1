@{
    RootModule           = 'Rvav.psm1'
    # make powershell-dist sets a release's version.
    ModuleVersion        = '0.0.0'
    GUID                 = '6f0f3f7e-5a2b-4d8e-9a51-3c2b7d0e4a19'
    Author               = 'rvav'
    Description          = 'Requests, deployments, day-2 actions and approvals of rvav, through its API with an API token.'
    PowerShellVersion    = '7.2'
    CompatiblePSEditions = @('Core')
    FormatsToProcess     = @('Rvav.Format.ps1xml')
    FunctionsToExport    = @(
        'Connect-Rvav', 'Disconnect-Rvav', 'Invoke-RvavApi',
        'Get-RvavCatalogItem', 'New-RvavRequest', 'Get-RvavRequest', 'Wait-RvavRequest',
        'Get-RvavDeployment', 'Get-RvavDeploymentAction', 'Invoke-RvavDeploymentAction', 'Remove-RvavDeployment',
        'Get-RvavApproval', 'Approve-RvavApproval', 'Deny-RvavApproval'
    )
    CmdletsToExport      = @()
    VariablesToExport    = @()
    AliasesToExport      = @()
    PrivateData          = @{
        PSData = @{
            Tags = @('rvav', 'vSphere', 'self-service')
        }
    }
}
