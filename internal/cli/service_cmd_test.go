package cli

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestServiceCommandsRegistered checks the install-service/uninstall-service
// commands are wired up with the expected shape, without executing their
// RunE (which would touch the real system via internal/service).
func TestServiceCommandsRegistered(t *testing.T) {
	g := &globalOptions{}

	install := newInstallServiceCmd(g)
	assert.Equal(t, "install-service", install.Use)
	assert.NotNil(t, install.Flags().Lookup("start"))

	uninstall := newUninstallServiceCmd(g)
	assert.Equal(t, "uninstall-service", uninstall.Use)
}
