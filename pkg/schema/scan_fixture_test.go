package schema_test

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/towertail/sampler/internal/wiretest"
	"github.com/towertail/sampler/pkg/schema"
)

// TestScanFixtures decodes each line of testdata/scan-<os>.ndjson with
// no unknown keys. Together the fixtures must hold each key of the
// progress and result lines.
func TestScanFixtures(t *testing.T) {
	seen := map[string]map[string]bool{
		schema.ScanLineProgress: {},
		schema.ScanLineResult:   {},
	}
	for _, goos := range []string{"darwin", "linux", "windows"} {
		raw, err := os.ReadFile("../../testdata/scan-" + goos + ".ndjson")
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(bytes.NewReader(raw))
		sc.Buffer(nil, 16<<20)
		var last string
		for sc.Scan() {
			var l schema.ScanLine
			if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
				t.Fatalf("%s: %v", goos, err)
			}
			if l.V != schema.ScanVersion {
				t.Errorf("%s: v = %d", goos, l.V)
			}
			var v any
			switch l.T {
			case schema.ScanLineProgress:
				v = &schema.ScanProgress{}
			case schema.ScanLineResult:
				v = &schema.ScanResult{}
			default:
				t.Fatalf("%s: unexpected line type %q", goos, l.T)
			}
			dec := json.NewDecoder(bytes.NewReader(sc.Bytes()))
			dec.DisallowUnknownFields()
			if err := dec.Decode(v); err != nil {
				t.Fatalf("%s %s: %v", goos, l.T, err)
			}
			var m any
			_ = json.Unmarshal(sc.Bytes(), &m)
			for k := range wiretest.Keys(m) {
				// Tree nodes at each depth count as the root node.
				if strings.HasPrefix(k, "tree.") {
					k = strings.ReplaceAll(k, "c[].", "")
				}
				seen[l.T][k] = true
			}
			last = l.T
		}
		if last != schema.ScanLineResult {
			t.Errorf("%s: last line is %q, want result", goos, last)
		}
	}
	want := map[string]reflect.Type{
		schema.ScanLineProgress: reflect.TypeOf(schema.ScanProgress{}),
		schema.ScanLineResult:   reflect.TypeOf(schema.ScanResult{}),
	}
	for typ, rt := range want {
		for _, k := range sorted(schemaKeys(rt, "")) {
			if !seen[typ][k] {
				t.Errorf("%s key %q is in no scan fixture", typ, k)
			}
		}
	}
}
