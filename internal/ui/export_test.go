package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/nexusriot/ducknetview/internal/probe"
)

func TestCSVSafe(t *testing.T) {
	cases := map[string]string{
		"=HYPERLINK(\"x\")": "'=HYPERLINK(\"x\")",
		"@SUM(A1)":          "'@SUM(A1)",
		"+cmd":              "'+cmd",
		"-":                 "-",
		"-12.5":             "-12.5",
		"curl":              "curl",
		"":                  "",
	}
	for in, want := range cases {
		if got := csvSafe(in); got != want {
			t.Errorf("csvSafe(%q) = %q, want %q", in, got, want)
		}
	}
}

// Export writes a CSV of the current view to the working directory.
func TestExportWritesCSV(t *testing.T) {
	dir := t.TempDir()
	wd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(wd)

	m := NewModel()
	m.activeTab = tabPorts
	m.ports = []probe.ListenPort{{Proto: "tcp", Local: "0.0.0.0:443", PID: 1, Process: "nginx"}}

	cmd := exportViewCmd(m)
	if cmd == nil {
		t.Fatal("expected an export command")
	}
	res, ok := cmd().(exportResultMsg)
	if !ok || res.err != nil {
		t.Fatalf("export failed: %+v", res)
	}
	b, err := os.ReadFile(res.path)
	if err != nil {
		t.Fatalf("read exported file: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "proto,local,service,exposure,in_baseline,user,pid,process") || !strings.Contains(s, "nginx") {
		t.Fatalf("unexpected CSV contents:\n%s", s)
	}
}
