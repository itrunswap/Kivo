package main

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/itrunswap/Kivo/internal/server"
)

func TestDesktopAssets(t *testing.T) {
	primary, err := fs.Sub(frontend, "frontend")
	if err != nil {
		t.Fatal(err)
	}
	assets := desktopAssets{primary: primary, shared: server.EmbeddedAssets()}
	for _, name := range []string{"index.html", "app.js", "app.css", "model.mjs", "fonts/noto-sans-sc-ui.woff2", "fonts/OFL.txt"} {
		if _, err := fs.ReadFile(assets, name); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	font, _ := fs.ReadFile(assets, "fonts/noto-sans-sc-ui.woff2")
	if string(font[:4]) != "wOF2" {
		t.Fatal("not WOFF2")
	}
	if _, err := fs.ReadFile(assets, "../../config.json"); err == nil {
		t.Fatal("accepted traversal")
	}
	if _, err := fs.ReadFile(assets, "missing.js"); err == nil {
		t.Fatal("missing asset fell back to Web")
	}
	index, _ := fs.ReadFile(assets, "index.html")
	if strings.Contains(string(index), "preview-bridge.js") {
		t.Fatal("development mock entered production")
	}
}

func TestOperationsAccountForCloseGuard(t *testing.T) {
	d := &Desktop{}
	done := d.beginOperation()
	if d.inflight != 1 {
		t.Fatal(d.inflight)
	}
	done()
	if d.inflight != 0 {
		t.Fatal(d.inflight)
	}
}
