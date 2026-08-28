// Package config stores the dashboard's user preferences on disk so that
// choices made inside the app survive a restart.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Config is the on-disk preference file.
type Config struct {
	// HiddenOrgs lists organizations to leave out of the dashboard. Storing
	// exclusions rather than inclusions means an org you join later shows up
	// on its own instead of silently going missing.
	HiddenOrgs []string `json:"hiddenOrgs"`

	// CollapsedRepos are repository groups left folded shut.
	CollapsedRepos []string `json:"collapsedRepos"`

	// HiddenRepos and HiddenPRs are things the user has explicitly dismissed:
	// whole repositories, and individual pull requests by "owner/repo#number".
	HiddenRepos []string `json:"hiddenRepos"`
	HiddenPRs   []string `json:"hiddenPRs"`

	Mode string `json:"mode,omitempty"`
	Sort string `json:"sort,omitempty"`

	// Seed is how far back to fill the activity feed in at startup, as a Go
	// duration such as "1h". Empty keeps the built-in default; "0" starts the
	// feed blank. The -seed flag overrides it for one run.
	Seed string `json:"seed,omitempty"`

	Grouped    bool `json:"grouped"`
	HideDrafts bool `json:"hideDrafts"`
}

// Defaults is the configuration used before anything has been saved. Load
// unmarshals over these, so a field absent from the file keeps its default.
func Defaults() Config {
	return Config{
		HiddenOrgs: nil,
		Mode:       "authored",
		Sort:       "attention",
		Seed:       "1h",
		Grouped:    true,
		HideDrafts: false,
	}
}

// Dir is the directory holding the config file, honouring XDG_CONFIG_HOME.
func Dir() (string, error) {
	if x := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); x != "" {
		return filepath.Join(x, "ghpr"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "ghpr"), nil
}

// Path is the config file's location.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "config.json"), nil
}

// Load reads the config file. A missing file is not an error: it yields the
// defaults, which is what a first run should see.
func Load() (Config, error) {
	c := Defaults()
	path, err := Path()
	if err != nil {
		return c, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, fmt.Errorf("read %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &c); err != nil {
		return Defaults(), fmt.Errorf("parse %s: %w", path, err)
	}
	c.normalizeAll()
	return c, nil
}

func (c *Config) normalizeAll() {
	c.HiddenOrgs = normalize(c.HiddenOrgs)
	c.CollapsedRepos = normalize(c.CollapsedRepos)
	c.HiddenRepos = normalize(c.HiddenRepos)
	c.HiddenPRs = normalize(c.HiddenPRs)
}

// Save writes the config atomically, creating the directory if needed.
func (c Config) Save() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}
	c.normalizeAll()

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	// Write to a sibling temp file first so a crash cannot leave a partial
	// config behind.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".config-*.json")
	if err != nil {
		return fmt.Errorf("create temp config: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	return nil
}

// Hidden reports whether an organization is filtered out.
func (c Config) Hidden(org string) bool { return contains(c.HiddenOrgs, org) }

// RepoHidden reports whether a whole repository is dismissed.
func (c Config) RepoHidden(repo string) bool { return contains(c.HiddenRepos, repo) }

// PRHidden reports whether an individual pull request is dismissed.
func (c Config) PRHidden(key string) bool { return contains(c.HiddenPRs, key) }

func contains(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(v, want) {
			return true
		}
	}
	return false
}

// normalize de-duplicates and sorts, so the file stays stable across saves.
func normalize(orgs []string) []string {
	if len(orgs) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(orgs))
	for _, o := range orgs {
		o = strings.TrimSpace(o)
		if o == "" {
			continue
		}
		k := strings.ToLower(o)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, o)
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}
