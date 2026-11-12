package localmedia

import (
	"context"
	"errors"
	"net"
	"reflect"
	"testing"
)

const (
	testDID       = "camera-did-a"
	testModel     = "xiaomi.camera.c201"
	testFirmware  = "5.3.2_0016"
	testCameraIP  = "192.168.2.84"
	testRevision  = uint64(41)
	testInterface = "lan0"
)

type fakeInventory struct {
	snapshot InventorySnapshot
	reads    int
	events   *[]string
}

func (i *fakeInventory) CameraInventory(ctx context.Context) (InventorySnapshot, error) {
	if err := ctx.Err(); err != nil {
		return InventorySnapshot{}, err
	}
	i.reads++
	*i.events = append(*i.events, "inventory")
	copy := i.snapshot
	copy.Cameras = append([]Camera(nil), i.snapshot.Cameras...)
	return copy, nil
}

func (i *fakeInventory) replace(revision uint64, camera Camera) {
	i.snapshot = InventorySnapshot{Revision: revision, Cameras: []Camera{camera}}
}

type fakeRoutes struct {
	interfaces []string
	calls      int
	events     *[]string
	afterCheck func(int)
}

func (r *fakeRoutes) Lookup(ctx context.Context, _ net.IP) (Route, error) {
	if err := ctx.Err(); err != nil {
		return Route{}, err
	}
	r.calls++
	*r.events = append(*r.events, "route")
	index := r.calls - 1
	if index >= len(r.interfaces) {
		index = len(r.interfaces) - 1
	}
	selected := r.interfaces[index]
	if r.afterCheck != nil {
		r.afterCheck(r.calls)
	}
	return Route{Interface: selected}, nil
}

type fakeCloud struct {
	dids      []string
	events    *[]string
	afterCall func()
	err       error
}

func (c *fakeCloud) Prepare(ctx context.Context, did string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.dids = append(c.dids, did)
	*c.events = append(*c.events, "cloud")
	if c.afterCall != nil {
		c.afterCall()
	}
	return c.err
}

type fakeSessions struct {
	targets []SessionTarget
	events  *[]string
	err     error
}

type fakeSession struct{}

func (fakeSession) Close() {}

func (s *fakeSessions) Open(ctx context.Context, target SessionTarget) (Session, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.targets = append(s.targets, target)
	*s.events = append(*s.events, "open")
	if s.err != nil {
		return nil, s.err
	}
	return fakeSession{}, nil
}

func newPolicyFixture(ip string, routeInterfaces ...string) (Policy, *fakeInventory, *fakeRoutes, *fakeCloud, *fakeSessions, *[]string, Request) {
	events := []string{}
	device := Camera{DID: testDID, ModelID: testModel, Firmware: testFirmware, IP: ip, MAC: "94:F8:27:00:00:01"}
	inventory := &fakeInventory{snapshot: InventorySnapshot{Revision: testRevision, Cameras: []Camera{device}}, events: &events}
	routes := &fakeRoutes{interfaces: routeInterfaces, events: &events}
	cloud := &fakeCloud{events: &events}
	sessions := &fakeSessions{events: &events}
	policy := Policy{
		AllowedNetworks: []AllowedNetwork{{CIDR: "192.168.2.0/24", Interface: testInterface}},
		Inventory:       inventory, Routes: routes, Cloud: cloud, Sessions: sessions,
	}
	request := Request{
		DID: testDID, ExpectedModelID: testModel, ExpectedFirmware: testFirmware,
		ExpectedIP: ip, ExpectedMAC: "94-f8-27-00-00-01", InventoryRevision: testRevision,
	}
	return policy, inventory, routes, cloud, sessions, &events, request
}

