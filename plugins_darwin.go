//go:build darwin

package main

import "gitlab.com/tinyland/lab/tinyland-cleanup/plugins"

func registerDarwinPlugins(registry *plugins.Registry) {
	registry.Register(plugins.NewHomebrewPlugin())
	registry.Register(plugins.NewIOSSimulatorPlugin())
	registry.Register(plugins.NewXcodePlugin())
	registry.Register(plugins.NewICloudPlugin())
	registry.Register(plugins.NewPhotosPlugin())
	registry.Register(plugins.NewLimaPlugin())
	registry.Register(plugins.NewAPFSPlugin())
}
