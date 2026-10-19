package app

func flushDNS() (string, error) {
	return runAdmin("dscacheutil -flushcache; killall -HUP mDNSResponder", "Connaio 要刷新 DNS 缓存。", "DNS 缓存已刷新")
}

// platformChecks: a TUN left by a dead core vanishes with it on macOS, routes included, so there
// is nothing beyond the common checks.
func platformChecks() []RepairItem { return nil }

func platformRepair(string) (string, error) { return "", errUnknownRepair }
