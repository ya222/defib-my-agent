package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/ya222/defib-my-agent/internal/config"
)

// startFlags collects start's parsed flag values, before resolving the
// prompt text and building the daemon's createParams payload. Kept as a
// plain struct (rather than reading *cobra.Command directly) so
// buildCreateParams stays a pure, table-testable function.
type startFlags struct {
	Name           string
	Provider       string
	Mode           string
	Session        string
	Cwd            string
	Model          string
	ModelSet       bool
	Unattended     bool
	UnattendedSet  bool
	MaxAttempts    int
	MaxAttemptsSet bool
	Deadline       string
	DeadlineSet    bool
}

func newStartCmd(g *globalOptions) *cobra.Command {
	var (
		prompt      string
		promptFile  string
		provider    string
		mode        string
		session     string
		cwd         string
		name        string
		model       string
		unattended  bool
		maxAttempts int
		deadline    string
		detach      bool
		attach      bool
	)

	cmd := &cobra.Command{
		Use:   "start [flags] [-- provider passthrough args]",
		Short: "Create and start a task",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			flags := cmd.Flags()

			promptText, err := resolvePrompt(prompt, flags.Changed("prompt"), promptFile, flags.Changed("prompt-file"), cmd.InOrStdin())
			if err != nil {
				return err
			}

			resolvedCwd := cwd
			if resolvedCwd == "" {
				wd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve cwd: %w", err)
				}
				resolvedCwd = wd
			}

			opts := startFlags{
				Name:           name,
				Provider:       provider,
				Mode:           mode,
				Session:        session,
				Cwd:            resolvedCwd,
				Model:          model,
				ModelSet:       flags.Changed("model"),
				Unattended:     unattended,
				UnattendedSet:  flags.Changed("unattended"),
				MaxAttempts:    maxAttempts,
				MaxAttemptsSet: flags.Changed("max-attempts"),
				Deadline:       deadline,
				DeadlineSet:    flags.Changed("deadline"),
			}

			params, err := buildCreateParams(opts, promptText, passthroughArgs(cmd, args), func() (string, error) {
				return resolveDefaultProvider(g)
			})
			if err != nil {
				return err
			}

			// The warning must fire on ANY opt-in path — flag or config
			// file — so it is derived from the effective config, before
			// anything runs (docs/architecture.md#security-model).
			if unattendedEffective(g, params) {
				printUnattendedWarning(cmd.ErrOrStderr(), params.Provider)
			}

			client, err := connect(ctx, g)
			if err != nil {
				return err
			}
			var info taskInfo
			callErr := client.Call(ctx, "task.create", params, &info)
			_ = client.Close()
			if callErr != nil {
				return callErr
			}

			if g.jsonOut {
				if err := emitJSON(info); err != nil {
					return err
				}
			} else if !g.quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "started task %s (%s)\n", info.ID, info.Name)
			}

			// Per docs/cli.md, --attach is equivalent to --detach=false:
			// either opts into streaming until the task is terminal.
			if !attach && detach {
				return nil
			}
			return runAttach(ctx, g, info.ID, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}

	flags := cmd.Flags()
	flags.StringVarP(&prompt, "prompt", "p", "", "the instruction for the agent")
	flags.StringVar(&promptFile, "prompt-file", "", "read the prompt from a file (- for stdin)")
	flags.StringVar(&provider, "provider", "", "provider to use (default: config default_provider)")
	flags.StringVar(&mode, "mode", "", "execution mode: headless|interactive (default: config default_mode)")
	flags.StringVar(&session, "session", "new", "start a new session, or attach to an existing provider session ID")
	flags.StringVar(&cwd, "cwd", "", "working directory the provider runs in (default: current dir)")
	flags.StringVar(&name, "name", "", "human-friendly task name (default: short id)")
	flags.StringVar(&model, "model", "", "provider model override")
	flags.BoolVar(&unattended, "unattended", false, "opt into the provider's skip-approvals flag (dangerous)")
	flags.IntVar(&maxAttempts, "max-attempts", 0, "override attempt cap for this task")
	flags.StringVar(&deadline, "deadline", "", "override deadline cap for this task")
	flags.BoolVar(&detach, "detach", true, "return immediately after the task is registered")
	flags.BoolVar(&attach, "attach", false, "after creating, stream events/logs")

	return cmd
}