func TestOpenVerifiesInventoryCIDRAndInterfaceBeforeOpeningSession(t *testing.T) {
	policy, inventory, routes, cloud, sessions, events, request := newPolicyFixture(testCameraIP, testInterface, testInterface)
	opened, err := policy.Open(context.Background(), request)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if opened == nil {
		t.Fatal("Open() returned a nil session")
	}
	if inventory.reads != 4 || routes.calls != 2 || !reflect.DeepEqual(cloud.dids, []string{testDID}) || len(sessions.targets) != 1 {
		t.Fatalf("calls inventory/routes/cloud/session = %d/%d/%v/%d", inventory.reads, routes.calls, cloud.dids, len(sessions.targets))
	}
	got := sessions.targets[0]
	if got.DID != testDID || !got.IP.Equal(net.ParseIP(testCameraIP)) || got.Interface != testInterface || got.InventoryRevision != testRevision {
		t.Fatalf("opened target = %#v", got)
	}
	wantEvents := []string{"inventory", "route", "inventory", "cloud", "inventory", "route", "inventory", "open"}
	if !reflect.DeepEqual(*events, wantEvents) {
		t.Fatalf("operation order = %v, want %v", *events, wantEvents)
	}
}

func TestOpenFailsClosedForNetworkAndInterfaceBeforeCloud(t *testing.T) {
	tests := []struct {
		name       string
		ip         string
		route      string
		want       error
		routeCalls int
	}{
		{name: "outside allowlist", ip: "192.168.3.84", route: testInterface, want: ErrTargetOutsideNetwork},
		{name: "different interface", ip: testCameraIP, route: "wlan0", want: ErrRouteInterfaceMismatch, routeCalls: 1},
		{name: "public address even with no matching local route", ip: "203.0.113.84", route: testInterface, want: ErrTargetInvalid},
		{name: "hostname is not an endpoint", ip: "camera.local", route: testInterface, want: ErrTargetInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, _, routes, cloud, sessions, _, request := newPolicyFixture(test.ip, test.route)
			_, err := policy.Open(context.Background(), request)
			if !errors.Is(err, test.want) {
				t.Fatalf("Open() error = %v, want %v", err, test.want)
			}
			if routes.calls != test.routeCalls || len(cloud.dids) != 0 || len(sessions.targets) != 0 {
				t.Fatalf("route/cloud/session calls = %d/%v/%d; preflight rejection must prevent cloud and session work", routes.calls, cloud.dids, len(sessions.targets))
			}
		})
	}
}

func TestOpenRejectsInventoryRevisionOrIPChangeForSameDIDBeforeCloud(t *testing.T) {
	for _, test := range []struct {
		name     string
		revision uint64
		ip       string
	}{
		{name: "revision changed with same IP", revision: testRevision + 1, ip: testCameraIP},
		{name: "IP changed with same DID and revision", revision: testRevision, ip: "192.168.2.85"},
		{name: "IP and revision changed with same DID", revision: testRevision + 1, ip: "192.168.2.85"},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy, inventory, routes, cloud, sessions, _, request := newPolicyFixture(testCameraIP, testInterface, testInterface)
			routes.afterCheck = func(call int) {
				if call == 1 {
					inventory.replace(test.revision, Camera{DID: testDID, ModelID: testModel, Firmware: testFirmware, IP: test.ip, MAC: "94:F8:27:00:00:01"})
				}
			}
			if _, err := policy.Open(context.Background(), request); !errors.Is(err, ErrInventoryStale) {
				t.Fatalf("Open() error = %v, want ErrInventoryStale", err)
			}
			if routes.calls != 1 || len(cloud.dids) != 0 || len(sessions.targets) != 0 {
				t.Fatalf("route/cloud/session calls = %d/%v/%d, want pre-cloud rejection", routes.calls, cloud.dids, len(sessions.targets))
			}
		})
	}
}

func TestOpenRejectsInventoryChangeDuringCloudPreparationBeforeSession(t *testing.T) {
	policy, inventory, routes, cloud, sessions, _, request := newPolicyFixture(testCameraIP, testInterface, testInterface)
	cloud.afterCall = func() {
		inventory.replace(testRevision+1, Camera{DID: testDID, ModelID: testModel, Firmware: testFirmware, IP: "192.168.2.85", MAC: "94:F8:27:00:00:01"})
	}
	if _, err := policy.Open(context.Background(), request); !errors.Is(err, ErrInventoryStale) {
		t.Fatalf("Open() error = %v, want ErrInventoryStale", err)
	}
	if routes.calls != 1 || !reflect.DeepEqual(cloud.dids, []string{testDID}) || len(sessions.targets) != 0 {
		t.Fatalf("route/cloud/session calls = %d/%v/%d, stale target reached session opener", routes.calls, cloud.dids, len(sessions.targets))
	}
}

