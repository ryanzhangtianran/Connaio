package sysproxy

import (
	"net/netip"
	"slices"
	"testing"
)

func TestLan(t *testing.T) {
	got := lan([]string{"localhost", "10.0.0.0/8", "172.16.0.0/12"}, []netip.Prefix{netip.MustParsePrefix("172.16.10.0/24")})
	want := []string{"localhost", "10.0.0.0/8", "172.16.0.0/21", "172.16.8.0/23", "172.16.11.0/24", "172.16.12.0/22", "172.16.16.0/20", "172.16.32.0/19", "172.16.64.0/18", "172.16.128.0/17", "172.17.0.0/16",
		"172.18.0.0/15", "172.20.0.0/14", "172.24.0.0/13"}
	if !slices.Equal(got, want) {
		t.Fatalf("got  %v\nwant %v", got, want)
	}
	if got := lan([]string{"10.0.0.0/8"}, []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}); len(got) != 0 {
		t.Fatalf("a cut holding the range leaves nothing: %v", got)
	}
}
