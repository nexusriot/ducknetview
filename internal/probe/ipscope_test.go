package probe

import "testing"

func TestIPScope(t *testing.T) {
	cases := map[string]string{
		"127.0.0.1":    "local",
		"0.0.0.0":      "local",
		"::1":          "local",
		"10.0.0.5":     "private",
		"192.168.1.1":  "private",
		"172.16.0.1":   "private",
		"100.64.0.1":   "cgnat",
		"169.254.1.1":  "link",
		"fe80::1":      "link",
		"fd00::1":      "private",
		"8.8.8.8":      "public",
		"1.1.1.1":      "public",
		"2606:4700::1": "public",
		"not-an-ip":    "",
		"":             "",
	}
	for ip, want := range cases {
		if got := IPScope(ip); got != want {
			t.Errorf("IPScope(%q) = %q, want %q", ip, got, want)
		}
	}
}

func TestListenerExposure(t *testing.T) {
	cases := map[string]string{
		"0.0.0.0":      "exposed", // wildcard = all interfaces
		"::":           "exposed",
		"127.0.0.1":    "local",
		"::1":          "local",
		"192.168.1.10": "lan",
		"10.0.0.1":     "lan",
		"172.16.5.5":   "lan",
		"100.64.0.1":   "lan", // CGNAT
		"fe80::1":      "lan", // link-local
		"8.8.8.8":      "exposed",
		"1.1.1.1":      "exposed",
		"not-an-ip":    "",
	}
	for ip, want := range cases {
		if got := ListenerExposure(ip); got != want {
			t.Errorf("ListenerExposure(%q) = %q, want %q", ip, got, want)
		}
	}
}
