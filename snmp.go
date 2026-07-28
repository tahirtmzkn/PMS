package main

import (
	"context"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// SNMP lookup knobs. There is deliberately no UI for them: a wrong community
// or an agent that doesn't answer just means the name falls back to "Unknown",
// which is exactly what a blank name used to give anyway.
const (
	snmpVersion   = "2c"
	snmpCommunity = "public"

	// sysNameOID is SNMPv2-MIB::sysName.0 written numerically. The symbolic
	// name only works where the MIB files are installed (on Debian/Ubuntu they
	// live in the separate snmp-mibs-downloader package, and /etc/snmp/snmp.conf
	// ships with MIB loading switched off), so the OID keeps the lookup working
	// on a machine that only has the `snmp` package.
	sysNameOID = "1.3.6.1.2.1.1.5.0"

	// snmpQueryTimeout (seconds) and snmpRetries bound one lookup at ~2s: the
	// row sits on a placeholder name until this returns, so it must not hang
	// around for long when the target doesn't speak SNMP.
	snmpQueryTimeout = 1
	snmpRetries      = 1
)

// maxSysNameLen caps what a device can put in the Name column. sysName is
// free-form text from the far end, and a table row is not the place to render
// however much of it a misconfigured agent decides to send.
const maxSysNameLen = 64

// snmpSysName asks ip for its SNMP sysName and returns it, or "" if the host
// doesn't answer, doesn't speak SNMP, or snmpget isn't installed. It shells out
// to a subprocess, so call it from a background goroutine, never the UI thread.
func snmpSysName(ip, iface string) string {
	// Backstop for a query that outlives its own -t/-r budget; the margin has
	// to cover process spawn, so it can't be tight.
	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(snmpQueryTimeout*(snmpRetries+1))*time.Second+2*time.Second)
	defer cancel()

	args := []string{
		"-v" + snmpVersion, "-c", snmpCommunity,
		"-t", strconv.Itoa(snmpQueryTimeout), "-r", strconv.Itoa(snmpRetries),
		// -Oqv prints just the value, so the common case needs no parsing at
		// all (parseSysName still copes with the `OID = STRING: ...` form, in
		// case a system snmp.conf sets its own output options).
		"-Oqv",
	}
	// The pings go out the selected interface (ping -I), so this query is
	// sourced from that interface too where that is unambiguous — see
	// interfaceSourceIP for why it can't just be the interface's first address.
	if addr := interfaceSourceIP(iface, ip); addr != "" {
		args = append(args, "--clientaddr="+addr)
	}
	args = append(args, ip, sysNameOID)

	// Output (not Run) so snmpget's own error text — "Timeout: No Response
	// from ..." — is captured instead of printed to the terminal.
	out, err := exec.CommandContext(ctx, "snmpget", args...).Output()
	if err != nil {
		return ""
	}
	return parseSysName(string(out))
}

// interfaceSourceIP returns the address of iface that target is on the same
// subnet as — the source address a reply can actually come back to — or "" if
// there is no such address, in which case the caller must leave the source to
// the kernel's own route lookup rather than guess.
//
// Note that "iface's IPv4 address" is not a thing: one interface routinely
// carries several subnets (the machine this was written on has enp3s0 on
// 192.168.5.0/24, 192.168.10.0/24, 10.0.0.0/24 and 10.4.50.0/24). Binding the
// query to the first of them sent every SNMP request out with a source address
// on the wrong subnet, so the device answered into the void and every name came
// back "Unknown" — while the same query worked by hand, which does no binding at
// all. Off-subnet targets (reached through a gateway) deliberately fall through
// to "": the kernel picks the right source per destination, and second-guessing
// it is what broke this in the first place.
func interfaceSourceIP(iface, target string) string {
	ip := net.ParseIP(target)
	if ip == nil {
		return ""
	}
	dev, err := net.InterfaceByName(iface)
	if err != nil {
		return "" // no such interface (renamed, unplugged, never existed)
	}
	addrs, err := dev.Addrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok || ipNet.IP.To4() == nil {
			continue
		}
		if ipNet.Contains(ip) {
			return ipNet.IP.String()
		}
	}
	return ""
}

// parseSysName pulls the hostname out of snmpget's stdout. Agent-level
// failures ("No Such Object available on this agent...") come back on stdout
// with a zero exit status, so they have to be rejected here rather than by the
// command's error.
func parseSysName(out string) string {
	name := strings.TrimSpace(out)
	// A sysName can legally contain newlines; only the first line is usable in
	// a single-line table cell.
	if i := strings.IndexAny(name, "\r\n"); i >= 0 {
		name = strings.TrimSpace(name[:i])
	}
	// `SNMPv2-MIB::sysName.0 = STRING: "sw-core-01"` -> `sw-core-01`.
	if _, value, ok := strings.Cut(name, " = "); ok {
		name = strings.TrimSpace(value)
		if tag, rest, ok := strings.Cut(name, ": "); ok && !strings.Contains(tag, " ") {
			name = strings.TrimSpace(rest)
		}
	}
	name = strings.Trim(name, `"`)

	for _, failure := range []string{
		"No Such Object", "No Such Instance", "Unknown Object Identifier",
		"Timeout:", "No Response",
	} {
		if strings.Contains(name, failure) {
			return ""
		}
	}

	if runes := []rune(name); len(runes) > maxSysNameLen {
		name = strings.TrimSpace(string(runes[:maxSysNameLen]))
	}
	return name
}
