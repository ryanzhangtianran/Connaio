// Package sysproxy points the system proxy (HTTP, HTTPS, SOCKS) at the core's mixed-port: every
// enabled network service via networksetup on macOS, GNOME's proxy settings on Linux.
package sysproxy

import "net/netip"

// lan is the platform's local and LAN bypass with proxied cut out of its ranges: a subnet the rules
// send to a VPN (172.16.10.0/24 inside 172.16.0.0/12) reaches the proxy, the rest of the range not.
func lan(bypass []string, proxied []netip.Prefix) []string {
	var out []string
	for _, h := range bypass {
		p, err := netip.ParsePrefix(h)
		if err != nil {
			out = append(out, h)
			continue
		}
		for _, q := range without(p, proxied) {
			out = append(out, q.String())
		}
	}
	return out
}

// without is p less every prefix in cut: p itself, its halves, and so on down to what cut leaves.
func without(p netip.Prefix, cut []netip.Prefix) []netip.Prefix {
	for _, c := range cut {
		if c.Bits() <= p.Bits() && c.Contains(p.Addr()) {
			return nil // p lies inside c
		}
	}
	for _, c := range cut {
		if p.Contains(c.Addr()) { // c lies inside p: split p
			bits := p.Bits() + 1
			lo := netip.PrefixFrom(p.Addr(), bits)
			hi := netip.PrefixFrom(nextAt(p.Addr(), bits), bits)
			return append(without(lo, cut), without(hi, cut)...)
		}
	}
	return []netip.Prefix{p}
}

// nextAt is a with the bit at prefix length bits set: the upper half's start.
func nextAt(a netip.Addr, bits int) netip.Addr {
	b := a.AsSlice()
	b[(bits-1)/8] |= 0x80 >> ((bits - 1) % 8)
	out, _ := netip.AddrFromSlice(b)
	return out
}
