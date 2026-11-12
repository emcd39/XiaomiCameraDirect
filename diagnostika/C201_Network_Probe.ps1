# Xiaomi C201 read-only LAN baseline for Windows PowerShell 5.1+ / PowerShell 7+
# No login, firmware updates, pairing, brute-force, or config changes.
param(
    [Parameter(Mandatory=$true)]
    [ValidatePattern('^\d{1,3}(\.\d{1,3}){3}$')]
    [string]$CameraIP,
    [string]$OutputDirectory = "$HOME\Desktop\C201-Network-Report"
)
$ErrorActionPreference = 'Continue'
New-Item -Path $OutputDirectory -ItemType Directory -Force | Out-Null
$stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
$outFile = Join-Path $OutputDirectory "C201-network-$stamp.txt"
$logs = New-Object System.Collections.Generic.List[string]
function Add-Log([string]$s) { $script:logs.Add($s); Write-Host $s }
Add-Log "Xiaomi C201 network baseline $(Get-Date -Format o)"
Add-Log "Target IP: $CameraIP"
Add-Log '--- Local network adapters ---'
try { Get-NetIPConfiguration | Out-String | ForEach-Object { Add-Log $_ } }
catch { Add-Log "Get-NetIPConfiguration failed: $_" }
Add-Log '--- ICMP reachability (non-response is not proof of outage) ---'
try { Test-Connection -ComputerName $CameraIP -Count 2 -ErrorAction Stop | Out-String | ForEach-Object { Add-Log $_ } }
catch { Add-Log "Ping failed/filtered: $_" }
Add-Log '--- ARP/neighbour ---'
try { Get-NetNeighbor -IPAddress $CameraIP -ErrorAction Stop | Select-Object IPAddress,LinkLayerAddress,State,InterfaceAlias | Format-Table | Out-String | ForEach-Object { Add-Log $_ } }
catch { Add-Log "Neighbour lookup unavailable: $_" }
Add-Log '--- Targeted TCP connect checks (closed ports do not rule out P2P) ---'
$ports = @(80,443,554,8554,8080,8000,54321)
foreach ($port in $ports) {
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $iar = $client.BeginConnect($CameraIP, $port, $null, $null)
        $ok = $iar.AsyncWaitHandle.WaitOne(1000)
        if ($ok) {
            try { $client.EndConnect($iar); Add-Log "TCP $port : OPEN" }
            catch { Add-Log "TCP $port : CLOSED/ERROR ($($_.Exception.Message))" }
        } else { Add-Log "TCP $port : TIMEOUT/FILTERED" }
        $iar.AsyncWaitHandle.Close()
    } catch { Add-Log "TCP $port : ERROR ($($_.Exception.Message))" }
    finally { $client.Close() }
}
Add-Log '--- Next: run XiaomiViewer -> Help -> Camera diagnostics with Mi Cloud connectivity ---'
Add-Log 'NOTE: Do not publish this file without reviewing local IPs/MACs.'
$logs | Set-Content -Path $outFile -Encoding UTF8
Write-Host "Saved: $outFile"
