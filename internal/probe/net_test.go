package probe

import "testing"

func TestRate(t *testing.T) {
	cases := []struct {
		name      string
		cur, prev uint64
		dt        float64
		want      float64
	}{
		{"normal", 2000, 1000, 1, 1000},
		{"half second", 1500, 1000, 0.5, 1000},
		{"no change", 1000, 1000, 1, 0},
		{"counter reset does not wrap", 500, 1_000_000, 1, 0},
		{"zero dt", 2000, 1000, 0, 0},
		{"negative dt", 2000, 1000, -1, 0},
	}
	for _, c := range cases {
		if got := rate(c.cur, c.prev, c.dt); got != c.want {
			t.Fatalf("%s: rate(%d,%d,%v) = %v, want %v", c.name, c.cur, c.prev, c.dt, got, c.want)
		}
	}
}
