package tui

import (
	"io"
	"os"
	"strings"
	"testing"
)

func capturePrintSummary(title string, rows [][2]string) string {
	r, w, _ := os.Pipe()
	orig := os.Stdout
	os.Stdout = w
	PrintSummary(title, rows)
	_ = w.Close()
	os.Stdout = orig
	out, _ := io.ReadAll(r)
	return string(out)
}

func TestPrintSummary(t *testing.T) {
	output := capturePrintSummary("Installation Config", [][2]string{{"Role", "server"}, {"Port", "8080"}})
	if !strings.Contains(output, "Installation Config") {
		t.Fatalf("summary output missing title: %q", output)
	}
	if !strings.Contains(output, "Role") || !strings.Contains(output, "server") {
		t.Fatalf("summary output missing role row: %q", output)
	}
	if !strings.Contains(output, "Port") || !strings.Contains(output, "8080") {
		t.Fatalf("summary output missing port row: %q", output)
	}
}
