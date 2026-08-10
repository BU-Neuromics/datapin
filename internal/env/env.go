// Package env reads datapin environment variables with backward
// compatibility for the tool's previous life as gosf: DATAPIN_<name> is
// authoritative, and GOSF_<name> is accepted with a deprecation warning
// so existing environments keep working through the rename.
package env

import (
	"os"

	"github.com/BU-Neuromics/datapin/internal/log"
)

// Get returns the value of DATAPIN_<suffix>, falling back to the legacy
// GOSF_<suffix> with a deprecation warning. Returns "" if neither is set.
func Get(suffix string) string {
	if v := os.Getenv("DATAPIN_" + suffix); v != "" {
		return v
	}
	if v := os.Getenv("GOSF_" + suffix); v != "" {
		log.Warnf("GOSF_%s is deprecated — rename it to DATAPIN_%s", suffix, suffix)
		return v
	}
	return ""
}
