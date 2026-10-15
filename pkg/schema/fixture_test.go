package schema_test

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/towertail/sampler/internal/wiretest"
	"github.com/towertail/sampler/pkg/schema"
)

const fixturePath = "../../testdata/sample-v1.json"

// fixtureOmits lists keys the fixture leaves out on purpose.
// procs.skipped is omitted when false, and true means items is empty.
var fixtureOmits = map[string]bool{"procs.skipped": true}

func TestFixtureV1(t *testing.T) {
	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var s schema.Sample
	if err := dec.Decode(&s); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if s.V != schema.SchemaVersion {
		t.Errorf("fixture v = %d, want %d", s.V, schema.SchemaVersion)
	}

	var fixture any
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	want := wiretest.Keys(fixture)

	out, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var again any
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	got := wiretest.Keys(again)
	for _, k := range sorted(want) {
		if !got[k] {
			t.Errorf("round trip lost key %q", k)
		}
	}

	for _, k := range sorted(schemaKeys(reflect.TypeOf(s), "")) {
		if !want[k] && !fixtureOmits[k] {
			t.Errorf("schema key %q is not in %s; update the fixture", k, fixturePath)
		}
	}
}

// schemaKeys returns the key paths that the JSON tags of t declare.
func schemaKeys(t reflect.Type, prefix string) map[string]bool {
	out := map[string]bool{}
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice {
		if t.Kind() == reflect.Slice {
			prefix += "[]"
		}
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return out
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "" || name == "-" {
			continue
		}
		p := name
		if prefix != "" {
			p = prefix + "." + name
		}
		out[p] = true
		for k := range schemaKeys(f.Type, p) {
			out[k] = true
		}
	}
	return out
}

func sorted(m map[string]bool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
