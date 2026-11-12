[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)]
    [ValidateSet('Add', 'Remove', 'Status')]
    [string]$Action
)

$ErrorActionPreference = 'Stop'
$RuleName = 'XLG-C201-CS2-WSL-Test'
$DisplayName = 'Xiaomi C201 CS2 test UDP to WSL'
$WslCreatorId = '{40E0AC32-46A5-438A-A0B2-2B479E8F2E90}'
$CameraAddress = '192.168.2.124'

$identity = [System.Security.Principal.WindowsIdentity]::GetCurrent()
$principal = [System.Security.Principal.WindowsPrincipal]::new($identity)
if (-not $principal.IsInRole([System.Security.Principal.WindowsBuiltInRole]::Administrator)) {
    throw 'Run this helper from an elevated PowerShell window.'
}

switch ($Action) {
    'Status' {
        $rule = Get-NetFirewallHyperVRule -VMCreatorId $WslCreatorId -ErrorAction Stop |
            Where-Object Name -eq $RuleName
        if ($rule) {
            'PILOT_HYPERV_UDP_RULE=present'
            $rule | Select-Object Name, Direction, Action, Enabled, Protocol, RemoteAddresses, VMCreatorId |
                ConvertTo-Json -Compress
        } else {
            'PILOT_HYPERV_UDP_RULE=absent'
        }
    }
    'Add' {
        $existing = Get-NetFirewallHyperVRule -VMCreatorId $WslCreatorId -ErrorAction Stop |
            Where-Object Name -eq $RuleName
        if ($existing) {
            throw 'The named pilot rule already exists; inspect it with -Action Status or remove it first.'
        }
        New-NetFirewallHyperVRule `
            -Name $RuleName `
            -DisplayName $DisplayName `
            -Direction Inbound `
            -VMCreatorId $WslCreatorId `
            -Protocol UDP `
            -RemoteAddresses $CameraAddress `
            -Action Allow | Out-Null
        'PILOT_HYPERV_UDP_RULE=added'
        'SCOPE=WSL_INBOUND_UDP_FROM_C201_ONLY'
    }
    'Remove' {
        $existing = Get-NetFirewallHyperVRule -VMCreatorId $WslCreatorId -ErrorAction Stop |
            Where-Object Name -eq $RuleName
        if ($existing) {
            Remove-NetFirewallHyperVRule -Name $RuleName -Confirm:$false
            'PILOT_HYPERV_UDP_RULE=removed'
        } else {
            'PILOT_HYPERV_UDP_RULE=absent'
        }
    }
}
