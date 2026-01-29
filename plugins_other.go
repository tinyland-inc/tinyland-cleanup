//go:build !darwin

package main

import "gitlab.com/tinyland/lab/tinyland-cleanup/plugins"

func registerDarwinPlugins(registry *plugins.Registry) {
	// Darwin-specific plugins are not available on other platforms
	// This is a no-op to satisfy the function signature
	_ = registry
}
