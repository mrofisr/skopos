// Package version holds the build version shared by every Skopos mode.
package version

// Version is the build version. Release builds set it with
//
//	-ldflags "-X github.com/jawaracloud/skopos/internal/version.Version=<version>"
var Version = "dev"
