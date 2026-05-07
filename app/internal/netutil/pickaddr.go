// Package netutil resolves which Windows-host IP the embedded HTTP server
// should advertise to target VMs.
//
// The wizard knows the target endpoint (e.g. qemu+ssh://root@10.10.1.210/system
// for libvirt, or https://pve1.example.com:8006/ for Proxmox). The HTTP
// server itself binds to 0.0.0.0:<port>, but the URL we put into Agama
// profiles + kernel cmdlines must be a host IP the target VMs can reach.
//
// On a multi-homed Windows machine (Wi-Fi + LAN + VPN + Hyper-V vSwitch),
// the wrong IP turns into a silent install failure: dracut times out
// trying to fetch squashfs.img from an unreachable URL.
//
// Strategy:
//   1. If the user supplied an explicit advertise IP, use it.
//   2. Otherwise: resolve the target endpoint to an IP, then ask the OS
//      what local interface address would route to it (UDP-dial trick —
//      no packets actually sent).
//   3. Fallback: first non-loopback IPv4 with a default route.
package netutil

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// PickAdvertiseIP returns the Windows-host IPv4 the HTTP server should
// advertise to targets reachable via the given endpoint. The endpoint can be
// any of: a bare IP/host, a full URL (https://...), or a libvirt URI
// (qemu+ssh://user@host/system).
func PickAdvertiseIP(targetEndpoint string) (string, error) {
	host := extractHost(targetEndpoint)
	if host == "" {
		return defaultRouteIP()
	}

	// Resolve host to an IP if it's a name.
	ips, err := net.LookupIP(host)
	if err != nil || len(ips) == 0 {
		return defaultRouteIP()
	}
	var targetIP net.IP
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			targetIP = v4
			break
		}
	}
	if targetIP == nil {
		return defaultRouteIP()
	}

	// UDP-dial trick: ask the kernel which local IP would route to targetIP.
	// No packets are sent — Dial just resolves the route.
	conn, err := net.Dial("udp4", net.JoinHostPort(targetIP.String(), "1"))
	if err != nil {
		return defaultRouteIP()
	}
	defer func() { _ = conn.Close() }()
	local := conn.LocalAddr().(*net.UDPAddr)
	if local.IP.IsUnspecified() || local.IP.IsLoopback() {
		return defaultRouteIP()
	}
	return local.IP.String(), nil
}

func extractHost(s string) string {
	if s == "" {
		return ""
	}
	// libvirt-style URI (qemu+ssh://user@host/system).
	if strings.Contains(s, "+") && strings.Contains(s, "://") {
		s = strings.SplitN(s, "://", 2)[1]
		if i := strings.Index(s, "@"); i >= 0 {
			s = s[i+1:]
		}
		if i := strings.Index(s, "/"); i >= 0 {
			s = s[:i]
		}
		return s
	}
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err == nil {
			return u.Hostname()
		}
	}
	// Bare host or host:port.
	if i := strings.Index(s, ":"); i >= 0 {
		return s[:i]
	}
	return s
}

func defaultRouteIP() (string, error) {
	conn, err := net.Dial("udp4", "8.8.8.8:1")
	if err != nil {
		return "", fmt.Errorf("default-route dial: %w", err)
	}
	defer func() { _ = conn.Close() }()
	local := conn.LocalAddr().(*net.UDPAddr)
	if local.IP.IsUnspecified() || local.IP.IsLoopback() {
		return "", fmt.Errorf("default route resolved to %s", local.IP)
	}
	return local.IP.String(), nil
}

// ListLocalIPv4 returns every non-loopback IPv4 the host has, useful for
// the wizard's "advertise IP" override dropdown.
func ListLocalIPv4() ([]string, error) {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []string
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ip, _, err := net.ParseCIDR(a.String())
			if err != nil {
				continue
			}
			v4 := ip.To4()
			if v4 == nil || v4.IsLoopback() || v4.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, v4.String())
		}
	}
	return out, nil
}
