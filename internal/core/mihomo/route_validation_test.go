package mihomo

import (
	"context"
	"os"
	"testing"

	"github.com/itrunswap/Kivo/internal/config"
)

// 可选实机预检：CI 无内核时跳过，本地指定绝对路径即可验证真正的 Mihomo -t。
func TestValidateRouteWithInstalledBinary(t *testing.T) {
	binary := os.Getenv("KIVO_TEST_MIHOMO_BINARY")
	if binary == "" {
		t.Skip("未指定 KIVO_TEST_MIHOMO_BINARY")
	}
	paths, err := config.ResolvePaths(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := config.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(cfg *config.Config) error { cfg.Mihomo.BinaryPath = binary; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := NewManager(store).ValidateConfig(context.Background(), store.Snapshot()); err != nil {
		t.Fatal(err)
	}
}
