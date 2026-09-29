// Package lab gates intentionally injected fault scenarios used for
// troubleshooting practice. Faults are off unless named in LAB_FAULTS,
// e.g. LAB_FAULTS=cpu or LAB_FAULTS=latency (comma-separated).
package lab

import (
	"os"
	"strings"
)

var enabled = parse(os.Getenv("LAB_FAULTS"))

func parse(raw string) map[string]bool {
	out := map[string]bool{}
	for _, v := range strings.Split(raw, ",") {
		if v = strings.TrimSpace(strings.ToLower(v)); v != "" {
			out[v] = true
		}
	}
	return out
}

// Enabled reports whether the named fault scenario is switched on.
func Enabled(name string) bool { return enabled[name] }

// Active lists enabled scenarios for startup logging.
func Active() []string {
	out := make([]string, 0, len(enabled))
	for k := range enabled {
		out = append(out, k)
	}
	return out
}
