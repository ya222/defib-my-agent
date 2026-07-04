// Package version holds build-time identifiers for the defib binary.
package version

const (
	// Version is the human-readable release version of defib.
	Version = "0.0.0-dev"

	// SchemaVersion is the version of the on-disk SQLite schema. The store
	// records this value in daemon_meta and refuses to open a database whose
	// schema was written by a newer binary.
	SchemaVersion = 1
)
