// Package config loads and writes the CLI's TOML config (cli-spec-001-v1.1.md
// §Configuration). Defaults match the spec.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

// Config is the CLI configuration loaded from TOML + env + flags.
type Config struct {
	CLI        CLISection       `mapstructure:"cli"`
	Transports []Transport      `mapstructure:"transport"`
}

// CLISection is the [cli] table.
type CLISection struct {
	DefaultFIF       string `mapstructure:"default_fif"`
	DefaultTransport string `mapstructure:"default_transport"`
	QueueDB          string `mapstructure:"queue_db"`
	LogLevel         string `mapstructure:"log_level"`
}

// Transport is one [[transport]] entry.
type Transport struct {
	Tag        string `mapstructure:"tag"`
	Type       string `mapstructure:"type"`
	URL        string `mapstructure:"url"`
	Preference int    `mapstructure:"preference"`
	// HubDataDir is a CLI-side hint for the on-disk path of the Hub's
	// data dir, when the CLI runs on the same host as the Hub. The
	// `conformance` command uses it to read hub-identity.json for the
	// macaroon root key. Optional; only meaningful for type="hub".
	HubDataDir string `mapstructure:"hub_data_dir"`
}

// Default returns a Config populated with the spec defaults.
func Default() *Config {
	return &Config{
		CLI: CLISection{
			DefaultFIF:       defaultFIFPath(),
			DefaultTransport: "hub",
			QueueDB:          defaultQueuePath(),
			LogLevel:         "info",
		},
		Transports: []Transport{},
	}
}

// Load reads the CLI config from the given path (or the spec's lookup chain
// if path is empty: $NAKLI_CONFIG, then ~/.config/nakli-cli/config.toml).
// A missing file returns Default() without error.
func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigType("toml")
	v.SetEnvPrefix("NAKLI")
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if path == "" {
		if envPath := os.Getenv("NAKLI_CONFIG"); envPath != "" {
			path = envPath
		} else {
			path = DefaultConfigPath()
		}
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Default(), nil
	} else if err != nil {
		return nil, fmt.Errorf("config.Load: stat %s: %w", path, err)
	}
	v.SetConfigFile(path)
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("config.Load: read %s: %w", path, err)
	}
	c := Default()
	if err := v.Unmarshal(c); err != nil {
		return nil, fmt.Errorf("config.Load: unmarshal: %w", err)
	}
	return c, nil
}

// Save writes c to path as TOML. Creates parent directories with 0o700.
func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config.Save: mkdir: %w", err)
	}
	v := viper.New()
	v.SetConfigType("toml")
	v.Set("cli", map[string]any{
		"default_fif":       c.CLI.DefaultFIF,
		"default_transport": c.CLI.DefaultTransport,
		"queue_db":          c.CLI.QueueDB,
		"log_level":         c.CLI.LogLevel,
	})
	transports := make([]map[string]any, 0, len(c.Transports))
	for _, t := range c.Transports {
		e := map[string]any{
			"tag":        t.Tag,
			"type":       t.Type,
			"url":        t.URL,
			"preference": t.Preference,
		}
		if t.HubDataDir != "" {
			e["hub_data_dir"] = t.HubDataDir
		}
		transports = append(transports, e)
	}
	v.Set("transport", transports)
	if err := v.WriteConfigAs(path); err != nil {
		return fmt.Errorf("config.Save: write %s: %w", path, err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("config.Save: chmod: %w", err)
	}
	return nil
}

// TransportByTag returns the transport matching tag, or "" if absent.
func (c *Config) TransportByTag(tag string) *Transport {
	for i := range c.Transports {
		if c.Transports[i].Tag == tag {
			return &c.Transports[i]
		}
	}
	return nil
}

// PreferredTransport returns the transport matching --transport flag, the
// default_transport from config, or — if neither is set — the first transport
// in the list.
func (c *Config) PreferredTransport(flagTag string) (*Transport, error) {
	tag := flagTag
	if tag == "" {
		tag = c.CLI.DefaultTransport
	}
	if tag != "" {
		if t := c.TransportByTag(tag); t != nil {
			return t, nil
		}
		return nil, fmt.Errorf("transport tag %q not found in config", tag)
	}
	if len(c.Transports) == 0 {
		return nil, fmt.Errorf("no transports configured; run `nakli-cli transport add` first")
	}
	return &c.Transports[0], nil
}

// DefaultConfigPath returns ~/.config/nakli-cli/config.toml.
func DefaultConfigPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "nakli-cli", "config.toml")
	}
	return "./nakli-cli-config.toml"
}

func defaultFIFPath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config", "nakli-cli", "identity.fif")
	}
	return "./identity.fif"
}

func defaultQueuePath() string {
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "state", "nakli-cli", "queue.db")
	}
	return "./queue.db"
}
