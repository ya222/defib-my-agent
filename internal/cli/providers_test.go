package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ya222/defib-my-agent/internal/provider"
	"github.com/ya222/defib-my-agent/internal/provider/fake"
)

func TestCapabilitiesString(t *testing.T) {
	tests := []struct {
		name string
		c    provider.Capabilities
		want string
	}{
		{name: "none set", c: provider.Capabilities{}, want: ""},
		{name: "all set", c: provider.Capabilities{Resume: true, ClientSuppliedID: true, Headless: true, Interactive: true},
			want: "resume,client-supplied-id,headless,interactive"},
		{name: "fake provider's actual capabilities", c: fake.New().Capabilities(), want: "resume,client-supplied-id,headless,interactive"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, capabilitiesString(tt.c))
		})
	}
}

func TestRenderProviders(t *testing.T) {
	var buf bytes.Buffer
	renderProviders(&buf, []provider.Provider{fake.New()})
	out := buf.String()
	assert.Contains(t, out, "NAME")
	assert.Contains(t, out, "CAPABILITIES")
	assert.Contains(t, out, "fake")
	assert.Contains(t, out, "resume,client-supplied-id,headless")
}

func TestProviderInfoList(t *testing.T) {
	list := providerInfoList([]provider.Provider{fake.New()})
	require.Len(t, list, 1)
	assert.Equal(t, "fake", list[0].Name)
	assert.Equal(t, providerCapabilities{Resume: true, ClientSuppliedID: true, Headless: true, Interactive: true}, list[0].Capabilities)
}
