package probe

import (
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const asnFixture = "1.0.0.0\t1.0.0.255\t13335\tUS\tCLOUDFLARENET\n" +
	"8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n" +
	"13.107.0.0\t13.107.255.255\t8075\tUS\tMICROSOFT-CORP-MSN-AS-BLOCK\n" +
	"192.168.0.0\t192.168.255.255\t0\tNone\tNot routed\n" +
	"2606:4700::\t2606:4700:ffff:ffff:ffff:ffff:ffff:ffff\t13335\tUS\tCLOUDFLARENET\n"

func loadFixture(t *testing.T) *ASNDB {
	t.Helper()
	db, err := parseASNTable(strings.NewReader(asnFixture))
	if err != nil {
		t.Fatalf("parseASNTable: %v", err)
	}
	return db
}

func TestASNLookup(t *testing.T) {
	db := loadFixture(t)

	cases := []struct {
		ip   string
		want string
	}{
		{"8.8.8.8", "GOOGLE"},
		{"8.8.8.0", "GOOGLE"},   // inclusive lower bound
		{"8.8.8.255", "GOOGLE"}, // inclusive upper bound
		{"13.107.42.14", "MICROSOFT-CORP-MSN-AS-BLOCK"},
		{"1.0.0.7", "CLOUDFLARENET"},
		{"2606:4700::1111", "CLOUDFLARENET"},
	}
	for _, c := range cases {
		info, ok := db.Lookup(c.ip)
		if !ok {
			t.Errorf("Lookup(%s) returned ok=false", c.ip)
			continue
		}
		if info.Name != c.want {
			t.Errorf("Lookup(%s) = %q, want %q", c.ip, info.Name, c.want)
		}
	}
}

// An address below the first range, above the last, or in a gap must not be
// attributed to a neighbouring range — a binary search that forgets the end
// bound silently mislabels most of the address space.
func TestASNLookupMisses(t *testing.T) {
	db := loadFixture(t)
	for _, ip := range []string{"0.0.0.1", "9.9.9.9", "255.255.255.255", "2001:db8::1", "not-an-ip", ""} {
		if info, ok := db.Lookup(ip); ok {
			t.Errorf("Lookup(%q) = %+v, want no match", ip, info)
		}
	}
}

// A v4-mapped v6 address is a v4 address; searching the v6 table for it would
// miss every row.
func TestASNLookupV4Mapped(t *testing.T) {
	db := loadFixture(t)
	info, ok := db.Lookup("::ffff:8.8.8.8")
	if !ok || info.Name != "GOOGLE" {
		t.Errorf("Lookup(::ffff:8.8.8.8) = %+v, %v; want GOOGLE", info, ok)
	}
}

// AS0 rows mean "not announced", which is not ownership.
func TestASNSkipsUnrouted(t *testing.T) {
	db := loadFixture(t)
	if info, ok := db.Lookup("192.168.1.1"); ok {
		t.Errorf("Lookup(192.168.1.1) = %+v, want no match for an unrouted range", info)
	}
}

func TestASNNilDatabase(t *testing.T) {
	var db *ASNDB
	if _, ok := db.Lookup("8.8.8.8"); ok {
		t.Error("a nil database must report no match rather than panicking")
	}
	if db.Len() != 0 {
		t.Error("a nil database must have length 0")
	}
}

func TestASNLabel(t *testing.T) {
	cases := map[string]ASNInfo{
		"GOOGLE":  {Number: 15169, Name: "GOOGLE"},
		"AS15169": {Number: 15169},
		"":        {},
	}
	for want, info := range cases {
		if got := info.Label(); got != want {
			t.Errorf("Label(%+v) = %q, want %q", info, got, want)
		}
	}
}

// Malformed rows are skipped, not fatal: these tables are concatenations and
// one bad line should not cost the user the other half million.
func TestASNSkipsMalformedRows(t *testing.T) {
	in := "garbage\n" +
		"1.2.3.4\n" +
		"10.0.0.5\t10.0.0.1\t1\tXX\tBACKWARDS\n" +
		"1.0.0.0\t2606:4700::\t1\tXX\tMIXED-FAMILY\n" +
		"8.8.8.0\t8.8.8.255\tnotanumber\tUS\tBAD-ASN\n" +
		asnFixture
	db, err := parseASNTable(strings.NewReader(in))
	if err != nil {
		t.Fatalf("parseASNTable: %v", err)
	}
	if db.Len() != 4 {
		t.Errorf("loaded %d ranges, want 4 (the good rows of the fixture)", db.Len())
	}
	if info, ok := db.Lookup("10.0.0.3"); ok {
		t.Errorf("a range whose end precedes its start was kept: %+v", info)
	}
}

func TestLoadASNDBPlainAndGzip(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "ip2asn.tsv")
	if err := os.WriteFile(plain, []byte(asnFixture), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(asnFixture)); err != nil {
		t.Fatal(err)
	}
	gz.Close()
	zipped := filepath.Join(dir, "ip2asn.tsv.gz")
	if err := os.WriteFile(zipped, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, path := range []string{plain, zipped} {
		db, err := LoadASNDB(path)
		if err != nil {
			t.Fatalf("LoadASNDB(%s): %v", path, err)
		}
		if info, ok := db.Lookup("8.8.8.8"); !ok || info.Name != "GOOGLE" {
			t.Errorf("%s: Lookup = %+v, %v", path, info, ok)
		}
		if db.Source != path {
			t.Errorf("%s: Source = %q", path, db.Source)
		}
	}
}

// A file that exists but is not this format must say so, rather than loading as
// an empty table that silently never matches anything.
func TestLoadASNDBRejectsWrongFormat(t *testing.T) {
	p := filepath.Join(t.TempDir(), "wrong.tsv")
	if err := os.WriteFile(p, []byte("this is not a TSV\nnor is this\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadASNDB(p); err == nil {
		t.Error("LoadASNDB accepted a file with no usable rows")
	}
	if _, err := LoadASNDB(filepath.Join(t.TempDir(), "missing.tsv")); err == nil {
		t.Error("LoadASNDB accepted a missing file")
	}
}
