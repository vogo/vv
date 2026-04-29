package configs

import (
	"os"
	"strings"
	"testing"
)

// TestMain neutralizes any VV_* environment variables inherited from the
// developer's shell before any test in this package runs. configs.Load reads
// 39 different VV_* variables; without this, an exported VV_WEB_SEARCH_*,
// VV_LLM_*, VV_PERMISSION_MODE etc. would silently override YAML and break
// assertions about default behavior.
//
// Tests that explicitly want a VV_* variable set use t.Setenv, which restores
// the value to the (now-empty) parent state on test cleanup.
func TestMain(m *testing.M) {
	for _, kv := range os.Environ() {
		if i := strings.IndexByte(kv, '='); i > 0 {
			if k := kv[:i]; strings.HasPrefix(k, "VV_") {
				_ = os.Unsetenv(k)
			}
		}
	}
	os.Exit(m.Run())
}
