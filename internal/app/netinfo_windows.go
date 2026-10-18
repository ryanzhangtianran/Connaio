package app

import (
	"encoding/csv"
	"github.com/ryanzhangtianran/Connaio/internal/run"
	"net"
	"slices"
	"strings"
)

// routeOf is the default route ("-inet" or "-inet6") as interface and gateway, as `route -n get`
// gives them on macOS; from Get-NetRoute, lowest total metric first.
func routeOf(family string) map[string]string {
	prefix := "0.0.0.0/0"
	if family == "-inet6" {
		prefix = "::/0"
	}
	out, _ := powershell("Get-NetRoute -DestinationPrefix '" + prefix + "' -ErrorAction SilentlyContinue | Sort-Object {$_.RouteMetric + $_.InterfaceMetric} | Select-Object -First 1 InterfaceAlias,NextHop | ConvertTo-Csv -NoTypeInformation")
	return parseRouteCSV(out)
}

// parseRouteCSV reads the one-row CSV ("InterfaceAlias","NextHop") Get-NetRoute was asked for.
func parseRouteCSV(out string) map[string]string {
	rows, err := csv.NewReader(strings.NewReader(strings.TrimSpace(out))).ReadAll()
	if err != nil || len(rows) < 2 || len(rows[1]) < 2 {
		return map[string]string{}
	}
	m := map[string]string{"interface": rows[1][0]}
	if gw := rows[1][1]; gw != "" && gw != "0.0.0.0" && gw != "::" {
		m["gateway"] = gw
	}
	return m
}

// dnsServers are the resolvers configured on the interfaces, IPv4 first.
func dnsServers() []string {
	out, _ := powershell("Get-DnsClientServerAddress -AddressFamily IPv4 -ErrorAction SilentlyContinue | Select-Object -ExpandProperty ServerAddresses")
	var servers []string
	for _, line := range strings.Split(out, "\n") {
		s := strings.TrimSpace(line)
		if ip := net.ParseIP(s); ip != nil && !ip.IsLoopback() && !slices.Contains(servers, s) {
			servers = append(servers, s)
		}
	}
	return servers
}

// pingArgs time one ICMP echo, with a 2s timeout (-w is in milliseconds on Windows).
var pingArgs = []string{"-n", "1", "-w", "2000"}

// arpTable maps IPv4 addresses to MAC addresses from `arp -a`.
func arpTable() map[string]string {
	out, _ := run.Command("arp", "-a").Output()
	return parseWindowsARP(string(out))
}

// parseWindowsARP reads `arp -a` rows ("192.168.1.1  aa-bb-cc-dd-ee-ff  dynamic"), MACs lower-case with colons.
func parseWindowsARP(out string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || net.ParseIP(f[0]) == nil || len(f[1]) != 17 {
			continue
		}
		mac := strings.ToLower(strings.ReplaceAll(f[1], "-", ":"))
		if mac != "ff:ff:ff:ff:ff:ff" && !strings.HasPrefix(mac, "01:00:5e") {
			m[f[0]] = mac
		}
	}
	return m
}

// interfaceLabel is the interface alias itself: Windows names them for users already (以太网, WLAN).
func interfaceLabel(iface string) string { return iface }

// powershell runs command and returns its output, as UTF-8: by default it writes in the console's
// code page (GBK on Chinese Windows), and names like 以太网 came out garbled.
func powershell(command string) (string, error) {
	out, err := run.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "[Console]::OutputEncoding = [Text.Encoding]::UTF8; "+command).Output()
	return string(out), err
}
