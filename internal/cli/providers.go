package cli

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ya222/defib-my-agent/internal/provider"
)

func newProvidersCmd(g *globalOptions, hooks Hooks) *cobra.Command {
	return &cobra.Command{
		Use:   "providers",
		Short: "List registered providers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var list []provider.Provider
			if hooks.Providers != nil {
				list = hooks.Providers()
			}
			if g.jsonOut {
				return emitJSON(providerInfoList(list))
			}
			renderProviders(cmd.OutOrStdout(), list)
			return nil
		},
	}
}

// providerCapabilities is the --json shape for one provider's capabilities.
type providerCapabilities struct {
	Resume           bool `json:"resume"`
	ClientSuppliedID bool `json:"client_supplied_id"`
	Headless         bool `json:"headless"`
	Interactive      bool `json:"interactive"`
}

// providerInfo is the --json shape for one `defib providers` entry.
type providerInfo struct {
	Name         string               `json:"name"`
	Capabilities providerCapabilities `json:"capabilities"`
}

func providerInfoList(list []provider.Provider) []providerInfo {
	out := make([]providerInfo, 0, len(list))
	for _, p := range list {
		c := p.Capabilities()
		out = append(out, providerInfo{
			Name: p.Name(),
			Capabilities: providerCapabilities{
				Resume:           c.Resume,
				ClientSuppliedID: c.ClientSuppliedID,
				Headless:         c.Headless,
				Interactive:      c.Interactive,
			},
		})
	}
	return out
}

// renderProviders prints the `defib providers` text table: NAME +
// CAPABILITIES (comma-joined set flags).
func renderProviders(w io.Writer, list []provider.Provider) {
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tCAPABILITIES")
	for _, p := range list {
		fmt.Fprintf(tw, "%s\t%s\n", p.Name(), capabilitiesString(p.Capabilities()))
	}
	_ = tw.Flush()
}

func capabilitiesString(c provider.Capabilities) string {
	var flags []string
	if c.Resume {
		flags = append(flags, "resume")
	}
	if c.ClientSuppliedID {
		flags = append(flags, "client-supplied-id")
	}
	if c.Headless {
		flags = append(flags, "headless")
	}
	if c.Interactive {
		flags = append(flags, "interactive")
	}
	return strings.Join(flags, ",")
}
