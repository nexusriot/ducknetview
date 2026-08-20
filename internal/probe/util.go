package probe

import "fmt"

func HumanBytesPerSec(bps float64) string {
	// bps is bytes/sec
	const unit = 1024.0
	if bps < unit {
		return fmt.Sprintf("%.0f B/s", bps)
	}
	div, exp := unit, 0
	for n := bps / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"KiB/s", "MiB/s", "GiB/s", "TiB/s", "PiB/s", "EiB/s"}[exp]
	return fmt.Sprintf("%.1f %s", bps/div, suffix)
}

func HumanBytes(b uint64) string {
	const unit = 1024.0
	bf := float64(b)
	if bf < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := unit, 0
	for n := bf / unit; n >= unit && exp < 5; n /= unit {
		div *= unit
		exp++
	}
	suffix := []string{"KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}[exp]
	return fmt.Sprintf("%.1f %s", bf/div, suffix)
}

// CompactRate renders a bytes/sec rate narrowly enough for a table column
// ("-", "812", "9.9k", "144M"). Suffixes are binary multiples, matching
// HumanBytesPerSec; the "/s" is implied by the column header.
func CompactRate(bps float64) string {
	if bps < 1 {
		return "-"
	}
	units := []string{"", "k", "M", "G", "T"}
	v, i := bps, 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f", v)
	}
	if v < 10 {
		return fmt.Sprintf("%.1f%s", v, units[i])
	}
	return fmt.Sprintf("%.0f%s", v, units[i])
}

// RateUnit selects how throughput is worded. Interfaces and ISPs are specified
// in bits per second while sockets are accounted in bytes, so the tool has to
// be able to speak both.
type RateUnit int

const (
	UnitBytes RateUnit = iota
	UnitBits
)

// bitsScale is how many bits are in a byte, as a float for the conversions.
const bitsScale = 8.0

// FormatRate renders a bytes/sec figure in the requested unit, at full width
// ("1.2 MiB/s", "9.8 Mbit/s"). Bit rates use decimal multiples, which is the
// convention every NIC and ISP quotes.
func FormatRate(bps float64, unit RateUnit) string {
	if unit == UnitBytes {
		return HumanBytesPerSec(bps)
	}
	bits := bps * bitsScale
	switch {
	case bits < 1000:
		return fmt.Sprintf("%.0f bit/s", bits)
	case bits < 1e6:
		return fmt.Sprintf("%.1f kbit/s", bits/1e3)
	case bits < 1e9:
		return fmt.Sprintf("%.1f Mbit/s", bits/1e6)
	default:
		return fmt.Sprintf("%.2f Gbit/s", bits/1e9)
	}
}

// CompactRateUnit renders a rate narrowly enough for a table column, in the
// requested unit.
func CompactRateUnit(bps float64, unit RateUnit) string {
	if unit == UnitBytes {
		return CompactRate(bps)
	}
	bits := bps * bitsScale
	if bits < 1 {
		return "-"
	}
	units := []string{"", "k", "M", "G", "T"}
	v, i := bits, 0
	for v >= 1000 && i < len(units)-1 {
		v /= 1000
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f", v)
	}
	if v < 10 {
		return fmt.Sprintf("%.1f%s", v, units[i])
	}
	return fmt.Sprintf("%.0f%s", v, units[i])
}

// CompactBytes renders a byte count as narrowly as CompactRate does, for table
// columns that show cumulative traffic rather than a rate.
func CompactBytes(b uint64) string {
	return CompactRate(float64(b))
}

func ClampHistory[T any](s []T, max int) []T {
	if max <= 0 {
		return s[:0]
	}
	if len(s) <= max {
		return s
	}
	return s[len(s)-max:]
}
