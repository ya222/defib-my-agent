package cli

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateLogStream(t *testing.T) {
	for _, ok := range []string{"stdout", "stderr", "both"} {
		t.Run(ok, func(t *testing.T) {
			assert.NoError(t, validateLogStream(ok))
		})
	}

	t.Run("invalid", func(t *testing.T) {
		err := validateLogStream("bogus")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "bogus")
		var ue usageError
		assert.True(t, errors.As(err, &ue))
	})
}
