$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot
$wslc = (Get-Command wslc.exe).Source
$image = 'xlg-camera-quick:local'

& $wslc build --target camera-quick --tag $image $root
if ($LASTEXITCODE) { exit $LASTEXITCODE }

& $wslc run --rm --network none $image
exit $LASTEXITCODE
