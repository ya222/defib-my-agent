package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
)

// ProjectFileName is the per-project config file discovered in the Task's
// working directory or its nearest ancestor.
const ProjectFileName = ".defib.toml"

// envPrefix prefixes every config-key environment variable. The path
// overrides DEFIB_CONFIG_DIR/DEFIB_STATE_DIR/DEFIB_RUNTIME_DIR belong to
// internal/paths and are never treated as config keys here.
const envPrefix = "DEFIB_"

// Options controls layered resolution. Zero values skip the corresponding
// layer.
type Options struct {
	// GlobalPath is the global config.toml; a missing file is fine.
	GlobalPath string
	// WorkDir is where the search for the nearest-ancestor .defib.toml
	// starts; empty skips project-config discovery.
	WorkDir string
	// Getenv supplies environment lookups; nil means os.Getenv.
	Getenv func(string) string
	// Overrides are explicit key-path -> raw-value settings (e.g. from CLI
	// flags). They win over every other layer. Unknown or non-scalar keys
	// are errors.
	Overrides map[string]string
}

// Resolve produces the effective Config by layering, lowest to highest:
// built-in defaults, the global file, the nearest-ancestor project file,
// DEFIB_* environment scalars, then explicit overrides, per
// docs/configuration.md#precedence-highest-wins.
func Resolve(opts Options) (Config, error) {
	cfg := Default()

	if opts.GlobalPath != "" {
		if err := mergeFile(&cfg, opts.GlobalPath); err != nil {
			return Config{}, err
		}
	}

	if opts.WorkDir != "" {
		project, err := findProjectFile(opts.WorkDir)
		if err != nil {
			return Config{}, err
		}
		if project != "" {
			if err := mergeFile(&cfg, project); err != nil {
				return Config{}, err
			}
		}
	}

	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	setters := scalarSetters(&cfg)
	for path, set := range setters {
		envVar := envPrefix + strings.ToUpper(strings.ReplaceAll(path, ".", "_"))
		if raw := getenv(envVar); raw != "" {
			if err := set(raw); err != nil {
				return Config{}, fmt.Errorf("environment %s (key %s): %w", envVar, path, err)
			}
		}
	}

	for path, raw := range opts.Overrides {
		set, ok := setters[path]
		if !ok {
			return Config{}, fmt.Errorf("override %s: unknown or non-scalar config key", path)
		}
		if err := set(raw); err != nil {
			return Config{}, fmt.Errorf("override %s: %w", path, err)
		}
	}

	return cfg, nil
}

// mergeFile unmarshals path's TOML over cfg, so keys absent from the file
// keep their current (lower-layer) values. A missing file is a no-op.
func mergeFile(cfg *Config, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if err := parseInto(cfg, data); err != nil {
		return fmt.Errorf("load config %s: %w", path, err)
	}
	return nil
}

// findProjectFile walks from dir to the filesystem root and returns the
// nearest .defib.toml, or "" if none exists.
func findProjectFile(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("resolve project config dir: %w", err)
	}
	for {
		candidate := filepath.Join(dir, ProjectFileName)
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %s: %w", candidate, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

// scalarSetters maps every scalar config key (dotted path, as documented)
// to a setter that parses a raw string into the field. Arrays and tables
// are intentionally absent: only scalars are settable via env/overrides.
func scalarSetters(cfg *Config) map[string]func(string) error {
	setters := make(map[string]func(string) error)
	root := reflect.ValueOf(cfg).Elem()
	collectStructSetters(setters, "", root)

	// Map entries are not addressable, so provider fields are set on a
	// copy that is written back to the map.
	entry := reflect.TypeOf(Provider{})
	for pname := range cfg.Providers {
		for i := 0; i < entry.NumField(); i++ {
			field := entry.Field(i)
			tag := tomlTag(field)
			if tag == "" || !isScalarKind(field.Type.Kind()) {
				continue
			}
			pname, idx := pname, i
			setters["providers."+pname+"."+tag] = func(raw string) error {
				p := cfg.Providers[pname]
				if err := setScalar(reflect.ValueOf(&p).Elem().Field(idx), raw); err != nil {
					return err
				}
				cfg.Providers[pname] = p
				return nil
			}
		}
	}
	return setters
}

// collectStructSetters recurses through addressable struct fields,
// registering setters for scalar leaves under their dotted toml-tag path.
func collectStructSetters(setters map[string]func(string) error, prefix string, v reflect.Value) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag := tomlTag(field)
		if tag == "" {
			continue
		}
		fv := v.Field(i)
		path := tag
		if prefix != "" {
			path = prefix + "." + tag
		}
		switch {
		case field.Type.Kind() == reflect.Struct:
			collectStructSetters(setters, path, fv)
		case isScalarKind(field.Type.Kind()):
			setters[path] = func(raw string) error {
				return setScalar(fv, raw)
			}
		}
	}
}

func tomlTag(field reflect.StructField) string {
	tag := field.Tag.Get("toml")
	if tag == "" || tag == "-" {
		return ""
	}
	return strings.Split(tag, ",")[0]
}

func isScalarKind(k reflect.Kind) bool {
	switch k {
	case reflect.String, reflect.Int, reflect.Float64, reflect.Bool:
		return true
	default:
		return false
	}
}

func setScalar(v reflect.Value, raw string) error {
	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Int:
		n, err := strconv.Atoi(raw)
		if err != nil {
			return fmt.Errorf("invalid integer %q: %w", raw, err)
		}
		v.SetInt(int64(n))
	case reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("invalid number %q: %w", raw, err)
		}
		v.SetFloat(f)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid boolean %q: %w", raw, err)
		}
		v.SetBool(b)
	default:
		return fmt.Errorf("unsupported scalar kind %s", v.Kind())
	}
	return nil
}
