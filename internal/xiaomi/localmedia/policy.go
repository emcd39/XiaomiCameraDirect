// Package localmedia contains the fail-closed preflight for an explicitly
// configured local-media policy. It owns no sockets and performs no cloud
// calls itself; callers inject those operations so the ordering can be tested
// without contacting a camera or gateway.
package localmedia

import (
	"context"
	"errors"
	"net"
	"strings"
)

var (
	ErrInvalidPolicy            = errors.New("local_media_policy_invalid")
	ErrIdentityUnverified       = errors.New("identity_unverified")
	ErrInventoryStale           = errors.New("inventory_stale")
	ErrTargetInvalid            = errors.New("target_invalid")
	ErrTargetOutsideNetwork     = errors.New("target_outside_allowed_network")
	ErrRouteInterfaceMismatch   = errors.New("route_interface_mismatch")
	ErrLocalInterfaceUnverified = errors.New("local_interface_unverified")
	ErrCloudMaterialUnavailable = errors.New("cloud_material_unavailable")
	ErrSessionOpen              = errors.New("local_session_open_failed")
)

// AllowedNetwork is an administrator-configured pair. A target must match the
// CIDR and the route resolver must select exactly this interface.
type AllowedNetwork struct {
	CIDR      string
	Interface string
}

type Camera struct {
	DID      string
	ModelID  string
	Firmware string
	IP       string
	MAC      string
}

type InventorySnapshot struct {
	Revision uint64
	Cameras  []Camera
}

type Inventory interface {
	Snapshot(context.Context) (InventorySnapshot, error)
}

type Route struct {
	Interface string
}

type RouteResolver interface {
	Lookup(context.Context, net.IP) (Route, error)
}

// CloudPreparer represents the existing MISS key preparation for one DID.
// local_media_required allows this step after local preflight; offline_required
// would use a separate policy and must not call it.
type CloudPreparer interface {
	Prepare(context.Context, string) error
}

// SessionTarget carries the verified endpoint to an opener that must bind or
// otherwise enforce the named interface. An opener unable to do so must fail.
type SessionTarget struct {
	DID               string
	IP                net.IP
	Interface         string
	InventoryRevision uint64
}

type SessionOpener interface {
	Open(context.Context, SessionTarget) error
}

type Request struct {
	DID               string
	ExpectedModelID   string
	ExpectedFirmware  string
	ExpectedIP        string
	ExpectedMAC       string
	InventoryRevision uint64
}

type Policy struct {
	AllowedNetworks []AllowedNetwork
	Inventory       Inventory
	Routes          RouteResolver
	Cloud           CloudPreparer
	Sessions        SessionOpener
}

type verifiedTarget struct {
	camera    Camera
	ip        net.IP
	iface     string
	revision  uint64
	allowedIP *net.IPNet
}

// Open performs local preflight before cloud preparation and repeats both
// inventory and route checks immediately before opening the local session.
// Every dependency is injected; this package's tests use fakes only.
func (p Policy) Open(ctx context.Context, request Request) error {
	if ctx == nil || ctx.Err() != nil {
		if ctx != nil {
			return ctx.Err()
		}
		return context.Canceled
	}
	if strings.TrimSpace(request.DID) == "" || strings.TrimSpace(request.ExpectedModelID) == "" || request.InventoryRevision == 0 ||
		p.Inventory == nil || p.Routes == nil || p.Cloud == nil || p.Sessions == nil || len(p.AllowedNetworks) == 0 {
		return ErrInvalidPolicy
	}

	target, err := p.currentTarget(ctx, request)
	if err != nil {
		return err
	}
	if err := p.verifyRoute(ctx, target); err != nil {
		return err
	}
	// Do not send MISS/cloud work when identity or route changed during preflight.
	if _, err := p.currentTarget(ctx, request); err != nil {
		return err
	}
	if err := p.Cloud.Prepare(ctx, target.camera.DID); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrCloudMaterialUnavailable
	}

	current, err := p.currentTarget(ctx, request)
	if err != nil {
		return err
	}
	if err := p.verifyRoute(ctx, current); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := p.Sessions.Open(ctx, SessionTarget{
		DID: current.camera.DID, IP: append(net.IP(nil), current.ip...),
		Interface: current.iface, InventoryRevision: current.revision,
	}); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrSessionOpen
	}
	return nil
}

