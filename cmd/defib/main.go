// Command defib is the entry point for the defib CLI. It wires the cobra
// command tree; all business logic lives in internal packages.
package main

import (
	"fmt"

	"github.com/ya222/defib/internal/version"
)

func main() {
	fmt.Printf("defib version %s (schema %d)\n", version.Version, version.SchemaVersion)
}
