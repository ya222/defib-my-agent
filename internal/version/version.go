// Package version holds build-time identifiers for the defib binary.
package version

// Version is the human-readable release version of defib. It defaults to a
// dev marker and is overridden at release build time via
// -ldflags "-X github.com/ya222/defib-my-agent/internal/version.Version=<tag>"
// (see .goreleaser.yaml).
var Version = "0.0.0-dev"

// SchemaVersion is the version of the on-disk SQLite schema. The store
// records this value in daemon_meta and refuses to open a database whose
// schema was written by a newer binary.
const SchemaVersion = 1
