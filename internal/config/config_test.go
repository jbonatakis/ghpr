package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	return dir
}

func TestLoadMissingFileYieldsDefaults(t *testing.T) {
	isolate(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("a missing config should not be an error: %v", err)
	}
	if !c.Grouped {
		t.Error("Grouped should default to true")
	}
	if c.Mode != "authored" {
		t.Errorf("Mode = %q, want authored", c.Mode)
	}
	if len(c.HiddenOrgs) != 0 {
		t.Errorf("HiddenOrgs = %v, want empty", c.HiddenOrgs)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	isolate(t)

	want := Config{
		HiddenOrgs: []string{"acme", "octo-dev"},
		Mode:       "involved",
		Sort:       "comments",
		Grouped:    false,
		HideDrafts: true,
	}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Mode != want.Mode || got.Sort != want.Sort {
		t.Errorf("mode/sort = %q/%q, want %q/%q", got.Mode, got.Sort, want.Mode, want.Sort)
	}
	if got.Grouped != false || got.HideDrafts != true {
		t.Errorf("booleans did not round-trip: %+v", got)
	}
	// Saved sorted, so the order is stable rather than map-random.
	if len(got.HiddenOrgs) != 2 || got.HiddenOrgs[0] != "acme" {
		t.Errorf("HiddenOrgs = %v, want sorted [acme octo-dev]", got.HiddenOrgs)
	}
}

func TestSaveCreatesDirectoryAndIsAtomic(t *testing.T) {
	dir := isolate(t)

	if err := (Config{HiddenOrgs: []string{"acme"}}).Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := filepath.Join(dir, "ghpr", "config.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config not written: %v", err)
	}

	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "config.json" {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func TestNormalizeDedupesAndDropsBlanks(t *testing.T) {
	isolate(t)

	if err := (Config{HiddenOrgs: []string{"acme", "ACME", "  ", "beta", "acme"}}).Save(); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.HiddenOrgs) != 2 {
		t.Errorf("HiddenOrgs = %v, want 2 entries", got.HiddenOrgs)
	}
}

func TestHiddenIsCaseInsensitive(t *testing.T) {
	c := Config{HiddenOrgs: []string{"Acme"}}
	if !c.Hidden("acme") {
		t.Error("Hidden should ignore case")
	}
	if c.Hidden("someoneelse") {
		t.Error("unrelated org reported as hidden")
	}
}

func TestCorruptConfigFallsBackToDefaults(t *testing.T) {
	dir := isolate(t)
	if err := os.MkdirAll(filepath.Join(dir, "ghpr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ghpr", "config.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}

	c, err := Load()
	if err == nil {
		t.Error("a corrupt config should report an error")
	}
	if !c.Grouped || c.Mode != "authored" {
		t.Errorf("corrupt config should still yield usable defaults, got %+v", c)
	}
}

func TestPathHonoursXDGConfigHome(t *testing.T) {
	dir := isolate(t)
	path, err := Path()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "ghpr", "config.json"); path != want {
		t.Errorf("Path = %q, want %q", path, want)
	}
}

func TestFoldsAndHiddenItemsRoundTrip(t *testing.T) {
	isolate(t)

	want := Config{
		CollapsedRepos: []string{"acme/starfield", "acme/tools"},
		HiddenRepos:    []string{"acme/legacy"},
		HiddenPRs:      []string{"acme/tools#42", "acme/starfield#96"},
	}
	if err := want.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(got.CollapsedRepos) != 2 || got.CollapsedRepos[0] != "acme/starfield" {
		t.Errorf("CollapsedRepos = %v, want sorted [acme/starfield acme/tools]", got.CollapsedRepos)
	}
	if !got.RepoHidden("acme/legacy") {
		t.Errorf("HiddenRepos = %v", got.HiddenRepos)
	}
	if !got.PRHidden("acme/tools#42") || !got.PRHidden("acme/starfield#96") {
		t.Errorf("HiddenPRs = %v", got.HiddenPRs)
	}
	if got.PRHidden("acme/tools#43") {
		t.Error("an unrelated PR reported as hidden")
	}
}

func TestHiddenLookupsIgnoreCase(t *testing.T) {
	c := Config{HiddenRepos: []string{"Acme/Starfield"}, HiddenPRs: []string{"Acme/Tools#42"}}
	if !c.RepoHidden("acme/starfield") {
		t.Error("RepoHidden should ignore case")
	}
	if !c.PRHidden("acme/tools#42") {
		t.Error("PRHidden should ignore case")
	}
}

func TestPeekingIsNotAConfigField(t *testing.T) {
	isolate(t)
	if err := (Config{HiddenPRs: []string{"a/b#1"}}).Save(); err != nil {
		t.Fatal(err)
	}
	path, _ := Path()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Revealing hidden items is a transient peek, so it must not be stored;
	// otherwise "hidden" would stop meaning hidden on a fresh start.
	if strings.Contains(string(data), "showHidden") {
		t.Errorf("config should not persist the peek toggle:\n%s", data)
	}
}
