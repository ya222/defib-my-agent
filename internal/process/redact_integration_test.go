package process

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib/internal/logging"
)

// Attempt logs are the redactor's Writer wrapped over the log file; child
// output must land redacted, everything else verbatim.
func TestCaptureThroughRedactor(t *testing.T) {
	redactor := logging.NewRedactor([]string{"MY_API_TOKEN=hunter2secret"})
	stdout, stderr := newSyncBuffer(), newSyncBuffer()

	p, err := Start(context.Background(), Spec{
		Argv: []string{script(t, `
printf 'token is hunter2secret ok\n'
printf 'key sk-abcdef1234567890 here\n'
printf 'plain line\n'
`)},
		Stdout: redactor.Writer(stdout),
		Stderr: redactor.Writer(stderr),
	})
	require.NoError(t, err)

	code, err := p.Wait()
	require.NoError(t, err)
	require.Zero(t, code)

	assert.Equal(t,
		"token is [REDACTED] ok\nkey [REDACTED] here\nplain line\n",
		stdout.String())
}
