package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/spf13/cobra"

	"github.com/ya222/defib/internal/config"
	"github.com/ya222/defib/internal/ipc"
	"github.com/ya222/defib/internal/paths"
)

// doctorCheck is one line of `defib doctor` output/--json entry.
type doctorCheck struct {
	Check  string `json:"check"`
	Status string `json:"status"` // ok | warn | fail
	Detail string `json:"detail,omitempty"`
}

func newDoctorCmd(g *globalOptions, hooks Hooks) *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Environment diagnostics",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			checks := runDoctorChecks(cmd.Context(), g, hooks)
			if g.jsonOut {
				return emitJSON(checks)
			}
			renderDoctor(cmd.OutOrStdout(), checks)
			for _, c := range checks {
				if c.Status == "fail" {
					return errors.New("doctor found failing checks")
				}
			}
			return nil
		},
	}
}

// renderDoctor prints one `status: detail` line per check.
func renderDoctor(w io.Writer, checks []doctorCheck) {
	for _, c := range checks {
		fmt.Fprintf(w, "%s: %s\n", c.Status, c.Detail)
	}
}

// runDoctorChecks performs the M8-T4 checks documented in docs/cli.md's
// `defib doctor` entry: dirs, config, providers, daemon. (M15-T2 extends
// this with service-install and version checks.)
func runDoctorChecks(ctx context.Context, g *globalOptions, hooks Hooks) []doctorCheck {
	var checks []doctorCheck

	dirs, err := paths.Resolve()
	if err != nil {
		checks = append(checks, doctorCheck{Check: "dirs", Status: "fail", Detail: err.Error()})
	} else {
		for _, d := range []struct{ name, path string }{
			{"config", dirs.Config}, {"state", dirs.State}, {"runtime", dirs.Runtime},
		} {
			checks = append(checks, checkDir(d.name, d.path))
		}
	}

	var cfg config.Config
	globalPath, err := globalConfigPath(g)
	if err != nil {
		checks = append(checks, doctorCheck{Check: "config", Status: "fail", Detail: err.Error()})
	} else if cfg, err = config.Resolve(config.Options{GlobalPath: globalPath}); err != nil {
		checks = append(checks, doctorCheck{Check: "config", Status: "fail", Detail: err.Error()})
	} else {
		warnings, verr := config.Validate(cfg)
		if verr != nil {
			checks = append(checks, doctorCheck{Check: "config", Status: "fail", Detail: verr.Error()})
		} else {
			checks = append(checks, doctorCheck{Check: "config", Status: "ok", Detail: "valid"})
		}
		for _, w := range warnings {
			checks = append(checks, doctorCheck{Check: "config", Status: "warn", Detail: w})
		}
	}

	if hooks.Providers != nil {
		for _, p := range hooks.Providers() {
			checks = append(checks, checkProvider(cfg, p.Name()))
		}
	}

	checks = append(checks, checkDaemon(ctx))

	return checks
}

func checkDir(name, dir string) doctorCheck {
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return doctorCheck{Check: "dirs:" + name, Status: "warn", Detail: fmt.Sprintf("%s missing; run any defib command to create it", dir)}
		}
		return doctorCheck{Check: "dirs:" + name, Status: "fail", Detail: err.Error()}
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		return doctorCheck{Check: "dirs:" + name, Status: "warn", Detail: fmt.Sprintf("%s has permissions %04o, want 0700", dir, perm)}
	}
	return doctorCheck{Check: "dirs:" + name, Status: "ok", Detail: dir}
}

func checkProvider(cfg config.Config, name string) doctorCheck {
	if name == "fake" {
		return doctorCheck{Check: "providers:" + name, Status: "ok", Detail: "fake (built-in)"}
	}
	binary := name
	if p, ok := cfg.Providers[name]; ok && p.Binary != "" {
		binary = p.Binary
	}
	if _, err := exec.LookPath(binary); err != nil {
		return doctorCheck{Check: "providers:" + name, Status: "warn", Detail: fmt.Sprintf("%s: binary %q not found on PATH", name, binary)}
	}
	return doctorCheck{Check: "providers:" + name, Status: "ok", Detail: fmt.Sprintf("%s (%s)", name, binary)}
}

func checkDaemon(ctx context.Context) doctorCheck {
	sock, err := socketPath()
	if err != nil {
		return doctorCheck{Check: "daemon", Status: "fail", Detail: err.Error()}
	}
	client, err := ipc.Dial(sock)
	if err != nil {
		return doctorCheck{Check: "daemon", Status: "warn", Detail: "daemon not running (will auto-start on demand)"}
	}
	defer client.Close()
	var ping pingResult
	if err := client.Call(ctx, "daemon.ping", nil, &ping); err != nil {
		return doctorCheck{Check: "daemon", Status: "warn", Detail: "daemon not running (will auto-start on demand)"}
	}
	return doctorCheck{Check: "daemon", Status: "ok", Detail: fmt.Sprintf("daemon running (pid %d, version %s)", ping.PID, ping.Version)}
}
