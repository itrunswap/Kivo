package tray

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestMenuUsesFixedActionsAndBusyGate(t *testing.T) {
	for _, busy := range []bool{false, true} {
		items := Menu(View{Title: "已启用", Node: "A&B\n香港", Connect: true, Disconnect: true, Check: true, TUNCanChange: true, Busy: busy})
		seen := map[Action]bool{}
		for _, item := range items {
			if item.Action != 0 {
				if seen[item.Action] {
					t.Fatal("duplicate", item.Action)
				}
				seen[item.Action] = true
				if item.Action != Show && item.Action != OpenWeb && item.Enabled == busy {
					t.Fatal("mutation busy gate", item)
				}
			}
			if strings.ContainsAny(item.Label, "\n&") {
				t.Fatal("unsafe menu label", item)
			}
		}
		if len(seen) != 7 || seen[Connect] || !seen[Disconnect] || !seen[EnableTUN] || seen[DisableTUN] {
			t.Fatal("missing command", seen)
		}
	}
	items := Menu(View{Title: "未启用", Connect: true})
	if items[4].Action != Connect || items[4].Label != "开启代理" || !items[4].Enabled {
		t.Fatal("off state did not show connect action", items[4])
	}
}

func TestMenuShowsExactlyOneTUNAction(t *testing.T) {
	for _, test := range []struct {
		enabled bool
		action  Action
		label   string
	}{
		{false, EnableTUN, "开启 TUN 模式"},
		{true, DisableTUN, "关闭 TUN 模式"},
	} {
		items := Menu(View{TUNEnabled: test.enabled, TUNCanChange: true})
		found := 0
		for _, item := range items {
			if item.Action != EnableTUN && item.Action != DisableTUN {
				continue
			}
			found++
			if item.Action != test.action || item.Label != test.label || !item.Enabled {
				t.Fatal("TUN menu state", item)
			}
		}
		if found != 1 {
			t.Fatal("TUN menu action count", found)
		}
	}
}
func TestIconHasStablePixelsAndDifferentStates(t *testing.T) {
	previous := []byte{}
	for _, tone := range []string{"idle", "active", "good", "error", "busy"} {
		data := IconPNG(tone)
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
			t.Fatal(img.Bounds())
		}
		if bytes.Equal(data, previous) {
			t.Fatal("duplicate state icon", tone)
		}
		previous = data
	}
}
func TestCleanPreservesUnicodeWithoutControlCharacters(t *testing.T) {
	if got := Clean("  日本🇯🇵 & A\nB  ", 5); got != "日本🇯🇵 …" {
		t.Fatal(got)
	}
	if got := Clean("", 10); got != "未选择" {
		t.Fatal(got)
	}
}
