package probe

import (
	"bufio"
	"io"
	"os"
	"strconv"
	"strings"
)

// WifiLink is the radio state of a wireless interface. On a laptop or a
// handheld, "the network is slow" is very often "the radio is weak", which no
// amount of socket accounting will tell you.
type WifiLink struct {
	Iface   string
	Quality float64 // link quality, driver-scaled (typically 0-70)
	Signal  float64 // dBm
	Noise   float64 // dBm, 0 when the driver does not report it
}

// SignalPercent maps the link quality onto 0-100. Drivers report quality
// against their own maximum; 70 is what mac80211 uses, which covers every
// modern Linux wireless driver.
func (w WifiLink) SignalPercent() int {
	const driverMax = 70.0
	pct := int(w.Quality / driverMax * 100)
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	return pct
}

// Label describes the link strength in words, since dBm means nothing to most
// people looking at a monitor.
func (w WifiLink) Label() string {
	switch {
	case w.Signal == 0 && w.Quality == 0:
		return "unknown"
	case w.Signal >= -50:
		return "excellent"
	case w.Signal >= -60:
		return "good"
	case w.Signal >= -70:
		return "fair"
	case w.Signal >= -80:
		return "weak"
	default:
		return "very weak"
	}
}

// WifiLinks reads /proc/net/wireless. Hosts with no wireless interfaces return
// an empty map rather than an error.
func WifiLinks() (map[string]WifiLink, error) {
	f, err := os.Open("/proc/net/wireless")
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]WifiLink{}, nil
		}
		return nil, err
	}
	defer f.Close()

	return parseWireless(f), nil
}

// parseWireless reads the two header lines and then one line per radio:
//
//	wlan0: 0000   70.  -40.  -256        0      0      0      0     0        0
func parseWireless(r io.Reader) map[string]WifiLink {
	out := map[string]WifiLink{}

	sc := bufio.NewScanner(r)
	for line := 0; sc.Scan(); line++ {
		if line < 2 { // two header lines
			continue
		}
		text := sc.Text()
		colon := strings.IndexByte(text, ':')
		if colon <= 0 {
			continue
		}

		name := strings.TrimSpace(text[:colon])
		fields := strings.Fields(text[colon+1:])
		if name == "" || len(fields) < 4 {
			continue
		}

		// Values carry a trailing "." to mark them as updated.
		num := func(s string) float64 {
			v, err := strconv.ParseFloat(strings.TrimSuffix(s, "."), 64)
			if err != nil {
				return 0
			}
			return v
		}
		link := WifiLink{
			Iface:   name,
			Quality: num(fields[1]),
			Signal:  num(fields[2]),
			Noise:   num(fields[3]),
		}
		// Drivers that don't measure noise report the sentinel -256.
		if link.Noise <= -256 {
			link.Noise = 0
		}
		out[name] = link
	}
	return out
}
