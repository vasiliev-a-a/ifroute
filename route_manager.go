package ifroute

import (
	"context"
	"fmt"
	"net"
	"sort"

	"github.com/vasiliev-a-a/strlock"
	"github.com/vishvananda/netlink"
)

type byLinkIndex []*netlink.NexthopInfo

func (b byLinkIndex) Len() int {
	return len(b)
}
func (b byLinkIndex) Less(i, j int) bool {
	return b[i].LinkIndex < b[j].LinkIndex
}

func (b byLinkIndex) Swap(i, j int) {
	b[i], b[j] = b[j], b[i]
}

type Routes struct {
	SinglePath []*netlink.Route
	MultiPath  []*netlink.Route
}

// RouteManager manages network routes with thread-safe operations for
// adding and removing routes to specific network interfaces.
type RouteManager struct {
	lockMgr *strlock.LockManager
}

// NewRouteManager creates a new routeManager instance with internal
// synchronization primitives initialized.
func NewRouteManager() *RouteManager {
	return &RouteManager{
		lockMgr: strlock.NewLockManager(),
	}
}

// GetRoutes fetches routes that match the given prefix and interface name.
func (rm *RouteManager) GetRoutes(ctx context.Context, prefix, ifName string) (*Routes, error) {
	var ipnet *net.IPNet
	var ifIndex int = -1

	if prefix != "" {
		in, err := getIPNetFromPrefix(prefix)
		if err != nil {
			return nil, &RouteError{
				op:     "GetRoutes",
				ifName: ifName,
				prefix: prefix,
				err:    err,
			}
		}
		ipnet = in
	}

	if ifName != "" {
		ii, err := getIfIndexByName(ifName)
		if err != nil {
			return nil, &RouteError{
				op:     "GetRoutes",
				ifName: ifName,
				prefix: prefix,
				err:    err,
			}
		}
		ifIndex = ii
	}

	lockKey := "GetRoutes-lock-key"
	if ipnet != nil {
		lockKey = ipnet.String()
	}
	releaseLock, err := rm.lockMgr.AcquireLock(ctx, lockKey)
	if err != nil {
		return nil, &RouteError{
			op:     "GetRoutes",
			ifName: ifName,
			prefix: prefix,
			err:    fmt.Errorf("failed to acquire a lock: %w", err),
		}
	}
	defer releaseLock()

	routes, err := getRoutes(ipnet, ifIndex)
	if err != nil {
		return nil, &RouteError{
			op:     "GetRoutes",
			ifName: ifName,
			prefix: prefix,
			err:    err,
		}
	}
	return routes, nil
}

// AddInterfaceRoute adds a route for the given prefix to the given interface.
// If a route already exists for the prefix, it will be converted to a multi-path route
// and the new interface will be added as a new nexthop.
func (rm *RouteManager) AddInterfaceRoute(ctx context.Context, prefix string, ifName string) error {
	ifIndex, err := getIfIndexByName(ifName)
	if err != nil {
		return &RouteError{
			op:     "AddInterfaceRoute",
			ifName: ifName,
			prefix: prefix,
			err:    err,
		}
	}

	ipnet, err := getIPNetFromPrefix(prefix)
	if err != nil {
		return &RouteError{
			op:     "AddInterfaceRoute",
			ifName: ifName,
			prefix: prefix,
			err:    err,
		}
	}

	releaseLock, err := rm.lockMgr.AcquireLock(ctx, ipnet.String())
	if err != nil {
		return &RouteError{
			op:     "AddInterfaceRoute",
			ifName: ifName,
			prefix: prefix,
			err:    fmt.Errorf("failed to acquire a lock: %w", err),
		}
	}
	defer releaseLock()

	// Fetch all routes for the prefix to determine:
	// - If the exact route already exists
	// - If we need to install a single-path route
	// - If we need to convert an existing route to multi-path
	// - If we need to add a new nexthop to an existing multi-path route
	routes, err := getRoutes(ipnet, -1)
	if err != nil {
		return &RouteError{
			op:     "AddInterfaceRoute",
			ifName: ifName,
			prefix: prefix,
			err:    err,
		}
	}

	// Return if the exact route already exists.
	for _, r := range append(routes.SinglePath, routes.MultiPath...) {
		if hasNexthopToInterface(r, ifIndex) {
			return nil
		}
	}

	newRoute := &netlink.Route{Dst: ipnet, Protocol: routeProtocol}
	if ipnet.IP.To4() == nil {
		newRoute.Family = netlink.FAMILY_V6
	} else {
		newRoute.Family = netlink.FAMILY_V4
	}

	// Build a unique set of nexthop indices.
	nexthopSet := make(map[int]bool)

	// Add existing nexthops from single-path routes.
	for _, r := range routes.SinglePath {
		if r.LinkIndex != ifIndex {
			nexthopSet[r.LinkIndex] = true
		}
	}

	// Add existing nexthops from multi-path routes.
	for _, r := range routes.MultiPath {
		for _, nh := range r.MultiPath {
			if nh.LinkIndex != ifIndex {
				nexthopSet[nh.LinkIndex] = true
			}
		}
	}

	// Add the target interface.
	nexthopSet[ifIndex] = true

	// Single-path case.
	if len(nexthopSet) == 1 {
		newRoute.LinkIndex = ifIndex
		newRoute.MultiPath = nil

		if err := netlink.RouteAdd(newRoute); err != nil {
			return &RouteError{
				op:     "AddInterfaceRoute",
				ifName: ifName,
				prefix: prefix,
				err:    fmt.Errorf("RouteAdd(%s): %w", newRoute, err),
			}
		}
	} else {
		// Multi-path case.
		newRoute.MultiPath = make([]*netlink.NexthopInfo, 0, len(nexthopSet))
		for idx := range nexthopSet {
			newRoute.MultiPath = append(newRoute.MultiPath, &netlink.NexthopInfo{LinkIndex: idx})
		}
		sort.Sort(byLinkIndex(newRoute.MultiPath))
		if err := netlink.RouteReplace(newRoute); err != nil {
			return &RouteError{
				op:     "AddInterfaceRoute",
				ifName: ifName,
				prefix: prefix,
				err:    fmt.Errorf("RouteReplace(%s): %w", newRoute, err),
			}
		}
	}
	return nil
}

