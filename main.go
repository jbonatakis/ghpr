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
	"github.com/jbonatakis/ghpr/internal/eventlog"
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
		why      = flag.Bool("why-seed", false, "explain what the startup backfill can and cannot see, and exit")
		showCfg  = flag.Bool("config", false, "print the config file path and exit")
		links    = flag.Bool("links", true, "make pull request references clickable (-links=false to disable)")
		remember = flag.Bool("remember", true, "keep the activity feed between runs (-remember=false to keep it in memory only)")
		watch    = flag.String("watch", "involved,requested,reviewed", "which pull requests reach the activity feed: involved (authored, commented, assigned, mentioned), requested (a review asked of you, including via a CODEOWNERS team), reviewed (you have already reviewed it)")
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
	shapes, err := gh.ParseShapes(*watch)
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
		Watch:    shapes,
	}

	// Read before the dashboard starts. It is one small file, and the backfill
	// cannot work out how small a gap it has to cover until it knows how far
	// the saved record already reaches.
	if *remember {
		log, err := eventlog.Open()
		if err != nil {
			fmt.Fprintln(os.Stderr, "ghpr: not keeping activity between runs:", err)
		} else {
			now := time.Now()
			watermark := log.Watermark(gh.BackfillScope(*extra, shapes))
			cached, dropped, err := log.Prepare(now)
			if err != nil {
				fmt.Fprintln(os.Stderr, "ghpr: could not tidy the activity log:", err)
			} else if dropped > 0 && *once {
				fmt.Fprintf(os.Stderr, "ghpr: tidied %d activity lines\n", dropped)
			}
			cfg.Log, cfg.Cached, cfg.Watermark = log, cached, watermark
		}
	}

	if *why {
		if err := explainSeed(cfg); err != nil {
			fail(err)
		}
		return
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
	if dir, err := eventlog.Dir(); err == nil {
		fmt.Fprintf(os.Stderr, "feed:   %s (activity kept between runs)\n", dir)
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

// explainSeed accounts for the startup backfill pull request by pull request.
//
// A thin feed has two very different causes — a search whose pull requests
// genuinely had a quiet month, or activity the one query cannot see — and from
// the outside they look identical. This distinguishes them: every timestamp the
// seed reads, whether it landed inside the window, and how much of the
// conversation was never fetched to be dated in the first place.
func explainSeed(cfg ui.Config) error {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	now := time.Now()
	window := cfg.Seed
	if window <= 0 {
		window = time.Hour
	}
	// The same clamp a real launch applies. Reporting the whole window instead
	// would describe a run nobody is about to make: with a saved record behind
	// it, the searches cover the gap and the rest is read off disk, and the
	// difference between those two pictures is most of the output below.
	since := now.Add(-window)
	if cfg.Watermark.After(since) {
		since = cfg.Watermark
	}

	var (
		res  gh.Result
		seen = map[string]bool{}
		// Which searches turned each pull request up. Whether something is
		// reached at all, and by which of them, is the first thing worth
		// knowing when the feed is missing a whole category of work.
		foundBy  = map[string][]gh.Shape{}
		perShape = map[gh.Shape]int{}
	)
	plans := gh.BackfillSearches(cfg.Extra, since, now, cfg.Watch)
	for _, plan := range plans {
		found, err := cfg.Client.Backfill(ctx, plan.Query, cfg.Max)
		if err != nil {
			fmt.Printf("NOTE: the search %q failed (%s), so whatever only it\n"+
				"      would have found is missing from this account\n\n",
				plan.Query, gh.CleanMessage(err.Error(), 80))
			continue
		}
		if res.Viewer == "" {
			res.Viewer = found.Viewer
		}
		res.RateLimit = found.RateLimit
		for _, p := range found.PRs {
			key := p.Key()
			if !hasShape(foundBy[key], plan.Shape) {
				foundBy[key] = append(foundBy[key], plan.Shape)
				perShape[plan.Shape]++
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			res.PRs = append(res.PRs, p)
		}
	}

	var closed int
	for _, p := range res.PRs {
		if p.State != "" {
			closed++
		}
	}
	fmt.Printf("%s · %d pull requests touched in the window (%d of them finished)\n",
		res.Viewer, len(res.PRs), closed)
	fmt.Printf("watching %s · searched %d ways over %d windows, four at a time\n",
		gh.ShapeNames(cfg.Watch), len(cfg.Watch), len(plans)/max(1, len(cfg.Watch)))
	for _, shape := range cfg.Watch {
		fmt.Printf("    %-10s reached %d pull requests\n", shape, perShape[shape])
	}
	fmt.Println("newest window first:")
	for _, plan := range plans {
		fmt.Printf("    %s\n", plan.Query)
	}
	fmt.Printf("-seed %s — the feed should reach back to %s\n",
		tidy(window), now.Add(-window).Local().Format("2006-01-02 15:04"))
	if cfg.Watermark.IsZero() {
		fmt.Println("nothing is on record as covered, so the searches cover all of it")
	} else {
		fmt.Printf("the record already covers up to %s, so the searches only cover\n"+
			"the %s since — everything older comes off disk (%d lines saved)\n",
			cfg.Watermark.Local().Format("2006-01-02 15:04"),
			tidy(now.Sub(since).Round(time.Minute)), len(cfg.Cached))
		fmt.Println("run with -remember=false to see what a first launch would search")
	}
	fmt.Println()

	when := func(at time.Time) string {
		if at.IsZero() {
			return "        not reported by the API"
		}
		mark := "outside"
		if !at.Before(since) {
			mark = "IN     "
		}
		return fmt.Sprintf("%6s  %s  %s", age(now.Sub(at)), mark,
			at.Local().Format("2006-01-02 15:04"))
	}

	var (
		seeded, contributing int
		unfetched            int
	)
	for _, p := range res.PRs {
		events := gh.Seed([]gh.PR{p}, since, res.Viewer)
		seeded += len(events)
		if len(events) > 0 {
			contributing++
		}
		if missed := p.IssueComments - len(p.RecentComments); missed > 0 {
			unfetched += missed
		}

		what := "open"
		if p.State != "" {
			what = strings.ToLower(string(p.State))
		}
		fmt.Printf("%-46s %-7s %d seeded   found by %s\n",
			p.Key(), what, len(events), shapeList(foundBy[p.Key()]))
		fmt.Printf("    opened          %s\n", when(p.CreatedAt))
		fmt.Printf("    head commit     %s\n", when(p.PushedAt))
		fmt.Printf("    newest check    %s\n", when(p.ChecksAt))
		if !p.FinishedAt.IsZero() {
			fmt.Printf("    %-16s%s\n", strings.ToLower(string(p.State)), when(p.FinishedAt))
		}
		reviews := p.AllReviews
		if len(reviews) == 0 {
			reviews = p.Reviewers
		}
		for _, r := range reviews {
			if r.At.IsZero() {
				continue
			}
			fmt.Printf("    review %-9s%s\n", trunc(r.Login, 9), when(r.At))
		}
		for _, c := range p.Pushes {
			fmt.Printf("    commit %-9s%s\n", trunc(c.By, 9), when(c.At))
		}
		for _, c := range p.RecentComments {
			fmt.Printf("    comment %-8s%s\n", trunc(c.By, 8), when(c.At))
		}
		if missed := p.IssueComments - len(p.RecentComments); missed > 0 {
			fmt.Printf("    %d older conversation %s never fetched (the query takes the newest 3)\n",
				missed, plural(missed, "comment"))
		}
		for _, c := range p.ThreadComments {
			fmt.Printf("    review comment %-1s%s\n", "", when(c.At))
		}
		if missed := p.ReviewComments - len(p.ThreadComments); missed > 0 {
			fmt.Printf("    %d further review-thread %s beyond what one page carries\n",
				missed, plural(missed, "comment"))
		}
		fmt.Println()
	}

	fmt.Printf("%d events seeded, from %d of %d pull requests\n", seeded, contributing, len(res.PRs))
	if unfetched > 0 {
		fmt.Printf("out of reach: %d conversation comments beyond the newest twenty\n", unfetched)
	}
	fmt.Printf("this account cost %d rate-limit points\n", res.RateLimit.Cost)
	fmt.Println()
	if contributing == 0 {
		fmt.Println("Nothing at all landed in the window. Either the search really has been")
		fmt.Println("quiet that long, or you want a different -mode: authored covers only your")
		fmt.Println("own pull requests, while involved covers everything you have touched.")
	} else {
		fmt.Println("Lines marked \"outside\" would come back with a wider -seed. Lines marked")
		fmt.Println("\"not reported\" never had a date to begin with and no window will reach")
		fmt.Println("them — a review request and a conflict appearing are the two that never do.")
		fmt.Println()
		fmt.Println("If a pull request you expected is absent entirely, no search reached it:")
		fmt.Println("compare the \"found by\" column against what you expected, and check the")
		fmt.Println("counts above for a shape that reached nothing at all.")
	}
	return nil
}

func plural(n int, unit string) string {
	if n == 1 {
		return unit
	}
	return unit + "s"
}

// tidy drops the zero tail Go leaves on a round duration: 1h, not 1h0m0s.
func tidy(d time.Duration) string {
	s := d.String()
	s = strings.TrimSuffix(s, "0s")
	if strings.HasSuffix(s, "h0m") {
		s = strings.TrimSuffix(s, "0m")
	}
	return s
}

func trunc(s string, w int) string {
	if len(s) > w {
		return s[:w]
	}
	return s
}

func hasShape(list []gh.Shape, want gh.Shape) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func shapeList(shapes []gh.Shape) string {
	if len(shapes) == 0 {
		return "nothing"
	}
	return gh.ShapeNames(shapes)
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
