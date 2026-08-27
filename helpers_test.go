package ifroute

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/vishvananda/netlink"
)

var (
	flagIf1, flagIf2 bool
	testIf1, testIf2 netlink.Link
)

func setupDummyInterfaces() error {
	ifSeed1 := time.Now().UnixNano() % time.Now().UnixMilli()
	ifIndex1 := int(ifSeed1)
	ifSuffix1 := strconv.FormatInt(ifSeed1, 10)
	ifName1 := fmt.Sprintf("dummy%s", ifSuffix1)

	ifSeed2 := time.Now().UnixNano() % time.Now().UnixMilli()
	ifIndex2 := int(ifSeed2)
	ifSuffix2 := strconv.FormatInt(ifSeed2, 10)
	ifName2 := fmt.Sprintf("dummy%s", ifSuffix2)

	// Create first dummy interface.
	dummy1 := &netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{
			Name:  ifName1,
			Index: ifIndex1,
		},
	}
	if err := netlink.LinkAdd(dummy1); err != nil {
		return err
	} else {
		testIf1 = dummy1
		flagIf1 = true
	}

	// Create second dummy interface.
	dummy2 := &netlink.Dummy{
		LinkAttrs: netlink.LinkAttrs{
			Name:  ifName2,
			Index: ifIndex2,
		},
	}
	if err := netlink.LinkAdd(dummy2); err != nil {
		return err
	} else {
		testIf2 = dummy2
		flagIf2 = true
	}

	// Bring the interfaces up.
	if err := netlink.LinkSetUp(dummy1); err != nil {
		return err
	}

	if err := netlink.LinkSetUp(dummy2); err != nil {
		return err
	}

	return nil
}

func cleanupDummyInterfaces() error {
	var errs string

	if flagIf1 {
		if err := netlink.LinkDel(testIf1); err != nil {
			errs += fmt.Sprintf("testIf1: %s;", err)
		}
	}

	if flagIf2 {
		if err := netlink.LinkDel(testIf2); err != nil {
			errs += fmt.Sprintf("testIf2: %s", err)
		}
	}

	if len(errs) > 0 {
		return errors.New(errs)
	}

	return nil
}

func TestMain(m *testing.M) {
	if err := setupDummyInterfaces(); err != nil {
		panic(err)
	}

	code := m.Run()

	if err := cleanupDummyInterfaces(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to cleanup test interfaces: %s", err)
	}

	os.Exit(code)
}

func TestCopyRoute_NilPointer(t *testing.T) {
	result := copyRoute(nil)
	if result != nil {
		t.Fatalf("expected `nil`, got %+v", result)
	}
}

func TestCopyRoute(t *testing.T) {
	testRoute := &netlink.Route{
		Dst:       netlink.NewIPNet(net.IPv4(127, 0, 0, 127)),
		LinkIndex: 1,
		Protocol:  routeProtocol,
		Family:    netlink.FAMILY_V4,
		MultiPath: []*netlink.NexthopInfo{
			{LinkIndex: 100},
			{LinkIndex: 200},
		},
	}

	result := copyRoute(testRoute)
	if result.LinkIndex != testRoute.LinkIndex {
		t.Errorf("expected `LinkIndex` to be %d, got %d", testRoute.LinkIndex, result.LinkIndex)
	}
	if int(result.Protocol) != int(testRoute.Protocol) {
		t.Errorf("expected `Protocol` to be %d, got %d", testRoute.Protocol, result.Protocol)
	}
	if result.Family != testRoute.Family {
		t.Errorf("expected `Family` to be %d, got %d", testRoute.Family, result.Family)
	}
	if result.Dst == testRoute.Dst {
		t.Error("expected `Dst` to be a distinct value")
	}
	if result.Dst.String() != testRoute.Dst.String() {
		t.Errorf("expected `Dst` to be %q, got %q", testRoute.Dst.String(), result.Dst.String())
	}
	if len(result.MultiPath) != len(testRoute.MultiPath) {
		t.Errorf("expected `MultiPath` to have %d elements, got %d", len(testRoute.MultiPath), len(result.MultiPath))
	}

	for i := range result.MultiPath {
		if result.MultiPath[i] == testRoute.MultiPath[i] {
			t.Error("expected each `NextHopInfo` to be a distinct value")
		}

		if result.MultiPath[i].LinkIndex != testRoute.MultiPath[i].LinkIndex {
			t.Errorf("expected `NextHopInfo` to be %d, got %d", testRoute.MultiPath[i].LinkIndex, result.MultiPath[i].LinkIndex)
		}
	}
}

