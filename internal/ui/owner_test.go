package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
)

// Re-applying the config must not re-read an ASN table that has not changed:
// it is the one setting that costs seconds of a blocked update loop.
func TestASNDatabaseIsNotReloadedWhenUnchanged(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asn.tsv")
	if err := os.WriteFile(path, []byte("8.8.8.0\t8.8.8.255\t15169\tUS\tGOOGLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	m := NewModel()
	m.asnPath = path
	m.loadASNDB()
	first := m.asnDB
	if first == nil {
		t.Fatal("the table should have loaded")
	}

	// Remove the file: a reload that re-reads would now fail and drop the
	// column, which is exactly what must not happen.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	m.loadASNDB()
	if m.asnDB != first {
		t.Error("an unchanged path re-read the table")
	}
	if m.asnErr != nil {
		t.Errorf("unexpected error: %v", m.asnErr)
	}
}

func TestOwnerColumnAppearsOnlyWithADatabase(t *testing.T) {
	plain := NewModel()
	plain.w, plain.h = 170, 45
	plain.connsVP.Width = 150
	for _, c := range plain.connsColumns() {
		if c.key == "owner" {
			t.Fatal("the OWNER column appears with no database loaded")
		}
	}

	m := ownerModel(t)
	found := false
	for _, c := range m.connsColumns() {
		if c.key == "owner" {
			found = true
		}
	}
	if !found {
		t.Fatal("the OWNER column is missing although a database is loaded")
	}
}

func TestOwnerLookup(t *testing.T) {
	m := ownerModel(t)
	if got := m.ownerOf("8.8.8.8:443"); got != "GOOGLE" {
		t.Errorf("ownerOf = %q, want GOOGLE", got)
	}
	// A private address has no owner, and must not borrow a neighbour's.
	if got := m.ownerOf("192.168.1.5:443"); got != "" {
		t.Errorf("ownerOf(private) = %q, want empty", got)
	}
	if got := m.ownerDetail("8.8.8.8:443"); !strings.Contains(got, "AS15169") || !strings.Contains(got, "US") {
		t.Errorf("ownerDetail = %q", got)
	}
}

func TestOwnerColumnRenders(t *testing.T) {
	m := ownerModel(t)
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.2:4001", Remote: "8.8.8.8:443", State: "ESTABLISHED", PID: 1, Process: "x"},
		{Proto: "tcp", Local: "10.0.0.2:4002", Remote: "192.168.1.5:443", State: "ESTABLISHED", PID: 2, Process: "y"},
	}
	txt := stripANSI(m.renderConnsText())
	if !strings.Contains(txt, "OWNER") {
		t.Errorf("header has no OWNER column:\n%s", txt)
	}
	if !strings.Contains(txt, "GOOGLE") {
		t.Errorf("the owner is not rendered:\n%s", txt)
	}
}

// A broken or missing database must not take the session with it.
func TestOwnerDatabaseFailureIsNotFatal(t *testing.T) {
	m := NewModel()
	m.asnPath = filepath.Join(t.TempDir(), "missing.tsv")
	m.loadASNDB()
	if m.asnErr == nil {
		t.Error("a missing database was not reported")
	}
	if m.asnDB.Len() != 0 {
		t.Error("a failed load left a database behind")
	}
	if got := m.ownerOf("8.8.8.8:443"); got != "" {
		t.Errorf("ownerOf = %q with no database", got)
	}
}

func TestOwnerSort(t *testing.T) {
	m := ownerModel(t)
	rows := []probe.Conn{
		{Remote: "8.8.8.8:443"},   // GOOGLE
		{Remote: "1.0.0.1:443"},   // CLOUDFLARENET
		{Remote: "192.168.1.1:1"}, // no owner
	}
	for i, col := range connsSortCols {
		if col == "owner" {
			m.connsSortCol = i
		}
	}
	m.sortConns(rows)
	if m.ownerOf(rows[0].Remote) != "" {
		t.Errorf("sorted = %v, want the unowned row first ascending", rows)
	}
	if m.ownerOf(rows[1].Remote) != "CLOUDFLARENET" || m.ownerOf(rows[2].Remote) != "GOOGLE" {
		t.Errorf("owners out of order: %v", []string{
			m.ownerOf(rows[1].Remote), m.ownerOf(rows[2].Remote)})
	}
}
