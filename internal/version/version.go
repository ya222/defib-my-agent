// Package version holds build-time identifiers for the defib binary.
package version

import (
	"runtime/debug"
	"strings"
)

// devVersion marks a build with no release version: a plain `go build` or a
// local checkout.
const devVersion = "0.0.0-dev"

// Version is the human-readable release version of defib.
//
// Release builds override it via
// -ldflags "-X github.com/ya222/defib-my-agent/internal/version.Version=<tag>"
// (see .goreleaser.yaml). A `go install <module>@<tag>` build does not apply
// those ldflags, so init() fills Version in from the module version the Go
// toolchain records in the binary. Plain local builds keep the dev marker.
//
// It must stay initialized to a constant string so the -X ldflag takes effect.
var Version = devVersion

func init() {
	if Version != devVersion {
		return // a release build already injected the version via ldflags
	}
	if v := normalizeBuildVersion(moduleVersion()); v != "" {
		Version = v
	}
}

// moduleVersion returns the main module's version as embedded by the Go
// toolchain (e.g. "v1.0.2" for `go install ...@v1.0.2`), or "" if build info
// is unavailable.
func moduleVersion() string {
	if bi, ok := debug.ReadBuildInfo(); ok {
		return bi.Main.Version
	}
	return ""
}

// normalizeBuildVersion maps the toolchain's embedded module version to a
// release string matching the ldflag form (no leading "v"). It discards values
// that carry no release information: "" and the "(devel)" placeholder the
// toolchain uses for untagged builds.
func normalizeBuildVersion(v string) string {
	if v == "(devel)" {
		return ""
	}
	return strings.TrimPrefix(v, "v")
}

// SchemaVersion is the version of the on-disk SQLite schema. The store
// records this value in daemon_meta and refuses to open a database whose
// schema was written by a newer binary.
const SchemaVersion = 1
