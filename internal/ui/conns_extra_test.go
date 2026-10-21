package ui

import (
	"strconv"
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestRevDNSDisplaySubstitution(t *testing.T) {
	m := NewModel()
	m.connsRevDNS = true
	m.dnsCache = map[string]string{"1.2.3.4": "example.com"}
	if got := m.dnsDisplay("1.2.3.4:443"); got != "example.com:443" {
		t.Fatalf("dnsDisplay should substitute hostname, got %q", got)
	}
	// With rev DNS off, the raw remote is returned.
	m.connsRevDNS = false
	if got := m.dnsDisplay("1.2.3.4:443"); got != "1.2.3.4:443" {
		t.Fatalf("dnsDisplay should be a no-op when disabled, got %q", got)
	}
}

func TestPublicOnlyFilter(t *testing.T) {
	m := NewModel()
	m.conns = []probe.Conn{
		{Proto: "tcp", Local: "10.0.0.1:1", Remote: "8.8.8.8:80", State: "ESTABLISHED", PID: 1},   // public
		{Proto: "tcp", Local: "10.0.0.1:2", Remote: "10.0.0.9:80", State: "ESTABLISHED", PID: 2},  // private
		{Proto: "tcp", Local: "10.0.0.1:3", Remote: "127.0.0.1:80", State: "ESTABLISHED", PID: 3}, // local
	}
	m.publicOnly = true
	got := m.filteredConns()
	if len(got) != 1 || probe.IPScope(stripPort(got[0].Remote)) != "public" {
		t.Fatalf("public-only should keep exactly the public remote, got %d rows", len(got))
	}
}

func TestDNSCachePruning(t *testing.T) {
	m := eventsModel()
	m.dnsCache = map[string]string{}
	m.dnsTried = map[string]bool{}
	// Genuinely distinct keys: the pruner only runs once the cap is exceeded.
	for i := 0; i < dnsCacheMax+50; i++ {
		ip := "198.51." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256)
		m.dnsCache[ip] = "host"
		m.dnsTried[ip] = true
	}
	if len(m.dnsCache) <= dnsCacheMax {
		t.Fatalf("test set up only %d entries, need more than the %d cap", len(m.dnsCache), dnsCacheMax)
	}

	live := conn(1, "curl", "10.0.0.1:1", "9.9.9.9:443", 0, 0)
	m.dnsCache["9.9.9.9"] = "dns9"
	m.pruneDNSCache([]probe.Conn{live})

	if len(m.dnsCache) > 10 {
		t.Errorf("cache still holds %d entries after pruning", len(m.dnsCache))
	}
	if m.dnsCache["9.9.9.9"] != "dns9" {
		t.Error("pruning dropped an address that is still connected")
	}
	if len(m.dnsTried) > 10 {
		t.Errorf("tried-set still holds %d entries", len(m.dnsTried))
	}
}
