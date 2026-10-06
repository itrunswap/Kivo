package app

import (
	"context"
	"errors"
	"testing"

	"github.com/itrunswap/Kivo/internal/config"
)

func TestSettingsPatchPreservesOmittedFieldsAndRejectsStaleDraft(t *testing.T) {
	paths, _ := config.ResolvePaths(t.TempDir())
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Update(func(c *config.Config) error {
		c.Mihomo.MixedPort = 17895
		c.Mihomo.AllowLAN = true
		c.Mihomo.DownloadProxy = "http://127.0.0.1:12345"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	service := NewService(store, &fakeCore{})
	before := service.GetSettings()
	mode := "global"
	if err = service.PatchSettings(context.Background(), SettingsPatch{Mode: &mode}); err != nil {
		t.Fatal(err)
	}
	after := service.GetSettings()
	if after.Mode != "global" || after.MixedPort != before.MixedPort || !after.AllowLAN || after.DownloadProxy != before.DownloadProxy || after.Revision == before.Revision {
		t.Fatalf("invalid merge: %#v", after)
	}
	off, empty := false, ""
	if err = service.PatchSettings(context.Background(), SettingsPatch{Revision: before.Revision, AllowLAN: &off}); !errors.Is(err, ErrSettingsConflict) {
		t.Fatal("stale draft accepted", err)
	}
	if !service.GetSettings().AllowLAN {
		t.Fatal("conflict modified settings")
	}
	if err = service.PatchSettings(context.Background(), SettingsPatch{Revision: after.Revision, AllowLAN: &off, DownloadProxy: &empty}); err != nil {
		t.Fatal(err)
	}
	if got := service.GetSettings(); got.AllowLAN || got.DownloadProxy != "" || got.Mode != "global" {
		t.Fatalf("explicit empty/false ignored: %#v", got)
	}
	zero := 0
	saved := service.GetSettings().Revision
	if err = service.PatchSettings(context.Background(), SettingsPatch{DownloadRetry: &zero}); err == nil {
		t.Fatal("invalid retry accepted")
	}
	if service.GetSettings().Revision != saved {
		t.Fatal("invalid patch changed config")
	}
}