func TestGetIfIndexByName(t *testing.T) {
	t.Run("Empty name", func(t *testing.T) {
		result, err := getIfIndexByName("")
		if result != -1 && err == nil {
			t.Error("expected an error")
		}
		if err.Error() != "ifName is required" {
			t.Errorf("expected 'ifName is required', got %q", err.Error())
		}
	})

	t.Run("Non existent interface", func(t *testing.T) {
		if result, err := getIfIndexByName("dumb_interface"); result != -1 && err == nil {
			t.Error("expected an error")
		}
	})

	if !flagIf1 || !flagIf2 {
		t.Skip("dummy interfaces are required")
	}

	tests := []struct {
		ifName string
		expect int
	}{
		{
			ifName: testIf1.Attrs().Name,
			expect: testIf1.Attrs().Index,
		},
		{
			ifName: testIf2.Attrs().Name,
			expect: testIf2.Attrs().Index,
		},
	}

	for _, tt := range tests {
		t.Run(tt.ifName, func(t *testing.T) {
			result, err := getIfIndexByName(tt.ifName)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != tt.expect {
				t.Errorf("expected %d, got %d", tt.expect, result)
			}
		})
	}
}

func TestGetIPNetFromPrefix_EmptyString(t *testing.T) {
	ipnet, err := getIPNetFromPrefix("")
	if err == nil {
		t.Error("expected an error")

	}
	if err.Error() != "prefix is required" {
		t.Errorf("expected error 'prefix is required', got %q", err.Error())
	}
	if ipnet != nil {
		t.Errorf("expected `nil`, got %+v", ipnet)
	}
}

func TestGetIPNetFromPrefix_InvalidPrefix(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{
			name:   "missing mask",
			prefix: "192.168.0.0",
		},
		{
			name:   "malformed IP",
			prefix: "not.an.ip.address/24",
		},
		{
			name:   "negative mask",
			prefix: "192.168.0.0/-1",
		},
		{
			name:   "mask too large for IPv4",
			prefix: "192.168.0.0/33",
		},
		{
			name:   "mask too large for IPv6",
			prefix: "2001:db8::/129",
		},
		{
			name:   "mixed IP version and mask",
			prefix: "192.168.0.0/64",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ipnet, err := getIPNetFromPrefix(tt.prefix)
			if err == nil {
				t.Error("expected an error")
			}
			if !strings.HasPrefix(err.Error(), "failed to parse the prefix: ") {
				t.Errorf("expected error to start with 'failed to parse the prefix:', got %q", err.Error())
			}
			if ipnet != nil {
				t.Errorf("expected `nil`, got %+v", ipnet)
			}
		})
	}
}

func TestGetIPNetFromPrefix_NonCanonical(t *testing.T) {
	tests := []struct {
		name          string
		prefix        string
		canonicalForm string
	}{
		{
			name:          "IPv4 with host bits set",
			prefix:        "192.168.1.1/24",
			canonicalForm: "192.168.1.0/24",
		},
		{
			name:          "IPv4 with broadcast address",
			prefix:        "10.0.0.255/24",
			canonicalForm: "10.0.0.0/24",
		},
		{
			name:          "IPv6 with host bits set",
			prefix:        "2001:db8::1/64",
			canonicalForm: "2001:db8::/64",
		},
		{
			name:          "IPv6 with compressed format variation",
			prefix:        "2001:0db8:0000:0000:0000:0000:0000:0000/64",
			canonicalForm: "2001:db8::/64",
		},
		{
			name:          "IPv4 with non-zero host bits",
			prefix:        "172.16.5.10/12",
			canonicalForm: "172.16.0.0/12",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ipnet, err := getIPNetFromPrefix(tt.prefix)
			if err == nil {
				t.Error("expected an error")
			}

			expectedErr := fmt.Sprintf("prefix is not canonical (should be %q)", tt.canonicalForm)
			if err != nil && err.Error() != expectedErr {
				t.Errorf("expected error %q, got %q", expectedErr, err.Error())
			}
			if ipnet != nil {
				t.Errorf("expected `nil`, got %+v", ipnet)
			}
		})
	}
}

