// Package buildinfo exposes project identity and release values.
package buildinfo

const (
	// Name is the human-facing project name.
	Name = "Elgato Key Light Neo USB controller"
	// BinaryName is the installed command and release archive binary name.
	BinaryName = "elgatolight"
	// Description is the one-line project summary.
	Description = "Control Elgato Key Light Neo devices over Linux USB and bridge them to Home Assistant."
	// RepositoryURL identifies the canonical source repository.
	RepositoryURL = "https://git2.riper.fr/ztec/elgatocmd"
	// ReleaseAPI is the Forgejo endpoint used by self-update.
	ReleaseAPI = "https://git2.riper.fr/api/v1/repos/ztec/elgatocmd"
	// SigningKeyRegistryAPI is the central Forgejo trust registry shared by
	// projects generated from Tmplt.
	SigningKeyRegistryAPI = "https://git2.riper.fr/api/v1/repos/ztec/tmplt"
)

// Version is "dev" for local builds. Release builds replace it with the tag.
var Version = "dev"
