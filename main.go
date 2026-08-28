// Command ghpr is a terminal dashboard for the GitHub pull requests you care
// about. It polls the GraphQL API and keeps a live, grouped view of their
// status, checks, comments and age.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
	"github.com/jbonatakis/ghpr/internal/ui"
)

var version = "0.1.0"

func main() {
	var (
		interval = flag.Duration("interval", 30*time.Second, "how often to poll GitHub")
		max      = flag.Int("max", 200, "maximum pull requests to track")
		modeName = flag.String("mode", "authored", "which PRs to watch: authored, review-requested, involved")
		extra    = flag.String("query", "", "extra GitHub search qualifiers, e.g. \"org:acme\"")
		seed     = flag.Duration("seed", time.Hour, "fill the activity feed in from this far back at startup (0 to start blank)")
		api      = flag.String("api", "", "GraphQL endpoint, for GitHub Enterprise Server")
		once     = flag.Bool("once", false, "print a one-shot plain-text snapshot and exit")
		showCfg  = flag.Bool("config", false, "print the config file path and exit")
		links    = flag.Bool("links", true, "make pull request references clickable (-links=false to disable)")
		showVer  = flag.Bool("version", false, "print version and exit")
	)
	flag.Usage = usage
	flag.Parse()

	if *showVer {
		fmt.Println("ghpr", version)
		return
	}
	if *showCfg {
		path, err := config.Path()
		if err != nil {
			fail(err)
		}
		fmt.Println(path)
		return
	}

	// Preferences chosen inside the app persist here; an explicit flag still
	// wins for this run.
	prefs, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, "ghpr: ignoring config:", err)
		prefs = config.Defaults()
	}
	if !flagWasSet("mode") && prefs.Mode != "" {
		*modeName = prefs.Mode
	}
	if !flagWasSet("seed") {
		if d, err := parseSeed(prefs.Seed); err != nil {
			fmt.Fprintln(os.Stderr, "ghpr: ignoring seed in config:", err)
		} else if d >= 0 {
			*seed = d
		}
	}
	if *seed < 0 {
		*seed = 0
	}

	mode, err := parseMode(*modeName)
	if err != nil {
		fail(err)
	}
	if *interval < 5*time.Second {
		fail(fmt.Errorf("interval must be at least 5s (GitHub rate limits)"))
	}

	token, err := gh.ResolveToken()
	if err != nil {
		fail(err)
	}
	client := gh.NewClient(token)
	if *api != "" {
		client.Endpoint = *api
	}

	cfg := ui.Config{
		Client:   client,
		Mode:     mode,
		Interval: *interval,
		Max:      *max,
		Extra:    *extra,
		Prefs:    prefs,
		Links:    *links,
		Seed:     *seed,
	}

	if *once {
		if err := snapshot(cfg); err != nil {
			fail(err)
		}
		return
	}

	p := tea.NewProgram(ui.New(cfg), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fail(err)
	}
}

// flagWasSet reports whether the user passed a flag explicitly, so that a
// saved preference does not silently override the command line.
func flagWasSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

func usage() {
	fmt.Fprintf(os.Stderr, "ghpr — a live dashboard for your open GitHub pull requests\n\nusage: ghpr [flags]\n\n")
	flag.PrintDefaults()
	fmt.Fprintf(os.Stderr, "\nauth:   uses $GITHUB_TOKEN, $GH_TOKEN, or `gh auth token`.\n")
	if path, err := config.Path(); err == nil {
		fmt.Fprintf(os.Stderr, "config: %s (edit organizations in-app with O)\n", path)
	}
}

// parseSeed reads the saved seed window. A negative duration is returned for
// "nothing saved", which leaves the flag's default in place — distinct from a
// saved "0", which is a deliberate choice to start the feed blank.
func parseSeed(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return -1, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return -1, fmt.Errorf("%q is not a duration like \"1h\"", s)
	}
	if d < 0 {
		return 0, nil
	}
	return d, nil
}

func parseMode(s string) (gh.Mode, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "authored", "author", "mine":
		return gh.ModeAuthored, nil
	case "review-requested", "review", "reviewing":
		return gh.ModeReviewRequested, nil
	case "involved", "involves":
		return gh.ModeInvolved, nil
	}
	return 0, fmt.Errorf("unknown mode %q: want authored, review-requested, or involved", s)
}

// snapshot prints one plain-text listing, for pipes, cron and scripts.
func snapshot(cfg ui.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	res, err := cfg.Client.Fetch(ctx, cfg.Mode.Query(cfg.Extra), cfg.Max)
	if err != nil {
		return err
	}
	now := time.Now()

	byRepo := map[string][]gh.PR{}
	var hidden int
	for _, p := range res.PRs {
		if cfg.Prefs.Hidden(p.Org()) {
			hidden++
			continue
		}
		byRepo[p.Repo] = append(byRepo[p.Repo], p)
	}
	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	sort.Strings(repos)

	fmt.Printf("%s · %s · %d open pull requests", res.Viewer, cfg.Mode, len(res.PRs)-hidden)
	if hidden > 0 {
		fmt.Printf(" (%d hidden by org filter)", hidden)
	}
	fmt.Println()
	for _, repo := range repos {
		group := byRepo[repo]
		sort.Slice(group, func(i, j int) bool {
			if a, b := group[i].Status(), group[j].Status(); a != b {
				return a < b
			}
			return group[i].UpdatedAt.After(group[j].UpdatedAt)
		})
		fmt.Printf("\n%s (%d)\n", repo, len(group))
		for _, p := range group {
			checks := "—"
			if n := p.ChecksPassed + p.ChecksFailed + p.ChecksPending; n > 0 {
				checks = fmt.Sprintf("%d/%d", p.ChecksPassed, n)
			}
			fmt.Printf("  #%-6d %-10s checks %-7s cmt %-4d %s  %s\n",
				p.Number, p.Status().Short(), checks, p.Comments(),
				age(now.Sub(p.CreatedAt)), p.Title)
		}
	}
	fmt.Printf("\nrate limit: %d/%d points remaining this hour (this query cost %d)\n",
		res.RateLimit.Remaining, res.RateLimit.Limit, res.RateLimit.Cost)
	return nil
}

func age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%3dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%3dh", int(d.Hours()))
	}
	return fmt.Sprintf("%3dd", int(d.Hours()/24))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "ghpr:", err)
	os.Exit(1)
}