func TestGetIPNetFromPrefix(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
	}{
		{
			name:   "IPv4 /24 network",
			prefix: "192.168.1.0/24",
		},
		{
			name:   "IPv4 /16 network",
			prefix: "10.0.0.0/16",
		},
		{
			name:   "IPv4 /32 host route",
			prefix: "8.8.8.8/32",
		},
		{
			name:   "IPv4 /0 default route",
			prefix: "0.0.0.0/0",
		},
		{
			name:   "IPv6 /64 network",
			prefix: "2001:db8::/64",
		},
		{
			name:   "IPv6 /48 network",
			prefix: "2001:db8:1234::/48",
		},
		{
			name:   "IPv6 /128 host route",
			prefix: "::1/128",
		},
		{
			name:   "IPv6 /0 default route",
			prefix: "::/0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ipnet, err := getIPNetFromPrefix(tt.prefix)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if ipnet == nil {
				t.Fatal("expected `*net.IPNet`, got `nil`")
			}
			if ipnet.String() != tt.prefix {
				t.Errorf("expected %q, got %q", tt.prefix, ipnet.String())
			}
			if !ipnet.IP.Equal(ipnet.IP.Mask(ipnet.Mask)) {
				t.Errorf("expected network address %v to have all host bits zero", ipnet.IP)
			}
		})
	}
}

func TestHasNextHopToInterface(t *testing.T) {
	tests := []struct {
		name    string
		route   *netlink.Route
		ifIndex int
		expect  bool
	}{
		{
			name:    "nil route",
			route:   nil,
			ifIndex: 1,
			expect:  false,
		},
		{
			name:    "invalid ifIndex (0)",
			route:   &netlink.Route{LinkIndex: 1},
			ifIndex: 0,
			expect:  false,
		},
		{
			name:    "invalid ifIndex (-1)",
			route:   &netlink.Route{LinkIndex: 1},
			ifIndex: -1,
			expect:  false,
		},
		{
			name: "matching single-hop route",
			route: &netlink.Route{
				LinkIndex: 10,
				MultiPath: nil,
			},
			ifIndex: 10,
			expect:  true,
		},
		{
			name: "non-matching single-hop route",
			route: &netlink.Route{
				LinkIndex: 10,
				MultiPath: nil,
			},
			ifIndex: 20,
			expect:  false,
		},
		{
			name: "matching multi-path route",
			route: &netlink.Route{
				MultiPath: []*netlink.NexthopInfo{
					{LinkIndex: 5},
					{LinkIndex: 10},
				},
			},
			ifIndex: 10,
			expect:  true,
		},
		{
			name: "non-matching multi-path route",
			route: &netlink.Route{
				MultiPath: []*netlink.NexthopInfo{
					{LinkIndex: 5},
					{LinkIndex: 15},
				},
			},
			ifIndex: 10,
			expect:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hasNexthopToInterface(tt.route, tt.ifIndex)
			if got != tt.expect {
				t.Errorf("hasNexthopToInterface(%v, %d) = %v, expected %v",
					tt.route, tt.ifIndex, got, tt.expect)
			}
		})
	}
}

func TestIsInterfaceRoute(t *testing.T) {
	tests := []struct {
		name   string
		route  *netlink.Route
		expect bool
	}{
		{
			name:   "nil route",
			route:  nil,
			expect: false,
		},
		{
			name:   "direct single-path route",
			route:  &netlink.Route{LinkIndex: 42},
			expect: true,
		},
		{
			name:   "non-direct single-path route",
			route:  &netlink.Route{Gw: net.IP{127, 0, 0, 1}},
			expect: false,
		},
		{
			name: "direct multi-path route",
			route: &netlink.Route{
				MultiPath: []*netlink.NexthopInfo{
					{LinkIndex: 5},
					{LinkIndex: 15},
				},
			},
			expect: true,
		},
		{
			name: "non-direct multi-path route",
			route: &netlink.Route{
				MultiPath: []*netlink.NexthopInfo{
					{Gw: net.IP{127, 0, 0, 2}},
					{Gw: net.IP{127, 0, 0, 3}},
				},
			},
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isInterfaceRoute(tt.route)
			if got != tt.expect {
				t.Errorf("isInterfaceRoute(%v) = %v, expected %v", tt.route, got, tt.expect)
			}
		})
	}
}

