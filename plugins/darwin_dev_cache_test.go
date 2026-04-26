//go:build darwin

package plugins

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Jesssullivan/tinyland-cleanup/config"
)

func TestDarwinDeveloperCacheTargetsClassifyProtectedVersions(t *testing.T) {
	home := t.TempDir()
	cfg := config.DefaultConfig().DarwinDevCaches

	writeCacheFile(t, home, "Library/Caches/JetBrains/IntelliJIdea2024.3/cache.bin", "jetbrains")
	writeCacheFile(t, home, "Library/Caches/ms-playwright/chromium-1148/browser.bin", "old chromium")
	writeCacheFile(t, home, "Library/Caches/ms-playwright/chromium-1149/browser.bin", "new chromium")
	writeCacheFile(t, home, "Library/Caches/bazelisk/v1/bin/bazel", "v1")
	writeCacheFile(t, home, "Library/Caches/bazelisk/v2/bin/bazel", "v2")
	writeCacheFile(t, home, "Library/Caches/bazelisk/v3/bin/bazel", "v3")
	writeCacheFile(t, home, "Library/Caches/pip/http/cache.bin", "pip")

	now := time.Now()
	mustChtimes(t, filepath.Join(home, "Library/Caches/ms-playwright/chromium-1148"), now.Add(-2*time.Hour))
	mustChtimes(t, filepath.Join(home, "Library/Caches/ms-playwright/chromium-1149"), now)
	mustChtimes(t, filepath.Join(home, "Library/Caches/bazelisk/v1"), now.Add(-3*time.Hour))
	mustChtimes(t, filepath.Join(home, "Library/Caches/bazelisk/v2"), now.Add(-2*time.Hour))
	mustChtimes(t, filepath.Join(home, "Library/Caches/bazelisk/v3"), now)

	plugin := &CachePlugin{}
	targets := plugin.darwinDeveloperCacheTargets(home, cfg, map[string]bool{"goland": true})

	jetbrains := findCleanupTarget(t, targets, "jetbrains", "IntelliJIdea2024.3")
	if !jetbrains.Active || !jetbrains.Protected {
		t.Fatalf("expected active JetBrains cache to be protected: %#v", jetbrains)
	}

	oldChromium := findCleanupTarget(t, targets, "playwright", "chromium-1148")
	if oldChromium.Protected {
		t.Fatalf("expected old Playwright revision to be reviewable: %#v", oldChromium)
	}
	newChromium := findCleanupTarget(t, targets, "playwright", "chromium-1149")
	if !newChromium.Protected {
		t.Fatalf("expected newest Playwright revision to be protected: %#v", newChromium)
	}

	bazeliskV1 := findCleanupTarget(t, targets, "bazelisk", "v1")
	if bazeliskV1.Protected {
		t.Fatalf("expected oldest Bazelisk cache to be reviewable: %#v", bazeliskV1)
	}
	bazeliskV2 := findCleanupTarget(t, targets, "bazelisk", "v2")
	bazeliskV3 := findCleanupTarget(t, targets, "bazelisk", "v3")
	if !bazeliskV2.Protected || !bazeliskV3.Protected {
		t.Fatalf("expected two newest Bazelisk caches to be protected: v2=%#v v3=%#v", bazeliskV2, bazeliskV3)
	}

	pip := findCleanupTarget(t, targets, "pip", "pip")
	if pip.Bytes <= 0 {
		t.Fatalf("expected pip cache size to be measured: %#v", pip)
	}
}

func writeCacheFile(t *testing.T, home, relPath, content string) {
	t.Helper()

	path := filepath.Join(home, relPath)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustChtimes(t *testing.T, path string, modTime time.Time) {
	t.Helper()
	if err := os.Chtimes(path, modTime, modTime); err != nil {
		t.Fatal(err)
	}
}

func findCleanupTarget(t *testing.T, targets []CleanupTarget, targetType, name string) CleanupTarget {
	t.Helper()
	for _, target := range targets {
		if target.Type == targetType && target.Name == name {
			return target
		}
	}
	t.Fatalf("target %s/%s not found in %#v", targetType, name, targets)
	return CleanupTarget{}
}
