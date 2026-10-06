package platform

import (
	"strings"
	"testing"
)

func TestProxyGuidesHaveHostScopeAndSafeDisableInstructions(t *testing.T) {
	for _, system := range []string{"windows", "darwin", "linux"} {
		guide := proxyGuideFor(system, 23456)
		if guide.Port != 23456 || guide.Address != "127.0.0.1" || len(guide.Steps) < 3 {
			t.Fatalf("%+v", guide)
		}
		if !strings.Contains(strings.Join(guide.Steps, " "), "23456") || !strings.Contains(guide.Disable, "不会自动还原") || !strings.Contains(guide.Warning, "公司") || !strings.Contains(guide.Warning, "远程") {
			t.Fatalf("%+v", guide)
		}
	}
}
