//go:build darwin

package main

import "gitlab.com/tinyland/lab/tinyland-cleanup/plugins"

func registerDarwinPlugins(registry *plugins.Registry) {
	registry.Register(plugins.NewHomebrewPlugin())
	registry.Register(plugins.NewIOSSimulatorPlugin())
	registry.Register(plugins.NewXcodePlugin())
}
