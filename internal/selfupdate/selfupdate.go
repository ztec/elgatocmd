// Package selfupdate adapts the reusable updater to this project's identity.
package selfupdate

import (
	"context"

	"git2.riper.fr/ztec/elgatocmd/internal/buildinfo"
	shared "git2.riper.fr/ztec/tmplt/kit/selfupdate"
)

type Config = shared.Config
type Result = shared.Result

// Run applies the project binary identity to the reusable updater.
func Run(ctx context.Context, config Config) (Result, error) {
	config.BinaryName = buildinfo.BinaryName
	return shared.Run(ctx, config)
}

// CompletePendingReplacement completes the Windows update handoff.
func CompletePendingReplacement(targetPath string) error {
	return shared.CompletePendingReplacement(targetPath, buildinfo.BinaryName)
}

// CleanupStaleHelpers removes obsolete Windows update helpers.
func CleanupStaleHelpers() {
	shared.CleanupStaleHelpers(buildinfo.BinaryName)
}