func TestOpenRejectsRouteInterfaceChangeBeforeSession(t *testing.T) {
	policy, _, routes, cloud, sessions, _, request := newPolicyFixture(testCameraIP, testInterface, "wlan0")
	if _, err := policy.Open(context.Background(), request); !errors.Is(err, ErrRouteInterfaceMismatch) {
		t.Fatalf("Open() error = %v, want ErrRouteInterfaceMismatch", err)
	}
	if routes.calls != 2 || len(cloud.dids) != 1 || len(sessions.targets) != 0 {
		t.Fatalf("route/cloud/session calls = %d/%v/%d, route change reached session opener", routes.calls, cloud.dids, len(sessions.targets))
	}
}

func TestOpenRejectsInventoryChangeDuringSecondRouteCheck(t *testing.T) {
	policy, inventory, routes, cloud, sessions, _, request := newPolicyFixture(testCameraIP, testInterface, testInterface)
	routes.afterCheck = func(call int) {
		if call == 2 {
			inventory.replace(testRevision+1, Camera{DID: testDID, ModelID: testModel, Firmware: testFirmware, IP: "192.168.2.85", MAC: "94:F8:27:00:00:01"})
		}
	}
	if _, err := policy.Open(context.Background(), request); !errors.Is(err, ErrInventoryStale) {
		t.Fatalf("Open() error = %v, want ErrInventoryStale", err)
	}
	if routes.calls != 2 || len(cloud.dids) != 1 || len(sessions.targets) != 0 {
		t.Fatalf("route/cloud/session calls = %d/%v/%d, changed inventory reached session opener", routes.calls, cloud.dids, len(sessions.targets))
	}
}

func TestOpenRejectsModelFirmwareMACAndAmbiguousAllowlist(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*Request)
		camera Camera
		want   error
	}{
		{name: "wrong model", mutate: func(r *Request) { r.ExpectedModelID = "other.camera.model" }, want: ErrIdentityUnverified},
		{name: "wrong firmware", mutate: func(r *Request) { r.ExpectedFirmware = "new-firmware" }, want: ErrIdentityUnverified},
		{name: "wrong MAC", mutate: func(r *Request) { r.ExpectedMAC = "94:F8:27:00:00:02" }, want: ErrIdentityUnverified},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy, inventory, _, cloud, sessions, _, request := newPolicyFixture(testCameraIP, testInterface)
			if test.mutate != nil {
				test.mutate(&request)
			}
			if test.camera.DID != "" {
				inventory.snapshot.Cameras[0] = test.camera
			}
			if _, err := policy.Open(context.Background(), request); !errors.Is(err, test.want) {
				t.Fatalf("Open() error = %v, want %v", err, test.want)
			}
			if len(cloud.dids) != 0 || len(sessions.targets) != 0 {
				t.Fatalf("identity mismatch reached cloud/session: %v/%d", cloud.dids, len(sessions.targets))
			}
		})
	}

	policy, _, routes, cloud, sessions, _, request := newPolicyFixture(testCameraIP, testInterface)
	policy.AllowedNetworks = append(policy.AllowedNetworks, AllowedNetwork{CIDR: "192.168.2.0/24", Interface: "wlan0"})
	if _, err := policy.Open(context.Background(), request); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatalf("ambiguous allowlist error = %v, want ErrInvalidPolicy", err)
	}
	if routes.calls != 0 || len(cloud.dids) != 0 || len(sessions.targets) != 0 {
		t.Fatalf("ambiguous allowlist made route/cloud/session calls = %d/%v/%d", routes.calls, cloud.dids, len(sessions.targets))
	}
}
