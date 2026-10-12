package probe

import (
	"bufio"
	"compress/gzip"
	"fmt"
	"io"
	"net/netip"
	"os"
	"sort"
	"strconv"
	"strings"
)

// A remote address gets a scope and, when it has a PTR record, a name — but
// most cloud addresses have neither a useful name nor any clue who runs them.
// The answer to "who is 13.107.42.14" is its autonomous system, and that is a
// static table: no lookups leave the host, and nothing is bundled. The user
// points the config at a file and the column appears.
//
// The format is the widely mirrored ip2asn TSV (iptoasn.com):
//
//	range_start	range_end	AS_number	country_code	AS_description
//	1.0.0.0	1.0.0.255	13335	US	CLOUDFLARENET
//
// Plain or gzipped, IPv4-only, IPv6-only or the combined file — all four are
// the same five columns, so all four work.

// ASNInfo is the ownership of one address.
type ASNInfo struct {
	Number  uint32
	Name    string
	Country string
}

// Label renders the owner for a table cell: the name when there is one, the AS
// number otherwise.
func (a ASNInfo) Label() string {
	if a.Name != "" {
		return a.Name
	}
	if a.Number > 0 {
		return "AS" + strconv.FormatUint(uint64(a.Number), 10)
	}
	return ""
}

// asnRange is one line of the table. Addresses are kept as netip.Addr so the
// comparison is a plain byte comparison in the right family.
type asnRange struct {
	start, end netip.Addr
	info       ASNInfo
}

// ASNDB is a loaded ownership table, sorted for binary search.
type ASNDB struct {
	v4, v6 []asnRange
	// Source is the file it was read from, for the status line.
	Source string
}

// asnMaxLines bounds how much of a file is read. The combined table is around
// half a million lines; anything far past that is not this format and should
// not be allowed to exhaust memory.
const asnMaxLines = 4_000_000

// LoadASNDB reads an ip2asn table. A gzipped file is detected by its extension,
// which is how the published files are named.
func LoadASNDB(path string) (*ASNDB, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var r io.Reader = f
	if strings.HasSuffix(strings.ToLower(path), ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		defer gz.Close()
		r = gz
	}

	db, err := parseASNTable(r)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if db.Len() == 0 {
		return nil, fmt.Errorf("%s: no usable rows — expected the ip2asn TSV format", path)
	}
	db.Source = path
	return db, nil
}

// parseASNTable reads the TSV. Unparseable lines are skipped rather than
// failing the load: these files are concatenated from several sources and a
// single bad row should not cost the user the whole table.
func parseASNTable(r io.Reader) (*ASNDB, error) {
	db := &ASNDB{}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lines := 0; sc.Scan(); lines++ {
		if lines >= asnMaxLines {
			break
		}
		line := sc.Text()
		if line == "" || line[0] == '#' {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			continue
		}

		start, err1 := netip.ParseAddr(strings.TrimSpace(fields[0]))
		end, err2 := netip.ParseAddr(strings.TrimSpace(fields[1]))
		if err1 != nil || err2 != nil || start.Is4() != end.Is4() || start.Compare(end) > 0 {
			continue
		}

		num, err := strconv.ParseUint(strings.TrimSpace(fields[2]), 10, 32)
		if err != nil {
			continue
		}

		rng := asnRange{start: start, end: end, info: ASNInfo{Number: uint32(num)}}
		if len(fields) > 3 {
			rng.info.Country = strings.TrimSpace(fields[3])
		}
		if len(fields) > 4 {
			rng.info.Name = strings.TrimSpace(fields[4])
		}
		// AS0 is how these tables spell "not announced", which is not ownership.
		if rng.info.Number == 0 && (rng.info.Name == "" || rng.info.Name == "Not routed") {
			continue
		}

		if start.Is4() {
			db.v4 = append(db.v4, rng)
		} else {
			db.v6 = append(db.v6, rng)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}

	sort.Slice(db.v4, func(i, j int) bool { return db.v4[i].start.Compare(db.v4[j].start) < 0 })
	sort.Slice(db.v6, func(i, j int) bool { return db.v6[i].start.Compare(db.v6[j].start) < 0 })
	return db, nil
}

// Len is how many ranges were loaded.
func (db *ASNDB) Len() int {
	if db == nil {
		return 0
	}
	return len(db.v4) + len(db.v6)
}

// Lookup returns the owner of an address. ok is false for a private, loopback
// or otherwise unannounced address, and for a nil database — so callers can
// treat "no database configured" and "no answer" the same way.
func (db *ASNDB) Lookup(ip string) (ASNInfo, bool) {
	if db == nil {
		return ASNInfo{}, false
	}
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return ASNInfo{}, false
	}
	// A v4-mapped v6 address is a v4 address; looking it up in the v6 table
	// would silently miss every such row.
	addr = addr.Unmap()

	table := db.v6
	if addr.Is4() {
		table = db.v4
	}

	// The first range whose start is after the address; the candidate is the
	// one before it.
	i := sort.Search(len(table), func(i int) bool { return table[i].start.Compare(addr) > 0 })
	if i == 0 {
		return ASNInfo{}, false
	}
	rng := table[i-1]
	if addr.Compare(rng.end) > 0 {
		return ASNInfo{}, false
	}
	return rng.info, true
}
