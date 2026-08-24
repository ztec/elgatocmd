// Package buildinfo exposes values injected into release binaries at link time.
package buildinfo

// Version is "dev" for local builds. Release builds replace it through
// -ldflags -X with the exact MAJOR.MINOR tag.
var Version = "dev"
