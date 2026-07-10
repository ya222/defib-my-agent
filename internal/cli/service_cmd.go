package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ya222/defib-my-agent/internal/service"
)

// newInstallServiceCmd builds `defib install-service`, which writes and
// enables the per-user systemd/launchd service that runs `defib daemon run`
// on login/boot. It talks to internal/service directly (no IPC) so the
// service can be installed even when no daemon is running.
func newInstallServiceCmd(g *globalOptions) *cobra.Command {
	start := false
	cmd := &cobra.Command{
		Use:   "install-service",
		Short: "Install the per-user service that runs the daemon on login/boot",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve executable: %w", err)
			}
			res, err := service.Install(cmd.Context(), service.Options{ExecPath: exe, Start: start})
			if err != nil {
				return err
			}
			if !g.quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "installed %s service: %s\n", res.Manager, res.Path)
				for _, a := range res.Actions {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", a)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&start, "start", false, "start the service immediately after installing")
	return cmd
}

// newUninstallServiceCmd builds `defib uninstall-service`, which removes the
// per-user service installed by `defib install-service`.
func newUninstallServiceCmd(g *globalOptions) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "uninstall-service",
		Short: "Uninstall the per-user daemon service",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			exe, err := os.Executable()
			if err != nil {
				return fmt.Errorf("resolve executable: %w", err)
			}
			res, err := service.Uninstall(cmd.Context(), service.Options{ExecPath: exe})
			if err != nil {
				return err
			}
			if !g.quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "removed %s service: %s\n", res.Manager, res.Path)
				for _, a := range res.Actions {
					fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", a)
				}
			}
			return nil
		},
	}
	return cmd
}
