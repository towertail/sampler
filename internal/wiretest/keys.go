// Package wiretest holds helpers for the wire contract tests.
package wiretest

// Keys returns the key paths in a decoded JSON value, for example
// "host.name" and "procs.items[].pid". Array elements share one path.
func Keys(v any) map[string]bool {
	out := map[string]bool{}
	walk(v, "", out)
	return out
}

func walk(v any, prefix string, out map[string]bool) {
	switch x := v.(type) {
	case map[string]any:
		for k, child := range x {
			p := k
			if prefix != "" {
				p = prefix + "." + k
			}
			out[p] = true
			walk(child, p, out)
		}
	case []any:
		for _, child := range x {
			walk(child, prefix+"[]", out)
		}
	}
}
