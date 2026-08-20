package probe

import (
	"strings"
	"testing"
)

func TestParseWireless(t *testing.T) {
	body := `Inter-| sta-|   Quality        |   Discarded packets               | Missed | WE
 face | tus | link level noise |  nwid  crypt   frag  retry   misc | beacon | 22
  wlan0: 0000   62.  -48.  -256        0      0      0     14     0        0
  wlan1: 0000   30.  -75.  -92         0      0      0      0     0        0
`
	links := parseWireless(strings.NewReader(body))
	if len(links) != 2 {
		t.Fatalf("parsed %d links, want 2", len(links))
	}

	w := links["wlan0"]
	if w.Quality != 62 || w.Signal != -48 {
		t.Errorf("wlan0 = %+v, want quality 62 signal -48", w)
	}
	// -256 is the "not measured" sentinel, not a real noise floor.
	if w.Noise != 0 {
		t.Errorf("wlan0 noise = %v, want 0 for the sentinel", w.Noise)
	}
	if got := w.SignalPercent(); got != 88 {
		t.Errorf("wlan0 signal percent = %d, want 88 (62/70)", got)
	}
	if got := w.Label(); got != "excellent" {
		t.Errorf("wlan0 label = %q, want excellent for -48 dBm", got)
	}

	if links["wlan1"].Noise != -92 {
		t.Errorf("wlan1 noise = %v, want -92 to be kept", links["wlan1"].Noise)
	}
	if got := links["wlan1"].Label(); got != "weak" {
		t.Errorf("wlan1 label = %q, want weak for -75 dBm", got)
	}
}

func TestWifiLabels(t *testing.T) {
	cases := map[float64]string{-40: "excellent", -55: "good", -65: "fair", -75: "weak", -90: "very weak"}
	for signal, want := range cases {
		if got := (WifiLink{Quality: 40, Signal: signal}).Label(); got != want {
			t.Errorf("Label(%v dBm) = %q, want %q", signal, got, want)
		}
	}
	if got := (WifiLink{}).Label(); got != "unknown" {
		t.Errorf("empty link label = %q, want unknown", got)
	}
	if got := (WifiLink{Quality: 200}).SignalPercent(); got != 100 {
		t.Errorf("out-of-range quality clamped to %d, want 100", got)
	}
}

// A host with no radios is normal, not an error.
func TestWifiLinksOnWiredHost(t *testing.T) {
	links, err := WifiLinks()
	if err != nil {
		t.Skipf("/proc/net/wireless unreadable: %v", err)
	}
	for name, w := range links {
		if w.Iface != name {
			t.Errorf("link keyed %q carries iface %q", name, w.Iface)
		}
	}
}
