package tests

import (
	"testing"
	"time"

	"github.com/sagnik26/macdoc/internal/platform"
)

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestTimeoutFor(t *testing.T) {
	def := platform.DefaultTimeout
	cases := []struct {
		name string
		cmd  string
		env  map[string]string
		want time.Duration
	}{
		{"default", "brew", nil, def},
		{"global", "brew", map[string]string{"MACDOC_TIMEOUT": "30"}, 30 * time.Second},
		{"per command wins", "/usr/sbin/system_profiler", map[string]string{"MACDOC_TIMEOUT": "30", "MACDOC_TIMEOUT_SYSTEM_PROFILER": "90"}, 90 * time.Second},
		{"bad per command falls to global", "brew", map[string]string{"MACDOC_TIMEOUT": "30", "MACDOC_TIMEOUT_BREW": "abc"}, 30 * time.Second},
		{"zero ignored", "brew", map[string]string{"MACDOC_TIMEOUT": "0"}, def},
		{"negative ignored", "brew", map[string]string{"MACDOC_TIMEOUT": "-5"}, def},
		{"too large ignored", "brew", map[string]string{"MACDOC_TIMEOUT": "601"}, def},
		{"max accepted", "brew", map[string]string{"MACDOC_TIMEOUT": "600"}, 10 * time.Minute},
		{"min accepted", "brew", map[string]string{"MACDOC_TIMEOUT": "1"}, time.Second},
		{"duration syntax ignored", "brew", map[string]string{"MACDOC_TIMEOUT": "5s"}, def},
		{"look-alike name ignored", "brew", map[string]string{"MACDOC_TIMEOUT_BREWX": "99"}, def},
	}
	for _, c := range cases {
		if got := platform.TimeoutFor(c.cmd, 0, env(c.env)); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	if got := platform.TimeoutFor("brew", 3*time.Second, nil); got != 3*time.Second {
		t.Errorf("explicit default with nil lookup: %v", got)
	}
}
