#!/bin/sh
set -eu

cd "$(dirname "$0")/.."
go test ./internal/httpapi ./internal/xiaomi ./internal/streamrtsp -count=1 \
    -run '^Test(Managed(CameraList(CanSkipAllStreamsWithoutStartingSources|StartsOnlyRequestedCameraStream|UnknownStreamFilterDoesNotStartAnySource)|ReadyStreamIsAuthenticatedAndOnlyOpensRequestedCamera)|CloneManagedCamerasCopiesNestedMetadata|ProviderInventoryCancelsCloudRefreshWithCaller|RTSPLateClientReceivesReplayBeforeLivePackets)$'

cd xiaomi-diagnostic-console
go test ./internal/console -run '^Test(InventoryNotPolled|SelectedStreamStartsOnce|RepeatedEOF|ReadyRTSPMetadata|SelectedCameraStartRejects)' -count=1
node scripts/Test-Preview-UI.js >/dev/null
echo 'PASS console UI'
