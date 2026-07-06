package cluster

import (
	"strings"
	"testing"
)

func TestFdfOdfPreflightAllowed(t *testing.T) {
	t.Run("fresh run allows 4.20", func(t *testing.T) {
		if err := FdfOdfPreflightAllowed("4.20.1", false); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("fresh run rejects 4.21", func(t *testing.T) {
		err := FdfOdfPreflightAllowed("4.21.0", false)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "4.21") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("resuming from checkpoint allows 4.21", func(t *testing.T) {
		if err := FdfOdfPreflightAllowed("4.21.3", true); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rejects other minors", func(t *testing.T) {
		err := FdfOdfPreflightAllowed("4.19.0", false)
		if err == nil {
			t.Fatal("expected error")
		}
		err = FdfOdfPreflightAllowed("4.22.0", false)
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestOdfRedHatPreflightAllowed(t *testing.T) {
	t.Run("allows 4.20", func(t *testing.T) {
		if err := OdfRedHatPreflightAllowed("4.20.3"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("allows 4.21", func(t *testing.T) {
		if err := OdfRedHatPreflightAllowed("4.21.0"); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("rejects 4.19", func(t *testing.T) {
		err := OdfRedHatPreflightAllowed("4.19.0")
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "4.20.x or 4.21.x") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
	t.Run("rejects 4.22", func(t *testing.T) {
		err := OdfRedHatPreflightAllowed("4.22.0")
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("rejects wrong major", func(t *testing.T) {
		err := OdfRedHatPreflightAllowed("5.20.0")
		if err == nil {
			t.Fatal("expected error")
		}
	})
	t.Run("rejects empty version", func(t *testing.T) {
		err := OdfRedHatPreflightAllowed("")
		if err == nil {
			t.Fatal("expected error")
		}
	})
}

func TestParseFdfMajorMinor(t *testing.T) {
	maj, min, err := ParseFdfMajorMinor("4.20.5")
	if err != nil {
		t.Fatal(err)
	}
	if maj != 4 || min != 20 {
		t.Fatalf("got %d.%d want 4.20", maj, min)
	}
}