// DelInterfaceRoute deletes a route for the given prefix to the given interface.
// If the route has multiple nexthops, only the nexthop for the given interface will be removed.
// If it was the last nexthop, the entire route will be deleted.
func (rm *RouteManager) DelInterfaceRoute(ctx context.Context, prefix string, ifName string) error {
	ifIndex, err := getIfIndexByName(ifName)
	if err != nil {
		return &RouteError{
			op:     "DelInterfaceRoute",
			ifName: ifName,
			prefix: prefix,
			err:    err,
		}
	}

	ipnet, err := getIPNetFromPrefix(prefix)
	if err != nil {
		return &RouteError{
			op:     "DelInterfaceRoute",
			ifName: ifName,
			prefix: prefix,
			err:    err,
		}
	}

	releaseLock, err := rm.lockMgr.AcquireLock(ctx, ipnet.String())
	if err != nil {
		return &RouteError{
			op:     "DelInterfaceRoute",
			ifName: ifName,
			prefix: prefix,
			err:    fmt.Errorf("failed to acquire a lock: %w", err),
		}
	}
	defer releaseLock()

	routes, err := getRoutes(ipnet, ifIndex)
	if err != nil {
		return &RouteError{
			op:     "DelInterfaceRoute",
			ifName: ifName,
			prefix: prefix,
			err:    err,
		}
	}

	// Return if the route doesn't exist.
	if len(routes.SinglePath) == 0 && len(routes.MultiPath) == 0 {
		return nil
	}

	// Delete the single-path routes.
	for _, r := range routes.SinglePath {
		if err := netlink.RouteDel(r); err != nil {
			return &RouteError{
				op:     "DelInterfaceRoute",
				ifName: ifName,
				prefix: prefix,
				err:    fmt.Errorf("RouteDel(%s): %w", r, err),
			}
		}
	}

	// Modify the multi-path routes.
	for _, r := range routes.MultiPath {
		newRoute := &netlink.Route{Dst: r.Dst, Family: r.Family, Protocol: routeProtocol}

		for _, nh := range r.MultiPath {
			if nh.LinkIndex != ifIndex {
				nexthop := &netlink.NexthopInfo{LinkIndex: nh.LinkIndex}
				newRoute.MultiPath = append(newRoute.MultiPath, nexthop)
			}
		}

		if len(newRoute.MultiPath) == 0 {
			if err := netlink.RouteDel(newRoute); err != nil {
				return &RouteError{
					op:     "DelInterfaceRoute",
					ifName: ifName,
					prefix: prefix,
					err:    fmt.Errorf("RouteDel(%s): %w", newRoute, err),
				}
			}
		} else {
			sort.Sort(byLinkIndex(newRoute.MultiPath))
			if err := netlink.RouteReplace(newRoute); err != nil {
				return &RouteError{
					op:     "DelInterfaceRoute",
					ifName: ifName,
					prefix: prefix,
					err:    fmt.Errorf("RouteReplace(%s): %w", newRoute, err),
				}
			}
		}
	}
	return nil
}
