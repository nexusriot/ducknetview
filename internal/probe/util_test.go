package probe

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCompactRate(t *testing.T) {
	cases := []struct {
		bps  float64
		want string
	}{
		{0, "-"},
		{0.4, "-"},
		{812, "812"},
		{1023, "1023"},
		{1024, "1.0k"},
		{10 * 1024, "10k"},
		{1536 * 1024, "1.5M"},
		{150 * 1024 * 1024, "150M"},
		{3.5 * 1024 * 1024 * 1024, "3.5G"},
	}
	for _, c := range cases {
		if got := CompactRate(c.bps); got != c.want {
			t.Errorf("CompactRate(%v) = %q, want %q", c.bps, got, c.want)
		}
	}

	// Table columns are sized against this: nothing may exceed five columns.
	for _, bps := range []float64{0, 1, 999, 1025, 999 * 1024, 1 << 40, 1 << 50} {
		if got := CompactRate(bps); len(got) > 5 {
			t.Errorf("CompactRate(%v) = %q, wider than 5 columns", bps, got)
		}
	}
}

func TestFormatRateUnits(t *testing.T) {
	// 1 MiB/s is 8.4 Mbit/s: the two units are genuinely different numbers,
	// which is the whole reason for the toggle.
	if got := FormatRate(1024*1024, UnitBytes); got != "1.0 MiB/s" {
		t.Errorf("bytes = %q, want 1.0 MiB/s", got)
	}
	if got := FormatRate(1024*1024, UnitBits); got != "8.4 Mbit/s" {
		t.Errorf("bits = %q, want 8.4 Mbit/s", got)
	}
	if got := FormatRate(100, UnitBits); got != "800 bit/s" {
		t.Errorf("small bit rate = %q, want 800 bit/s", got)
	}
	if got := FormatRate(200*1024*1024, UnitBits); got != "1.68 Gbit/s" {
		t.Errorf("large bit rate = %q, want 1.68 Gbit/s", got)
	}

	// Compact forms stay inside a five-column table cell in either unit.
	for _, bps := range []float64{0, 1, 999, 1 << 20, 1 << 30, 1 << 40} {
		for _, unit := range []RateUnit{UnitBytes, UnitBits} {
			if got := CompactRateUnit(bps, unit); len(got) > 5 {
				t.Errorf("CompactRateUnit(%v, %v) = %q, too wide", bps, unit, got)
			}
		}
	}
	if got := CompactRateUnit(1024*1024, UnitBits); got != "8.4M" {
		t.Errorf("compact bits = %q, want 8.4M", got)
	}
}

func TestIfaceKindJSONRoundTrip(t *testing.T) {
	for k := IfaceUnknown; k <= IfacePhysical; k++ {
		b, err := json.Marshal(k)
		if err != nil {
			t.Fatalf("marshal %v: %v", k, err)
		}
		if !strings.Contains(string(b), k.Label()) {
			t.Errorf("marshalled %v as %s, want the label", k, b)
		}
		var back IfaceKind
		if err := json.Unmarshal(b, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", b, err)
		}
		if back != k {
			t.Errorf("round trip gave %v, want %v", back, k)
		}
	}

	// Older snapshots carry the bare integer.
	var k IfaceKind
	if err := json.Unmarshal([]byte("3"), &k); err != nil || k != IfaceLinuxBridge {
		t.Errorf("integer form gave (%v, %v), want the bridge kind", k, err)
	}
	if err := json.Unmarshal([]byte("999"), &k); err != nil || k != IfaceUnknown {
		t.Errorf("out-of-range integer gave (%v, %v), want unknown", k, err)
	}
}
