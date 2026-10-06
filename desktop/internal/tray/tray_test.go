package tray

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func TestMenuUsesFixedActionsAndBusyGate(t *testing.T) {
	for _, busy := range []bool{false, true} {
		items := Menu(View{Title: "仅内核运行", Node: "A&B\n香港", Connect: true, Disconnect: true, Check: true, Busy: busy})
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
		if len(seen) != 7 {
			t.Fatal("missing command", seen)
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