func (p Policy) currentTarget(ctx context.Context, request Request) (verifiedTarget, error) {
	snapshot, err := p.Inventory.Snapshot(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return verifiedTarget{}, ctx.Err()
		}
		return verifiedTarget{}, ErrIdentityUnverified
	}
	if snapshot.Revision != request.InventoryRevision {
		return verifiedTarget{}, ErrInventoryStale
	}
	var camera Camera
	found := false
	for _, candidate := range snapshot.Cameras {
		if candidate.DID != request.DID {
			continue
		}
		if found {
			return verifiedTarget{}, ErrIdentityUnverified
		}
		camera, found = candidate, true
	}
	if !found || camera.DID == "" {
		return verifiedTarget{}, ErrIdentityUnverified
	}
	if camera.ModelID != request.ExpectedModelID || camera.Firmware != request.ExpectedFirmware {
		return verifiedTarget{}, ErrIdentityUnverified
	}
	ip := net.ParseIP(strings.TrimSpace(camera.IP))
	expectedIP := net.ParseIP(strings.TrimSpace(request.ExpectedIP))
	if !validLocalUnicast(ip) || expectedIP == nil {
		return verifiedTarget{}, ErrTargetInvalid
	}
	if !ip.Equal(expectedIP) {
		return verifiedTarget{}, ErrInventoryStale
	}
	if request.ExpectedMAC != "" && !sameMAC(camera.MAC, request.ExpectedMAC) {
		return verifiedTarget{}, ErrIdentityUnverified
	}
	iface, allowedIP, err := p.allowedInterface(ip)
	if err != nil {
		return verifiedTarget{}, err
	}
	return verifiedTarget{camera: camera, ip: ip, iface: iface, revision: snapshot.Revision, allowedIP: allowedIP}, nil
}

func (p Policy) allowedInterface(ip net.IP) (string, *net.IPNet, error) {
	var selected string
	var selectedNet *net.IPNet
	for _, allowed := range p.AllowedNetworks {
		if strings.TrimSpace(allowed.Interface) == "" {
			return "", nil, ErrInvalidPolicy
		}
		_, network, err := net.ParseCIDR(allowed.CIDR)
		if err != nil {
			return "", nil, ErrInvalidPolicy
		}
		if !network.Contains(ip) {
			continue
		}
		if selected != "" && selected != allowed.Interface {
			return "", nil, ErrInvalidPolicy
		}
		selected, selectedNet = allowed.Interface, network
	}
	if selected == "" {
		return "", nil, ErrTargetOutsideNetwork
	}
	return selected, selectedNet, nil
}

func (p Policy) verifyRoute(ctx context.Context, target verifiedTarget) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	route, err := p.Routes.Lookup(ctx, append(net.IP(nil), target.ip...))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrLocalInterfaceUnverified
	}
	if strings.TrimSpace(route.Interface) == "" {
		return ErrLocalInterfaceUnverified
	}
	if route.Interface != target.iface || !target.allowedIP.Contains(target.ip) {
		return ErrRouteInterfaceMismatch
	}
	return nil
}

func validLocalUnicast(ip net.IP) bool {
	return ip != nil && ip.IsGlobalUnicast() && !ip.IsLoopback() && !ip.IsMulticast() && !ip.IsUnspecified() &&
		(ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func sameMAC(left, right string) bool {
	clean := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		value = strings.NewReplacer(":", "", "-", "", ".", "").Replace(value)
		return value
	}
	return clean(left) != "" && clean(left) == clean(right)
}
