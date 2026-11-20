package collect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStableID(t *testing.T) {
	dir := t.TempDir()
	uuid, mid := filepath.Join(dir, "product_uuid"), filepath.Join(dir, "machine-id")
	if got := stableID("boot-id", uuid, mid); got != "" {
		t.Errorf("no source: %q, want empty", got)
	}
	if err := os.WriteFile(mid, []byte("0123456789abcdef0123456789abcdef\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := stableID("m-id", uuid, mid); got != "m-id" {
		t.Errorf("machine-id: %q", got)
	}
	_ = os.Remove(mid)
	if err := os.WriteFile(uuid, []byte("ABC\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	if got := stableID("abc", uuid, mid); got != "abc" {
		t.Errorf("product_uuid: %q", got)
	}
}
