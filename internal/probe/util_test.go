package probe

import "testing"

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
