package ifroute

import (
	"fmt"
	"net"

	"github.com/vishvananda/netlink"
)

const (
	// routeProtocol is the protocol identifier for routes managed by this package.
	// Value 186 is chosen from the user-defined protocol range (143-254) to avoid
	// conflicts with standard Linux routing protocols (kernel, boot, static, etc.).
	// See /etc/iproute2/rt_protos for standard values.
	routeProtocol = 186
)

// copyRoute performs a limited deep copy of a netlink.Route.
// Only the fields relevant for our operations are copied.
func copyRoute(route *netlink.Route) *netlink.Route {
	if route == nil {
		return nil
	}

	r := &netlink.Route{
		Family:    route.Family,
		LinkIndex: route.LinkIndex,
		Protocol:  route.Protocol,
	}

	if route.Dst != nil {
		r.Dst = &net.IPNet{
			IP:   append(net.IP(nil), route.Dst.IP...),
			Mask: append(net.IPMask(nil), route.Dst.Mask...),
		}
	}

	if route.MultiPath != nil {
		r.MultiPath = make([]*netlink.NexthopInfo, len(route.MultiPath))
		for i, nh := range route.MultiPath {
			nexthop := &netlink.NexthopInfo{LinkIndex: nh.LinkIndex}
			r.MultiPath[i] = nexthop
		}
	}
	return r
}

// getIfIndexByName returns the interface index for the given interface name.
func getIfIndexByName(ifName string) (int, error) {
	if ifName == "" {
		return -1, fmt.Errorf("ifName is required")
	}

	netLink, err := netlink.LinkByName(ifName)
	if err != nil {
		return -1, fmt.Errorf("failed to get the network interface: %w", err)
	}
	return netLink.Attrs().Index, nil
}

// getIPNetFromPrefix parses a prefix string and returns a net.IPNet.
func getIPNetFromPrefix(prefix string) (*net.IPNet, error) {
	if prefix == "" {
		return nil, fmt.Errorf("prefix is required")
	}

	_, ipnet, err := net.ParseCIDR(prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to parse the prefix: %w", err)
	}

	if ipnet.String() != prefix {
		return nil, fmt.Errorf("prefix is not canonical (should be %q)", ipnet.String())
	}
	return ipnet, nil
}

// getRoutes invokes lower-level netlink.RouteListFiltered to fetch the matching routes.
func getRoutes(ipnet *net.IPNet, ifIndex int) (*Routes, error) {
	result := &Routes{}

	family := netlink.FAMILY_ALL
	filter := &netlink.Route{Protocol: routeProtocol}
	filterMask := netlink.RT_FILTER_PROTOCOL

	if ipnet != nil {
		filter.Dst = ipnet
		filterMask |= netlink.RT_FILTER_DST

		if ipnet.IP.To4() == nil {
			family = netlink.FAMILY_V6
		} else {
			family = netlink.FAMILY_V4
		}
	}

	routes, err := netlink.RouteListFiltered(family, filter, filterMask)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch routes: %w", err)
	}

	for _, r := range routes {
		// Skip routes that are not directly attached (on-link).
		if !isInterfaceRoute(&r) {
			continue
		}

		// Skip routes that don't have a nexthop to the given interface.
		if ifIndex != -1 && !hasNexthopToInterface(&r, ifIndex) {
			continue
		}

		rCopy := copyRoute(&r)
		if rCopy.MultiPath == nil {
			result.SinglePath = append(result.SinglePath, rCopy)
		} else {
			result.MultiPath = append(result.MultiPath, rCopy)
		}
	}
	return result, nil
}

// hasNexthopToInterface returns true if the route has a nexthop to the given interface.
func hasNexthopToInterface(route *netlink.Route, ifIndex int) bool {
	if route == nil {
		return false
	}

	if ifIndex <= 0 {
		return false
	}

	if route.MultiPath != nil {
		for _, nh := range route.MultiPath {
			if nh.LinkIndex == ifIndex {
				return true
			}
		}
		return false
	}
	return route.LinkIndex == ifIndex
}

// isInterfaceRoute returns true if the route is directly attached (on-link).
func isInterfaceRoute(route *netlink.Route) bool {
	if route == nil {
		return false
	}

	if route.LinkIndex > 0 {
		return true
	}

	if route.MultiPath != nil {
		for _, nh := range route.MultiPath {
			if nh.LinkIndex > 0 {
				return true
			}
		}
	}
	return false
}
