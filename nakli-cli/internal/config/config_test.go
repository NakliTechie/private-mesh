package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")

	c := Default()
	c.CLI.DefaultFIF = "/x/y/identity.fif"
	c.CLI.DefaultTransport = "hub"
	c.CLI.LogLevel = "debug"
	c.Transports = []Transport{
		{Tag: "hub", Type: "hub", URL: "http://h.example", Preference: 1, HubDataDir: "/var/hub"},
		{Tag: "cf", Type: "cf-worker", URL: "https://cf.example", Preference: 2},
	}
	if err := c.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.CLI.DefaultFIF != c.CLI.DefaultFIF {
		t.Errorf("DefaultFIF: %q vs %q", got.CLI.DefaultFIF, c.CLI.DefaultFIF)
	}
	if got.CLI.LogLevel != c.CLI.LogLevel {
		t.Errorf("LogLevel: %q vs %q", got.CLI.LogLevel, c.CLI.LogLevel)
	}
	if len(got.Transports) != 2 {
		t.Fatalf("Transports: got %d want 2", len(got.Transports))
	}
	if got.Transports[0].Tag != "hub" {
		t.Errorf("Transports[0].Tag: %q want hub", got.Transports[0].Tag)
	}
	if got.Transports[0].HubDataDir != "/var/hub" {
		t.Errorf("Transports[0].HubDataDir: %q", got.Transports[0].HubDataDir)
	}
}

func TestLoadMissingReturnsDefault(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "absent.toml"))
	if err != nil {
		t.Fatalf("expected no error for missing file; got %v", err)
	}
	if c.CLI.DefaultTransport != "hub" {
		t.Errorf("default transport: got %q want hub", c.CLI.DefaultTransport)
	}
}

func TestTransportByTagAndPreferred(t *testing.T) {
	c := Default()
	c.Transports = []Transport{
		{Tag: "a", Type: "hub"},
		{Tag: "b", Type: "hub"},
	}
	if got := c.TransportByTag("b"); got == nil || got.Tag != "b" {
		t.Errorf("TransportByTag(b): got %+v", got)
	}
	if got := c.TransportByTag("zzz"); got != nil {
		t.Errorf("TransportByTag(zzz): expected nil, got %+v", got)
	}
	c.CLI.DefaultTransport = "b"
	p, err := c.PreferredTransport("")
	if err != nil || p.Tag != "b" {
		t.Errorf("PreferredTransport: %+v %v", p, err)
	}
	p, err = c.PreferredTransport("a")
	if err != nil || p.Tag != "a" {
		t.Errorf("PreferredTransport(flag=a): %+v %v", p, err)
	}
	if _, err := c.PreferredTransport("missing"); err == nil {
		t.Errorf("PreferredTransport(missing): expected error")
	}
}

func TestSaveSetsTightPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := Default().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := info.Mode().Perm()
	if mode != 0o600 {
		t.Errorf("permissions: got %o want 0600", mode)
	}
}