// resolvePrompt reads the prompt text from --prompt or --prompt-file
// (a path, or "-" for stdin), enforcing that the two flags are mutually
// exclusive.
func resolvePrompt(prompt string, promptSet bool, promptFile string, promptFileSet bool, stdin io.Reader) (string, error) {
	if promptSet && promptFileSet {
		return "", usageError{errors.New("--prompt and --prompt-file are mutually exclusive")}
	}
	if !promptFileSet {
		return prompt, nil
	}
	if promptFile == "-" {
		data, err := io.ReadAll(stdin)
		if err != nil {
			return "", fmt.Errorf("read prompt from stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(promptFile)
	if err != nil {
		return "", fmt.Errorf("read prompt file %s: %w", promptFile, err)
	}
	return string(data), nil
}

// passthroughArgs returns the args following "--", if any, for forwarding
// to the provider (docs/cli.md's passthrough separator convention).
func passthroughArgs(cmd *cobra.Command, args []string) []string {
	dash := cmd.ArgsLenAtDash()
	if dash < 0 {
		return nil
	}
	return args[dash:]
}

// buildCreateParams assembles the task.create payload from start's parsed
// flags. resolveDefaultProvider is called at most once, and only if an
// override needs a provider name that --provider did not supply.
func buildCreateParams(opts startFlags, promptText string, passthrough []string, resolveDefaultProvider func() (string, error)) (createParams, error) {
	params := createParams{
		Name:     opts.Name,
		Provider: opts.Provider,
		Mode:     opts.Mode,
		Cwd:      opts.Cwd,
		Prompt:   promptText,
		Args:     passthrough,
	}

	switch opts.Session {
	case "", "new":
		params.SessionMode = "new"
	default:
		params.SessionMode = "existing"
		params.SessionRef = opts.Session
	}

	if !opts.MaxAttemptsSet && !opts.DeadlineSet && !opts.ModelSet && !opts.UnattendedSet {
		return params, nil
	}

	overrides := map[string]string{}
	if opts.MaxAttemptsSet {
		overrides["retry.max_attempts"] = strconv.Itoa(opts.MaxAttempts)
	}
	if opts.DeadlineSet {
		overrides["retry.deadline"] = opts.Deadline
	}
	if opts.ModelSet || opts.UnattendedSet {
		providerName := opts.Provider
		if providerName == "" {
			var err error
			providerName, err = resolveDefaultProvider()
			if err != nil {
				return createParams{}, err
			}
		}
		if opts.ModelSet {
			overrides["providers."+providerName+".model"] = opts.Model
		}
		if opts.UnattendedSet {
			// The explicit value is forwarded, so --unattended=false can
			// override a config-file opt-in.
			overrides["providers."+providerName+".unattended"] = strconv.FormatBool(opts.Unattended)
		}
	}
	params.Overrides = overrides
	return params, nil
}

// unattendedEffective reports whether the task will run with approvals
// skipped, resolving the same config layers the daemon will (global file,
// project file for cwd, env, flag overrides) so a config-file opt-in warns
// exactly like --unattended does.
func unattendedEffective(g *globalOptions, params createParams) bool {
	cfgPath, err := globalConfigPath(g)
	if err != nil {
		return false
	}
	cfg, err := config.Resolve(config.Options{
		GlobalPath: cfgPath,
		WorkDir:    params.Cwd,
		Overrides:  params.Overrides,
	})
	if err != nil {
		return false
	}
	name := params.Provider
	if name == "" {
		name = cfg.DefaultProvider
	}
	return cfg.Providers[name].Unattended
}

// printUnattendedWarning is the prominent notice required by the security
// model whenever skip-approvals is in effect.
func printUnattendedWarning(w io.Writer, providerName string) {
	if providerName == "" {
		providerName = "the provider"
	}
	fmt.Fprintf(w, `WARNING: unattended mode is ON — %s will run with approval prompts skipped.
	The agent can execute arbitrary commands with no human in the loop.
	Run it in a sandbox or container you are prepared to lose.
`, providerName)
}

// resolveDefaultProvider resolves the client-side default provider (used
// only to build the "providers.<name>.*" override key when --provider was
// not given but --model/--unattended were).
func resolveDefaultProvider(g *globalOptions) (string, error) {
	cfgPath, err := globalConfigPath(g)
	if err != nil {
		return "", err
	}
	cfg, err := config.Resolve(config.Options{GlobalPath: cfgPath})
	if err != nil {
		return "", err
	}
	return cfg.DefaultProvider, nil
}
