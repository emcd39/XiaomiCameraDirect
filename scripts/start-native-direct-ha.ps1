param(
    [switch]$MediaOnly,
    [string]$TestRoot = (Join-Path $PSScriptRoot '../.test-env/ha-native-win-py314')
)

$ErrorActionPreference = 'Stop'
$testDir = (Resolve-Path -LiteralPath $TestRoot).Path
$mediaDir = Join-Path $testDir 'media-tools'
$mediaUrl = 'http://127.0.0.1:56266/api/streams'
$haUrl = 'http://127.0.0.1:56264/api/'

function Get-LocalHttpStatus([string]$Url) {
    curl.exe --silent --max-time 2 --output NUL --write-out '%{http_code}' $Url
}

if ((Get-LocalHttpStatus $mediaUrl) -ne '200') {
    $configPath = Join-Path $mediaDir 'go2rtc.yaml'
    $mediaExe = Join-Path $mediaDir 'go2rtc.exe'
    if (!(Test-Path -LiteralPath $configPath) -or !(Test-Path -LiteralPath $mediaExe)) {
        throw "Missing local go2rtc executable or configuration in $mediaDir"
    }
    $process = Start-Process -FilePath $mediaExe -ArgumentList @('-config', ('"' + $configPath + '"')) `
        -WorkingDirectory $mediaDir -WindowStyle Hidden -PassThru `
        -RedirectStandardOutput (Join-Path $mediaDir 'go2rtc-direct.log') `
        -RedirectStandardError (Join-Path $mediaDir 'go2rtc-direct.err')
    for ($attempt = 0; $attempt -lt 15; $attempt++) {
        if ((Get-LocalHttpStatus $mediaUrl) -eq '200') { break }
        $process.Refresh()
        if ($process.HasExited) { throw "go2rtc exited with code $($process.ExitCode). See go2rtc-direct.err." }
        Start-Sleep -Milliseconds 400
    }
}
if ((Get-LocalHttpStatus $mediaUrl) -ne '200') {
    throw "go2rtc did not answer on $mediaUrl"
}
"DIRECT_MEDIA=200"
if ($MediaOnly) { return }

$haStatus = Get-LocalHttpStatus $haUrl
if ($haStatus -in @('401', '200')) {
    "DIRECT_HA_API=$haStatus"
} elseif ($haStatus -eq '000') {
    & (Join-Path $testDir 'start-native-ha.ps1')
} else {
    throw "Unexpected HTTP $haStatus from $haUrl"
}
