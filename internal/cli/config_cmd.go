package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/spf13/cobra"

	"github.com/ya222/defib/internal/config"
)

func newConfigCmd(g *globalOptions) *cobra.Command {
	cmd := &cobra.Command{Use: "config", Short: "Inspect and validate configuration"}
	cmd.AddCommand(newConfigPathCmd(g))
	cmd.AddCommand(newConfigShowCmd(g))
	cmd.AddCommand(newConfigValidateCmd(g))
	cmd.AddCommand(newConfigGetCmd(g))
	cmd.AddCommand(newConfigSetCmd(g))
	return cmd
}

func newConfigPathCmd(g *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print resolved config file locations",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			globalPath, err := globalConfigPath(g)
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve cwd: %w", err)
			}
			projectPath, err := config.FindProjectFile(cwd)
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(map[string]string{"global": globalPath, "project": projectPath})
			}
			project := projectPath
			if project == "" {
				project = "(none)"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "global: %s\n", globalPath)
			fmt.Fprintf(cmd.OutOrStdout(), "project: %s\n", project)
			return nil
		},
	}
}

func newConfigShowCmd(g *globalOptions) *cobra.Command {
	var effective bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the merged configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			globalPath, err := globalConfigPath(g)
			if err != nil {
				return err
			}
			opts := config.Options{GlobalPath: globalPath}
			if effective {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve cwd: %w", err)
				}
				opts.WorkDir = cwd
			}
			cfg, err := config.Resolve(opts)
			if err != nil {
				return err
			}
			if g.jsonOut {
				return emitJSON(cfg)
			}
			data, err := toml.Marshal(cfg)
			if err != nil {
				return fmt.Errorf("marshal config: %w", err)
			}
			_, err = cmd.OutOrStdout().Write(data)
			return err
		},
	}
	cmd.Flags().BoolVar(&effective, "effective", false, "resolve project config for the current directory")
	return cmd
}

func newConfigValidateCmd(g *globalOptions) *cobra.Command {
	var configFile string
	cmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			chosen := configFile
			if chosen == "" {
				globalPath, err := globalConfigPath(g)
				if err != nil {
					return err
				}
				chosen = globalPath
			}
			cfg, err := config.Resolve(config.Options{GlobalPath: chosen})
			if err != nil {
				return err
			}
			warnings, err := config.Validate(cfg)
			if err != nil {
				return err
			}

			if g.jsonOut {
				return emitJSON(map[string]any{"valid": true, "warnings": warnings})
			}
			for _, w := range warnings {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: %s\n", w)
			}
			if !g.quiet {
				fmt.Fprintln(cmd.OutOrStdout(), "config valid")
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&configFile, "config", "", "validate a specific config file instead of the resolved global one")
	return cmd
}

func newConfigGetCmd(g *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "get <key>",
		Short: "Print a single effective scalar config value",
		Args:  exactArgs(1, "config get <key>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			globalPath, err := globalConfigPath(g)
			if err != nil {
				return err
			}
			cwd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("resolve cwd: %w", err)
			}
			cfg, err := config.Resolve(config.Options{GlobalPath: globalPath, WorkDir: cwd})
			if err != nil {
				return err
			}
			value, err := config.GetScalar(&cfg, args[0])
			if err != nil {
				return usageError{err}
			}
			if g.jsonOut {
				return emitJSON(map[string]string{"key": args[0], "value": value})
			}
			fmt.Fprintln(cmd.OutOrStdout(), value)
			return nil
		},
	}
}

func newConfigSetCmd(g *globalOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "set <key> <value>",
		Short: "Write a config value to the global config file",
		Args:  exactArgs(2, "config set <key> <value>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			key, value := args[0], args[1]
			globalPath, err := globalConfigPath(g)
			if err != nil {
				return err
			}
			// Validate the key/value the same way the daemon would apply
			// it, before touching the file on disk.
			if _, err := config.Resolve(config.Options{GlobalPath: globalPath, Overrides: map[string]string{key: value}}); err != nil {
				return usageError{err}
			}
			if err := writeConfigValue(globalPath, key, value); err != nil {
				return err
			}
			if !g.quiet {
				fmt.Fprintf(cmd.OutOrStdout(), "set %s = %s\n", key, value)
			}
			return nil
		},
	}
}

