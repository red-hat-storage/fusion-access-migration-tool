package output

import (
	"strings"
	"testing"
	"time"
)

func TestLinePrefixRFC3339Nano(t *testing.T) {
	p := linePrefix()
	if p == "" {
		t.Fatal("empty prefix")
	}
	ts := strings.TrimSpace(p)
	_, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t.Fatalf("linePrefix not RFC3339Nano-parseable: %q: %v", ts, err)
	}
}