func TestGetRoutes(t *testing.T) {
	if !flagIf1 || !flagIf2 {
		t.Skip("dummy interfaces are required")
	}

	t.Run("verify no routes", func(t *testing.T) {
		for _, tt := range []netlink.Link{testIf1, testIf2} {
			t.Run(tt.Attrs().Name, func(t *testing.T) {
				routes, err := getRoutes(nil, tt.Attrs().Index)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				rr := append([]*netlink.Route{}, routes.SinglePath...)
				rr = append(rr, routes.MultiPath...)
				if len(rr) > 0 {
					t.Fatalf("expected 0 routes prior testing, got %d", len(rr))
				}
			})
		}
	})

	routeSet := []*netlink.Route{
		{Protocol: routeProtocol, Dst: netlink.NewIPNet(net.IPv4(127, 1, 100, 1)), LinkIndex: testIf1.Attrs().Index},
		{Protocol: routeProtocol, Dst: netlink.NewIPNet(net.IPv4(127, 1, 100, 2)), LinkIndex: testIf1.Attrs().Index},
		{Protocol: routeProtocol, Dst: netlink.NewIPNet(net.IPv4(127, 1, 100, 3)), LinkIndex: testIf1.Attrs().Index},
		{Protocol: routeProtocol, Dst: netlink.NewIPNet(net.IPv4(127, 2, 100, 1)), LinkIndex: testIf2.Attrs().Index},
		{Protocol: routeProtocol, Dst: netlink.NewIPNet(net.IPv4(127, 2, 100, 2)), LinkIndex: testIf2.Attrs().Index},
		{Protocol: routeProtocol, Dst: netlink.NewIPNet(net.IPv4(127, 2, 100, 3)), LinkIndex: testIf2.Attrs().Index},
		{Protocol: routeProtocol, Dst: netlink.NewIPNet(net.IPv4(127, 3, 100, 1)), MultiPath: []*netlink.NexthopInfo{
			{LinkIndex: testIf1.Attrs().Index}, {LinkIndex: testIf2.Attrs().Index},
		}},
		{Protocol: routeProtocol, Dst: netlink.NewIPNet(net.IPv4(127, 3, 100, 2)), MultiPath: []*netlink.NexthopInfo{
			{LinkIndex: testIf1.Attrs().Index}, {LinkIndex: testIf2.Attrs().Index},
		}},
		{Protocol: routeProtocol, Dst: netlink.NewIPNet(net.IPv4(127, 3, 100, 3)), MultiPath: []*netlink.NexthopInfo{
			{LinkIndex: testIf1.Attrs().Index}, {LinkIndex: testIf2.Attrs().Index},
		}},
	}

	for i := range routeSet {
		if err := netlink.RouteAdd(routeSet[i]); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}

	t.Cleanup(func() {
		for i := range routeSet {
			if err := netlink.RouteDel(routeSet[i]); err != nil {
				t.Logf("Cleanup: unexpected error: %v", err)
			}
		}
	})

	t.Run("get all routes", func(t *testing.T) {
		routes, err := getRoutes(nil, -1)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(routes.SinglePath) != 6 {
			t.Errorf("expected 6 single-path routes, got %d", len(routes.SinglePath))
		}
		if len(routes.MultiPath) != 3 {
			t.Errorf("expected 3 multi-path routes, got %d", len(routes.MultiPath))
		}
	})

	t.Run("get no routes", func(t *testing.T) {
		routes, err := getRoutes(netlink.NewIPNet(net.IPv4(127, 127, 127, 127)), 999999)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(routes.SinglePath) > 0 {
			t.Errorf("expected 0 single-path routes, got %d", len(routes.SinglePath))
		}
		if len(routes.MultiPath) > 0 {
			t.Errorf("expected 0 multi-path routes, got %d", len(routes.MultiPath))
		}
	})

	t.Run("get single-path routes", func(t *testing.T) {
		tests := []struct {
			ipnet           *net.IPNet
			expectedIfIndex int
		}{
			{
				ipnet:           netlink.NewIPNet(net.IPv4(127, 1, 100, 1)),
				expectedIfIndex: testIf1.Attrs().Index,
			},
			{
				ipnet:           netlink.NewIPNet(net.IPv4(127, 1, 100, 2)),
				expectedIfIndex: testIf1.Attrs().Index,
			},
			{
				ipnet:           netlink.NewIPNet(net.IPv4(127, 1, 100, 3)),
				expectedIfIndex: testIf1.Attrs().Index,
			},
			{
				ipnet:           netlink.NewIPNet(net.IPv4(127, 2, 100, 1)),
				expectedIfIndex: testIf2.Attrs().Index,
			},
			{
				ipnet:           netlink.NewIPNet(net.IPv4(127, 2, 100, 2)),
				expectedIfIndex: testIf2.Attrs().Index,
			},
			{
				ipnet:           netlink.NewIPNet(net.IPv4(127, 2, 100, 3)),
				expectedIfIndex: testIf2.Attrs().Index,
			},
		}

		for _, tt := range tests {
			t.Run(tt.ipnet.String(), func(t *testing.T) {
				routes, err := getRoutes(tt.ipnet, -1)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if len(routes.MultiPath) > 0 {
					t.Error("expected a single-path route")
				}
				if len(routes.SinglePath) > 1 {
					t.Error("expected a single route")
				}
				r := routes.SinglePath[0]
				if r == nil {
					t.Error("expected `*netlink.Route`, got `nil`")
				}
				if r.LinkIndex != tt.expectedIfIndex {
					t.Errorf("expected ifIndex %d, got %d", tt.expectedIfIndex, r.LinkIndex)
				}
			})
		}
	})

	t.Run("get multi-path routes", func(t *testing.T) {
		tests := []*net.IPNet{
			netlink.NewIPNet(net.IPv4(127, 3, 100, 1)),
			netlink.NewIPNet(net.IPv4(127, 3, 100, 2)),
			netlink.NewIPNet(net.IPv4(127, 3, 100, 3)),
		}

		for _, tt := range tests {
			t.Run(tt.String(), func(t *testing.T) {
				routes, err := getRoutes(tt, -1)
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if len(routes.SinglePath) > 0 {
					t.Error("expected a multi-path route")
				}
				if len(routes.MultiPath) > 1 {
					t.Error("expected a single route")
				}
				r := routes.MultiPath[0]
				if r == nil {
					t.Error("expected `*netlink.Route`, got `nil`")
				}
				if !hasNexthopToInterface(r, testIf1.Attrs().Index) {
					t.Errorf("expected to have the ifIndex %d as destination", testIf1.Attrs().Index)
				}
				if !hasNexthopToInterface(r, testIf2.Attrs().Index) {
					t.Errorf("expected to have the ifIndex %d as destination", testIf2.Attrs().Index)
				}
			})
		}
	})

	t.Run("get interface routes", func(t *testing.T) {
		routes, err := getRoutes(nil, testIf1.Attrs().Index)
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(routes.SinglePath) != 3 {
			t.Errorf("expected 3 single-path routes, got %d", len(routes.SinglePath))
		}
		if len(routes.MultiPath) != 3 {
			t.Errorf("expected 3 multi-path routes, got %d", len(routes.MultiPath))
		}

		tests := append([]*netlink.Route{}, routes.SinglePath...)
		tests = append(tests, routes.MultiPath...)

		for _, tt := range tests {
			t.Run(tt.Dst.String(), func(t *testing.T) {
				if tt == nil {
					t.Error("expected `*netlink.Route`, got `nil`")
				}
				if !hasNexthopToInterface(tt, testIf1.Attrs().Index) {
					t.Errorf("expected to have the ifIndex %d as destination", testIf1.Attrs().Index)
				}
			})
		}
	})
}