// writeConfigValue merges key=value into the global config file's existing
// raw TOML content (not the fully-materialized defaults) and writes it back
// atomically (temp file + rename, 0600), creating the config directory
// (0700) first if needed.
func writeConfigValue(path, key, value string) error {
	raw := map[string]any{}
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := toml.Unmarshal(data, &raw); err != nil {
			return fmt.Errorf("parse config %s: %w", path, err)
		}
	case os.IsNotExist(err):
		// No file yet: start from an empty document.
	default:
		return fmt.Errorf("read config %s: %w", path, err)
	}

	scalar, err := coerceScalar(key, value)
	if err != nil {
		return err
	}
	setDottedValue(raw, strings.Split(key, "."), scalar)

	out, err := toml.Marshal(raw)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o600); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("write config %s: %w", path, err)
	}
	return nil
}

// setDottedValue sets a value at a dotted path within a nested
// map[string]any, creating intermediate maps as needed.
func setDottedValue(m map[string]any, parts []string, value any) {
	if len(parts) == 1 {
		m[parts[0]] = value
		return
	}
	next, ok := m[parts[0]].(map[string]any)
	if !ok {
		next = map[string]any{}
		m[parts[0]] = next
	}
	setDottedValue(next, parts[1:], value)
}

// coerceScalar parses value into the Go type the config schema declares for
// key (bool/int/float64/string), so the TOML file gets a properly typed
// scalar rather than always a string. The key has already been validated by
// config.Resolve's Overrides path by the time this runs.
func coerceScalar(key, value string) (any, error) {
	kind, ok := configFieldKind(key)
	if !ok {
		return value, nil
	}
	switch kind {
	case reflect.Bool:
		b, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("invalid boolean %q: %w", value, err)
		}
		return b, nil
	case reflect.Int:
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("invalid integer %q: %w", value, err)
		}
		return n, nil
	case reflect.Float64:
		f, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid number %q: %w", value, err)
		}
		return f, nil
	default:
		return value, nil
	}
}

// configFieldKind returns the reflect.Kind of the scalar config field named
// by a dotted key (as accepted by config.Resolve's Overrides / config.
// GetScalar). It walks config.Config/config.Provider's exported fields via
// their toml tags directly, independent of any particular provider already
// existing in a resolved Config value.
func configFieldKind(key string) (reflect.Kind, bool) {
	if rest, ok := strings.CutPrefix(key, "providers."); ok {
		parts := strings.SplitN(rest, ".", 2)
		if len(parts) != 2 {
			return 0, false
		}
		return structFieldKind(reflect.TypeOf(config.Provider{}), parts[1])
	}
	return structFieldKindPath(reflect.TypeOf(config.Config{}), strings.Split(key, "."))
}

func structFieldKindPath(t reflect.Type, parts []string) (reflect.Kind, bool) {
	if len(parts) == 0 || t.Kind() != reflect.Struct {
		return 0, false
	}
	kind, ok := structFieldKind(t, parts[0])
	if !ok {
		return 0, false
	}
	if len(parts) == 1 {
		return kind, true
	}
	if kind != reflect.Struct {
		return 0, false
	}
	field, _ := fieldByTag(t, parts[0])
	return structFieldKindPath(field.Type, parts[1:])
}

func structFieldKind(t reflect.Type, name string) (reflect.Kind, bool) {
	field, ok := fieldByTag(t, name)
	if !ok {
		return 0, false
	}
	return field.Type.Kind(), true
}

func fieldByTag(t reflect.Type, tagName string) (reflect.StructField, bool) {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := strings.Split(f.Tag.Get("toml"), ",")[0]
		if tag == tagName {
			return f, true
		}
	}
	return reflect.StructField{}, false
}
