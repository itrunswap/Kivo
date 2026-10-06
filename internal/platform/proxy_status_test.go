package platform

import "testing"

func TestWindowsProxyStatus(t *testing.T) {
	for _, tc := range []struct {
		proxy     string
		automatic bool
		state     string
	}{
		{"", false, "off"}, {"127.0.0.1:17890", false, "this_app"}, {"localhost:17890", false, "this_app"},
		{"http=127.0.0.1:17890;https=127.0.0.1:17890", false, "this_app"},
		{"http=127.0.0.1:17890", false, "other"}, {"127.0.0.1:7890", false, "other"},
		{"127.0.0.1:17890", true, "automatic"}, {"http://secret@example.org:17890", false, "other"},
	} {
		got := windowsProxyStatus(tc.proxy, tc.automatic, "127.0.0.1:17890")
		if got.State != tc.state {
			t.Errorf("%q automatic=%v => %+v", tc.proxy, tc.automatic, got)
		}
	}
}
