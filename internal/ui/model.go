package ui

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
)

// freshFor is how long a PR stays highlighted after we notice it changed.
const freshFor = 60 * time.Second

// maxEvents caps the activity feed's backlog. It is generous because the feed
// can be scrolled: the cap is there to bound memory over a dashboard left open
// for days, not to decide how far back the user is allowed to look.
const maxEvents = 500

type sortMode int

const (
	sortStatus sortMode = iota
	sortUpdated
	sortAge
	sortComments
	sortChurn
)

func (s sortMode) String() string {
	switch s {
	case sortUpdated:
		return "recently updated"
	case sortAge:
		return "oldest first"
	case sortComments:
		return "most comments"
	case sortChurn:
		return "largest diff"
	}
	return "needs attention"
}

// key is the stable name written to the config file.
func (s sortMode) key() string {
	switch s {
	case sortUpdated:
		return "updated"
	case sortAge:
		return "oldest"
	case sortComments:
		return "comments"
	case sortChurn:
		return "diff"
	}
	return "attention"
}

func parseSort(key string) sortMode {
	for _, s := range sortModes {
		if s.key() == key {
			return s
		}
	}
	return sortStatus
}

var sortModes = []sortMode{sortStatus, sortUpdated, sortAge, sortComments, sortChurn}

// row is one line in the list: either a repo header or a pull request.
type row struct {
	repo   string
	pr     *gh.PR
	count  int       // repo rows: how many PRs inside
	urgent gh.Status // repo rows: most urgent status inside
	fresh  bool      // repo rows: something inside changed recently
	hidden bool      // dismissed, and only on screen because we are peeking
}

func (r row) isRepo() bool { return r.pr == nil }

// Config carries the options main() resolved from flags and the config file.
type Config struct {
	Client   *gh.Client
	Mode     gh.Mode
	Interval time.Duration
	Max      int
	Extra    string
	Prefs    config.Config

	// Seed is how far back the feed is filled in from the first poll, so a
	// dashboard just opened is not blank about the hour you missed. Zero
	// leaves it empty until something actually changes.
	Seed time.Duration

	// Links turns pull request references into clickable terminal hyperlinks.
	Links bool
}

// Model is the dashboard's bubbletea model.
type Model struct {
	cfg Config

	prs    []gh.PR
	rows   []row
	cursor int
	top    int

	collapsed map[string]bool
	changed   map[string]time.Time
	events    []gh.Event

	viewer    string
	rate      gh.RateLimit
	lastFetch time.Time
	nextFetch time.Time
	loading   bool
	loaded    bool
	// lastComplete records whether the snapshot now on screen came from a
	// search that ran to the end. A pull request appearing after an incomplete
	// one may only have been missed before.
	lastComplete bool
	err          error
	warn         string
	failures     int

	sortBy     sortMode
	grouped    bool
	hideDrafts bool
	showDetail bool
	showEvents bool
	showHelp   bool

	// Activity feed navigation. eventCursor and eventTop count backwards from
	// the newest event, matching the order the pane draws them in: 0 is the
	// line at the top. They only mean anything while eventsFocus is set —
	// leaving the feed returns it to the live view.
	eventsFocus bool
	eventCursor int
	eventTop    int

	// The feed has its own filter, deliberately separate from the list's. The
	// backlog stays a complete record of the session either way; these are two
	// independent views onto it, so narrowing one never narrows the other.
	feedFilter    textinput.Model
	feedFiltering bool

	// seeded records that the backlog has already been filled in once. It is
	// per session rather than per search: switching mode starts a new list,
	// but the feed spans them all, and seeding again would repeat the history
	// of every pull request the two modes have in common.
	seeded bool

	// backfilling is true from launch until the startup searches answer. They
	// are far slower than a poll, and an empty feed that says "watching for
	// changes" while they run is describing the wrong thing entirely.
	backfilling bool
	seedFailed  bool

	// backfillCh carries each search's answer as it lands. backfillSeen is what
	// has already been seeded from those answers: the searches overlap by
	// design, and a pull request found twice must not have its whole history
	// told twice. backfillFound counts what was filed, for the closing word.
	// backfillStop asks the workers to give up early: chunks arrive newest
	// first and the backlog only keeps maxEvents, so once that many have been
	// filed everything still queued would be trimmed away on arrival.
	backfillCh      chan backfillChunkMsg
	backfillStop    chan struct{}
	backfillStopped bool
	backfillSeen    map[string]bool
	backfillFound   int

	// The pool finishes windows out of order, so answers are held until every
	// window ahead of them has been filed. Without that the feed jumps: a slow
	// first window drops newer activity in above whatever is already on screen,
	// instead of the older activity arriving underneath it.
	backfillHeld    map[int][]gh.PR
	backfillAnswers map[int]int
	backfillNext    int
	backfillViewer  string
	backfillSince   time.Time

	// absent tracks pull requests that dropped out of the search but have not
	// been accounted for. They stay on screen and are looked up directly, so a
	// page-boundary artifact never shows up as "merged or closed".
	absent map[string]*absence

	hiddenOrgs  map[string]bool
	hiddenRepos map[string]bool
	hiddenPRs   map[string]bool
	showHidden  bool
	showOrgs    bool
	orgCursor   int
	orgDraft    map[string]bool

	filter    textinput.Model
	filtering bool

	toast       string
	toastExpiry time.Time

	// fetchSeq identifies the most recently issued request. Answers to any
	// earlier one are discarded: switching mode starts a new search while the
	// old one may still be in flight, and a slow review-requested response
	// arriving after a fast authored one would otherwise fill the list with
	// pull requests from the mode the user just left.
	fetchSeq int

	spin      spinner.Model
	now       time.Time
	startedAt time.Time
	width     int
	height    int
}

// New builds the initial model.
func New(cfg Config) Model {
	ti := textinput.New()
	ti.Prompt = "filter: "
	ti.Placeholder = "repo, title, label…"
	ti.PromptStyle = stBold
	ti.CharLimit = 80

	fi := textinput.New()
	fi.Prompt = "activity: "
	fi.Placeholder = "repo, number, what happened, who…"
	fi.PromptStyle = stBold
	fi.CharLimit = 80

	sp := spinner.New(spinner.WithSpinner(spinner.MiniDot), spinner.WithStyle(stMuted))

	// A caller that never loaded a config would otherwise inherit Go's zero
	// values here, which would silently start the dashboard ungrouped.
	if cfg.Prefs.Mode == "" && cfg.Prefs.Sort == "" {
		cfg.Prefs = config.Defaults()
	}

	hidden := setOf(cfg.Prefs.HiddenOrgs)
	collapsed := setOf(cfg.Prefs.CollapsedRepos)

	now := time.Now()
	return Model{
		cfg:         cfg,
		startedAt:   now,
		collapsed:   collapsed,
		changed:     map[string]time.Time{},
		absent:      map[string]*absence{},
		hiddenOrgs:  hidden,
		hiddenRepos: setOf(cfg.Prefs.HiddenRepos),
		hiddenPRs:   setOf(cfg.Prefs.HiddenPRs),
		grouped:     cfg.Prefs.Grouped,
		hideDrafts:  cfg.Prefs.HideDrafts,
		sortBy:      parseSort(cfg.Prefs.Sort),
		filter:      ti,
		feedFilter:  fi,
		spin:        sp,
		now:         now,
		nextFetch:   now,
		loading:     true,
		// Init issues the backfill on exactly this condition.
		backfilling:     cfg.Seed > 0,
		backfillCh:      backfillChannel(cfg.Seed),
		backfillStop:    make(chan struct{}),
		backfillSeen:    map[string]bool{},
		backfillHeld:    map[int][]gh.PR{},
		backfillAnswers: map[int]int{},
		fetchSeq:        1,
		width:           100,
		height:          30,
	}
}

// backfillChannel carries the searches' answers, buffered enough that a worker
// never blocks handing one over — the UI goroutine may be busy redrawing, and a
// search that has done its work should not be kept waiting to say so.
func backfillChannel(seed time.Duration) chan backfillChunkMsg {
	if seed <= 0 {
		return nil
	}
	return make(chan backfillChunkMsg, 32)
}

// pointsPerHour estimates the GraphQL budget the current interval consumes.
// GitHub bills GraphQL in points per hour, not requests: a query is scored by
// how much it asks for, and this one costs a handful.
func (m *Model) pointsPerHour() int {
	cost := m.rate.Cost
	if cost <= 0 {
		cost = 3
	}
	if m.cfg.Interval <= 0 {
		return cost
	}
	return int(float64(cost) * time.Hour.Seconds() / m.cfg.Interval.Seconds())
}

func setOf(items []string) map[string]bool {
	out := make(map[string]bool, len(items))
	for _, s := range items {
		out[s] = true
	}
	return out
}

// keysOf returns the entries a set has switched on, so only real state is
// written to disk.
func keysOf(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k, on := range set {
		if on {
			out = append(out, k)
		}
	}
	return out
}

// savePrefs persists the current view settings. A failure here is worth
// telling the user about but must never interrupt the dashboard.
func (m *Model) savePrefs() {
	prefs := config.Config{
		HiddenOrgs:     keysOf(m.hiddenOrgs),
		CollapsedRepos: keysOf(m.collapsed),
		HiddenRepos:    keysOf(m.hiddenRepos),
		HiddenPRs:      keysOf(m.hiddenPRs),
		Mode:           m.cfg.Mode.String(),
		Sort:           m.sortBy.key(),
		// Carried through rather than derived: the seed window has no in-app
		// control, so writing the struct without it would quietly erase the
		// user's setting the first time they folded a repo.
		Seed:       m.cfg.Prefs.Seed,
		Grouped:    m.grouped,
		HideDrafts: m.hideDrafts,
	}
	m.cfg.Prefs = prefs
	if err := prefs.Save(); err != nil {
		m.setToast("could not save config: " + err.Error())
	}
}

type fetchDoneMsg struct {
	seq int // the request this answers
	res gh.Result
	err error
}

// verifyDoneMsg carries the true state of pull requests that went missing.
type verifyDoneMsg struct {
	seq     int
	states  map[string]gh.State
	checked []gh.PR
	err     error
}

// backfillChunkMsg is one search's worth of the reconstructed past, delivered
// as it lands rather than at the end. A month of activity takes long enough to
// gather that waiting for all of it before showing any is the difference
// between a feed that fills in and a feed that hangs.
type backfillChunkMsg struct {
	prs    []gh.PR
	viewer string
	since  time.Time
	window int
	err    error
}

// backfillDoneMsg says every search has answered.
type backfillDoneMsg struct {
	events []gh.Event // only set by tests driving the old whole-shot path
	err    error
}

// backfillWorkers bounds how many searches are in flight at once.
//
// GitHub asks that requests for one user be made serially, and answers a burst
// with a secondary rate limit rather than data. This is a compromise: enough
// parallelism that the wait is a fraction of what it was, few enough that the
// API does not start refusing. The searches are heavy, so even four at a time
// is a lot of work for the other end.
const backfillWorkers = 4

// backfillCmd reconstructs the seed window, off the UI goroutine.
//
// Deliberately not scoped to the dashboard's mode: the feed spans every mode by
// design, so reconstructing it from one of them would leave out most of what
// the window held. See gh.BackfillSearches.
//
// Each search is best-effort. They overlap, so a pull request found twice is
// only seeded once, and a failure in one still leaves what the other found
// worth showing — only a complete washout is reported as an error.
// runBackfill fans the searches out across a bounded pool and streams each
// answer down the model's channel, closing it when they have all reported.
//
// The plans come back newest-window-first and are handed out in that order, so
// what lands first is what happened most recently — which is both what the
// reader wants first and what the top of the feed is.
func (m Model) runBackfill() tea.Cmd {
	cfg, started, out, stop := m.cfg, m.startedAt, m.backfillCh, m.backfillStop
	if out == nil {
		return nil
	}
	return func() tea.Msg {
		// Generous: each search is paginated and every page asks for far more
		// than a poll does.
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
		defer cancel()
		since := started.Add(-cfg.Seed)

		plans := make(chan gh.BackfillPlan)
		var wg sync.WaitGroup
		for i := 0; i < backfillWorkers; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for plan := range plans {
					// Checked per plan rather than per page: a search already
					// in flight is cheaper to finish than to abandon.
					select {
					case <-stop:
						return
					default:
					}
					res, err := cfg.Client.Backfill(ctx, plan.Query, cfg.Max)
					out <- backfillChunkMsg{
						prs: res.PRs, viewer: res.Viewer, since: since,
						window: plan.Window, err: err,
					}
				}
			}()
		}

		// Handed out newest window first, so what lands first is what happened
		// most recently — which is what the reader wants and what the top of
		// the feed is. Stops feeding the moment the backlog is full.
	feed:
		for _, plan := range gh.BackfillSearches(cfg.Extra, since, started) {
			select {
			case <-stop:
				break feed
			case plans <- plan:
			}
		}
		close(plans)

		wg.Wait()
		close(out)
		return nil
	}
}

// awaitBackfill waits for the next chunk, or for the channel to close.
func (m Model) awaitBackfill() tea.Cmd {
	ch := m.backfillCh
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		chunk, ok := <-ch
		if !ok {
			return backfillDoneMsg{}
		}
		return chunk
	}
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.fetchCmdSeq(m.fetchSeq), tickCmd(), m.spin.Tick}
	if m.cfg.Seed > 0 {
		// One command drives the searches, another waits on their answers; the
		// second re-arms itself for as long as they keep coming.
		cmds = append(cmds, m.runBackfill(), m.awaitBackfill())
	}
	return tea.Batch(cmds...)
}

// applyBackfillChunk holds one search's answer until its window's turn.
func (m Model) applyBackfillChunk(msg backfillChunkMsg) Model {
	if msg.err != nil {
		// Remembered rather than reported: the other searches may still find
		// plenty, and only a complete washout is worth calling a failure. The
		// window is still counted as answered, or its turn never comes and
		// everything behind it waits forever.
		m.seedFailed = true
	} else {
		m.backfillHeld[msg.window] = append(m.backfillHeld[msg.window], msg.prs...)
		if m.backfillViewer == "" {
			m.backfillViewer = msg.viewer
		}
		m.backfillSince = msg.since
	}
	m.backfillAnswers[msg.window]++

	// Release every window that is now complete and has nothing older ahead
	// of it still outstanding.
	for m.backfillAnswers[m.backfillNext] >= gh.BackfillShapes {
		m = m.releaseBackfillWindow(m.backfillNext)
		m.backfillNext++
	}
	return m
}

// releaseBackfillWindow files one window's findings into the feed.
func (m Model) releaseBackfillWindow(window int) Model {
	prs := m.backfillHeld[window]
	delete(m.backfillHeld, window)
	if len(prs) == 0 {
		return m
	}

	fresh := make([]gh.PR, 0, len(prs))
	for _, p := range prs {
		if m.backfillSeen[p.Key()] {
			continue // the search shapes overlap, and windows share their bounds
		}
		m.backfillSeen[p.Key()] = true
		fresh = append(fresh, p)
	}
	events := gh.Seed(fresh, m.backfillSince, m.backfillViewer)
	if len(events) == 0 {
		return m
	}

	first := m.backfillFound == 0
	m.backfillFound += len(events)
	if m.backfillFound >= maxEvents && !m.backfillStopped {
		// The backlog is full. Windows come newest first, so anything still
		// queued is older than what has already been filed and would be
		// trimmed away the moment it arrived — asking GitHub for it is work
		// nobody will ever see.
		m.backfillStopped = true
		close(m.backfillStop)
	}
	if first {
		// The boundary goes in with the first thing to arrive, so the feed
		// reads correctly while the rest is still landing.
		events = append(events, gh.SessionEvent(m.startedAt))
		m.showEvents = true
	}
	m.record(events)
	m.clampScroll()
	return m
}

// flushBackfill files whatever is still held once the searches have stopped.
// A window whose searches failed never completes, so without this everything
// behind it would be gathered and then silently dropped.
func (m Model) flushBackfill() Model {
	windows := make([]int, 0, len(m.backfillHeld))
	for w := range m.backfillHeld {
		windows = append(windows, w)
	}
	sort.Ints(windows)
	for _, w := range windows {
		m = m.releaseBackfillWindow(w)
	}
	return m
}

// applyBackfill files the reconstructed past, if there was any.
// applyBackfill closes the reconstruction out once every search has answered.
// The events themselves arrived chunk by chunk; what is left is to stop saying
// "filling in", and to say what it all came to.
func (m Model) applyBackfill(msg backfillDoneMsg) Model {
	m = m.flushBackfill()
	m.seeded = true
	m.backfilling = false

	// The whole-shot path, still used by tests that hand over a finished set.
	if len(msg.events) > 0 {
		m.backfillFound += len(msg.events)
		m.record(append(msg.events, gh.SessionEvent(m.startedAt)))
		m.showEvents = true
	}
	if msg.err != nil {
		m.seedFailed = true
	}

	switch {
	case m.backfillFound > 0:
		// It opens unfocused, so the arrow keys still belong to the list —
		// worth saying out loud, because a pane that appeared unbidden gives
		// no hint that getting into it takes a keypress.
		m.setToast(fmt.Sprintf("%s from the last %s — e to scroll the feed",
			plural(m.backfillFound, "event"), tidyDuration(m.cfg.Seed)))
	case m.seedFailed:
		// Never fatal: the dashboard's own job is unaffected by not knowing
		// what happened before it started.
		m.setToast("could not fill the feed in — the startup searches failed")
	}
	m.clampScroll()
	return m
}

// startFetch supersedes any request in flight and returns the command for the
// new one.
func (m *Model) startFetch() tea.Cmd {
	m.fetchSeq++
	m.loading = true
	return m.fetchCmdSeq(m.fetchSeq)
}

// fetchCmdSeq polls the API once, off the UI goroutine, stamped with seq.
func (m Model) fetchCmdSeq(seq int) tea.Cmd {
	cfg := m.cfg
	return func() tea.Msg {
		// Generous: a review-requested search across dozens of other people's
		// repositories can take several seconds per page.
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		res, err := cfg.Client.Fetch(ctx, cfg.Mode.Query(cfg.Extra), cfg.Max)
		return fetchDoneMsg{seq: seq, res: res, err: err}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.filter.Width = max(20, m.width-20)
		m.clampScroll()
		return m, nil

	case spinner.TickMsg:
		// Only animate while something is in flight; an idle dashboard should
		// not repaint ten times a second for hours on end. The backfill counts:
		// it outlasts the first poll by a long way, and a frozen spinner over a
		// feed that is still filling in reads as a hang.
		if !m.loading && !m.backfilling {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd

	case tickMsg:
		m.now = time.Time(msg)
		m.pruneFresh()
		if m.toast != "" && m.now.After(m.toastExpiry) {
			m.toast = ""
		}
		cmds := []tea.Cmd{tickCmd()}
		if !m.loading && !m.now.Before(m.nextFetch) {
			cmds = append(cmds, m.startFetch(), m.spin.Tick)
		}
		return m, tea.Batch(cmds...)

	case fetchDoneMsg:
		return m.applyFetch(msg)

	case verifyDoneMsg:
		return m.applyVerify(msg), nil

	case backfillChunkMsg:
		// Re-arm: there will be another until the channel closes.
		return m.applyBackfillChunk(msg), m.awaitBackfill()

	case backfillDoneMsg:
		return m.applyBackfill(msg), nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

// absence records a pull request missing from the search, and what we have
// done about it.
type absence struct {
	pr     gh.PR
	misses int  // consecutive complete polls without it
	asked  bool // a state look-up has already been issued
}

// maxAbsentPolls bounds how long a missing pull request is carried. Absence
// can mean three things: a paging artifact (it comes back), it finished (the
// look-up tells us), or it no longer matches the search at all — a withdrawn
// review request, say, which leaves it open but irrelevant. Without a cap that
// last case would be carried and re-queried forever.
const maxAbsentPolls = 3

// retryDelay backs off after a failed poll, starting quickly because most
// upstream failures clear within seconds, and never waiting longer than a
// couple of normal intervals.
func (m *Model) retryDelay() time.Duration {
	delay := 5 * time.Second
	for i := 1; i < m.failures && delay < time.Minute; i++ {
		delay *= 2
	}
	if cap := 2 * m.cfg.Interval; cap > 0 && delay > cap {
		delay = cap
	}
	if delay < 5*time.Second {
		delay = 5 * time.Second
	}
	return delay
}

// applyFetch folds a completed poll into the model, recording what changed.
// A failed poll never discards the data already on screen: a dashboard that
// blanks itself because GitHub hiccuped is worse than a slightly stale one.
func (m Model) applyFetch(msg fetchDoneMsg) (tea.Model, tea.Cmd) {
	if msg.seq != m.fetchSeq {
		// Answer to a superseded request: the mode or query has moved on.
		return m, nil
	}
	m.loading = false
	m.nextFetch = time.Now().Add(m.cfg.Interval)

	if msg.err != nil {
		m.failures++
		if gh.IsTransient(msg.err) {
			m.nextFetch = time.Now().Add(m.retryDelay())
			m.warn = gh.CleanMessage(msg.err.Error(), 160)
			// Only escalate to a hard error once it stops looking like a blip.
			if m.failures >= 3 {
				m.err = msg.err
			}
			return m, nil
		}
		m.err = msg.err
		m.warn = ""
		return m, nil
	}
	m.failures = 0
	m.warn = ""
	m.err = nil

	var prev []gh.PR
	if m.loaded {
		prev = m.prs
	}

	next := msg.res.PRs
	var verify []gh.PR
	if m.loaded {
		next, verify = m.holdVanished(prev, next, msg.res.Complete)
	}

	events := gh.Diff(prev, next, gh.DiffOpts{
		Now:          msg.res.FetchedAt,
		PrevComplete: m.lastComplete,
		Mode:         m.cfg.Mode,
		Viewer:       msg.res.Viewer,
	})
	m.record(events)

	selected := m.selectedKey()
	m.prs = next
	m.viewer = msg.res.Viewer
	m.rate = msg.res.RateLimit
	m.lastFetch = msg.res.FetchedAt
	m.lastComplete = msg.res.Complete
	m.loaded = true
	m.rebuild()
	m.restoreCursor(selected)

	if len(verify) > 0 {
		return m, m.verifyCmd(verify, m.fetchSeq)
	}
	return m, nil
}

// holdVanished keeps pull requests that dropped out of the search visible and
// returns the ones worth asking GitHub about. Nothing is reported as closed on
// the strength of absence alone, and nothing is carried indefinitely.
func (m *Model) holdVanished(prev, next []gh.PR, complete bool) (kept []gh.PR, verify []gh.PR) {
	present := make(map[string]bool, len(next))
	for _, p := range next {
		present[p.Key()] = true
	}
	for key := range m.absent {
		if present[key] {
			delete(m.absent, key) // it came back: a paging artifact
		}
	}

	kept = next
	for _, p := range gh.Vanished(prev, next) {
		key := p.Key()
		a := m.absent[key]
		if a == nil {
			a = &absence{}
			m.absent[key] = a
		}
		a.pr = p

		if !complete {
			// The search was cut short, so being absent means nothing at all.
			kept = append(kept, p)
			continue
		}

		a.misses++
		if a.misses > maxAbsentPolls {
			// Still open as far as we know, but persistently outside the
			// search. Drop it quietly rather than claiming it finished.
			delete(m.absent, key)
			continue
		}
		kept = append(kept, p)
		if !a.asked && p.ID != "" {
			a.asked = true
			verify = append(verify, p)
		}
	}
	return kept, verify
}

// verifyCmd asks GitHub what actually became of the missing pull requests.
func (m Model) verifyCmd(prs []gh.PR, seq int) tea.Cmd {
	client := m.cfg.Client
	ids := make([]string, 0, len(prs))
	for _, p := range prs {
		ids = append(ids, p.ID)
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		states, err := client.States(ctx, ids)
		return verifyDoneMsg{seq: seq, states: states, checked: prs, err: err}
	}
}

// applyVerify acts on the definitive answer: genuinely finished pull requests
// leave the list with an accurate "merged" or "closed", and ones still open
// were only ever a pagination artifact and are quietly kept.
func (m Model) applyVerify(msg verifyDoneMsg) Model {
	if msg.seq != m.fetchSeq {
		return m // the list it described has already been replaced
	}
	if msg.err != nil {
		// Let a later poll try again rather than concluding anything.
		for _, p := range msg.checked {
			if a := m.absent[p.Key()]; a != nil {
				a.asked = false
			}
		}
		return m
	}

	now := time.Now()
	finished := map[string]bool{}
	var events []gh.Event

	for _, p := range msg.checked {
		state, known := msg.states[p.ID]
		if !known || state == gh.StateOpen {
			// Still open: it never left. Keep it, and let the absence cap
			// retire it if it stays outside the search.
			continue
		}
		delete(m.absent, p.Key())
		finished[p.Key()] = true
		events = append(events, gh.ClosureEvent(p, state, now))
	}
	if len(finished) == 0 {
		return m
	}

	kept := make([]gh.PR, 0, len(m.prs))
	for _, p := range m.prs {
		if !finished[p.Key()] {
			kept = append(kept, p)
		}
	}
	selected := m.selectedKey()
	m.prs = kept
	m.record(events)
	m.rebuild()
	m.restoreCursor(selected)
	return m
}

// record files activity events and marks the pull requests they touch.
//
// Each event is marked at its own timestamp rather than at the moment it was
// filed. For a poll the two are the same, but a seeded event carries a time
// from before the dashboard opened, and the gutter dot means "changed in the
// last minute" — so only the seeded lines recent enough to deserve one get it.
func (m *Model) record(events []gh.Event) {
	if len(events) == 0 {
		return
	}
	for _, e := range events {
		if e.Key == "" {
			continue // the session marker names no pull request
		}
		m.changed[e.Key] = e.At
	}
	// Reading back through the feed should not be interrupted by whatever
	// lands mid-sentence. A cursor already at the top is left there, so an
	// unattended feed still follows along live; anywhere else it is pinned to
	// the line it was on and found again afterwards, which survives the
	// backfill arriving out of order as simple arithmetic would not.
	var held gh.Event
	anchored := false
	if m.eventsFocus && m.eventCursor > 0 {
		if e := m.selectedEvent(); e != nil {
			held, anchored = *e, true
		}
	}

	m.events = append(m.events, events...)
	gh.SortByTime(m.events)
	if len(m.events) > maxEvents {
		m.events = m.events[len(m.events)-maxEvents:]
	}

	if anchored {
		m.eventCursor = m.displayIndexOf(held)
	}
	m.clampEvents()
}

// displayIndexOf finds an event's position counting back from the newest,
// which is the order the pane draws in. A line that has fallen off the end of
// the backlog reports 0, putting the cursor back on the present.
func (m *Model) displayIndexOf(want gh.Event) int {
	ev := m.feedEvents()
	for i := len(ev) - 1; i >= 0; i-- {
		e := ev[i]
		if e.Kind == want.Kind && e.Key == want.Key && e.Text == want.Text && e.At.Equal(want.At) {
			return len(ev) - 1 - i
		}
	}
	return 0
}

// feedEvents is what the activity pane is currently showing. The backlog
// itself is never narrowed — hiding a pull request or switching mode does not
// un-happen what it did — so this is a view, rebuilt on demand.
func (m *Model) feedEvents() []gh.Event {
	q := strings.ToLower(strings.TrimSpace(m.feedFilter.Value()))
	if q == "" {
		return m.events
	}
	out := make([]gh.Event, 0, len(m.events))
	for _, e := range m.events {
		if matchesEvent(e, q) {
			out = append(out, e)
		}
	}
	return out
}

// matchesEvent searches everything an activity line actually shows: which pull
// request it was, what happened, and who did it.
func matchesEvent(e gh.Event, q string) bool {
	return strings.Contains(strings.ToLower(e.Text), q) ||
		strings.Contains(strings.ToLower(e.Actor), q) ||
		strings.Contains(strings.ToLower(e.Repo), q) ||
		strings.Contains(strconv.Itoa(e.Number), q)
}

// feedFiltered reports whether the pane is showing less than the whole record.
func (m *Model) feedFiltered() bool {
	return strings.TrimSpace(m.feedFilter.Value()) != ""
}

// clampEvents keeps the feed cursor and its window inside the backlog.
func (m *Model) clampEvents() {
	h := m.eventRowCount()
	n := len(m.feedEvents())
	if m.eventCursor < 0 {
		m.eventCursor = 0
	}
	if m.eventCursor >= n {
		m.eventCursor = max(0, n-1)
	}
	if m.eventCursor < m.eventTop {
		m.eventTop = m.eventCursor
	}
	if m.eventCursor >= m.eventTop+h {
		m.eventTop = m.eventCursor - h + 1
	}
	if maxTop := max(0, n-h); m.eventTop > maxTop {
		m.eventTop = maxTop
	}
	if m.eventTop < 0 {
		m.eventTop = 0
	}
}

// moveEvent walks the feed cursor; positive is further back in time, which is
// downwards on screen because the newest event is drawn at the top.
func (m *Model) moveEvent(delta int) {
	if len(m.feedEvents()) == 0 {
		return
	}
	m.eventCursor += delta
	m.clampEvents()
}

// selectedEvent is the feed line under the cursor, or nil when the feed is
// empty or not being navigated.
func (m *Model) selectedEvent() *gh.Event {
	ev := m.feedEvents()
	if !m.eventsFocus || m.eventCursor < 0 || m.eventCursor >= len(ev) {
		return nil
	}
	return &ev[len(ev)-1-m.eventCursor]
}

// leaveEvents drops out of the feed and returns it to the live view, so the
// pane never sits frozen on old activity once it stops being read.
func (m *Model) leaveEvents() {
	m.eventsFocus = false
	m.feedFiltering = false
	m.feedFilter.Blur()
	m.feedFilter.Reset()
	m.eventCursor, m.eventTop = 0, 0
	// The pane shrinks back on the way out, so the list gets its rows again.
	m.clampScroll()
}

func (m *Model) selectedKey() string {
	if p := m.selected(); p != nil {
		return p.Key()
	}
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return "repo:" + m.rows[m.cursor].repo
	}
	return ""
}

func (m *Model) restoreCursor(key string) {
	if key == "" {
		m.clampScroll()
		return
	}
	for i, r := range m.rows {
		if r.isRepo() {
			if "repo:"+r.repo == key {
				m.cursor = i
				m.clampScroll()
				return
			}
			continue
		}
		if r.pr.Key() == key {
			m.cursor = i
			m.clampScroll()
			return
		}
	}
	m.clampScroll()
}

// selected returns the PR under the cursor, or nil on a repo header.
func (m *Model) selected() *gh.PR {
	if m.cursor < 0 || m.cursor >= len(m.rows) {
		return nil
	}
	return m.rows[m.cursor].pr
}

func (m *Model) pruneFresh() {
	for k, at := range m.changed {
		if m.now.Sub(at) > freshFor {
			delete(m.changed, k)
		}
	}
}

// isHidden reports whether a pull request has been dismissed, either on its
// own or because its whole repository was.
func (m *Model) isHidden(p gh.PR) bool {
	return m.hiddenPRs[p.Key()] || m.hiddenRepos[p.Repo]
}

// orgHiddenCount is how many pull requests the organization filter holds back.
// It is reported separately from hiddenCount because the two are different
// controls: O manages organizations, h and H manage individual dismissals.
// Counting them together made the header claim hidden items that H could not
// reveal.
func (m *Model) orgHiddenCount() int {
	var n int
	for _, p := range m.prs {
		if m.hiddenOrgs[p.Org()] {
			n++
		}
	}
	return n
}

// hiddenCount is how many pull requests have been dismissed with h, and so is
// exactly what H reveals.
func (m *Model) hiddenCount() int {
	var n int
	for _, p := range m.prs {
		if m.hiddenOrgs[p.Org()] {
			continue // already accounted for by the organization filter
		}
		if m.isHidden(p) {
			n++
		}
	}
	return n
}

func (m *Model) isFresh(key string) bool {
	at, ok := m.changed[key]
	return ok && m.now.Sub(at) <= freshFor
}

// rebuild recomputes the visible rows from the current PR set.
func (m *Model) rebuild() {
	prs := m.visiblePRs()

	if !m.grouped {
		m.sortPRs(prs)
		m.rows = make([]row, 0, len(prs))
		for i := range prs {
			p := prs[i]
			m.rows = append(m.rows, row{repo: p.Repo, pr: &p, hidden: m.isHidden(p)})
		}
		m.clampScroll()
		return
	}

	byRepo := map[string][]gh.PR{}
	for _, p := range prs {
		byRepo[p.Repo] = append(byRepo[p.Repo], p)
	}
	repos := make([]string, 0, len(byRepo))
	for r := range byRepo {
		repos = append(repos, r)
	}
	// Most urgent repo first, then the one that moved most recently.
	sort.Slice(repos, func(i, j int) bool {
		a, b := byRepo[repos[i]], byRepo[repos[j]]
		if ua, ub := minStatus(a), minStatus(b); ua != ub {
			return ua < ub
		}
		if ta, tb := latestUpdate(a), latestUpdate(b); !ta.Equal(tb) {
			return ta.After(tb)
		}
		return repos[i] < repos[j]
	})

	m.rows = m.rows[:0]
	for _, repo := range repos {
		group := byRepo[repo]
		m.sortPRs(group)
		fresh := false
		for _, p := range group {
			if m.isFresh(p.Key()) {
				fresh = true
				break
			}
		}
		m.rows = append(m.rows, row{
			repo:   repo,
			count:  len(group),
			urgent: minStatus(group),
			fresh:  fresh,
			hidden: m.hiddenRepos[repo],
		})
		if m.collapsed[repo] {
			continue
		}
		for i := range group {
			p := group[i]
			m.rows = append(m.rows, row{repo: repo, pr: &p, hidden: m.isHidden(p)})
		}
	}
	m.clampScroll()
}

// visiblePRs applies the draft toggle and the text filter.
func (m *Model) visiblePRs() []gh.PR {
	q := strings.ToLower(strings.TrimSpace(m.filter.Value()))
	out := make([]gh.PR, 0, len(m.prs))
	for _, p := range m.prs {
		if m.hiddenOrgs[p.Org()] {
			continue
		}
		if !m.showHidden && m.isHidden(p) {
			continue
		}
		if m.hideDrafts && p.IsDraft {
			continue
		}
		if q != "" && !matches(p, q) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func matches(p gh.PR, q string) bool {
	if strings.Contains(strings.ToLower(p.Title), q) ||
		strings.Contains(strings.ToLower(p.Repo), q) ||
		strings.Contains(strings.ToLower(p.Author), q) ||
		strings.Contains(strconv.Itoa(p.Number), q) ||
		strings.Contains(strings.ToLower(p.Status().Label()), q) {
		return true
	}
	for _, l := range p.Labels {
		if strings.Contains(strings.ToLower(l.Name), q) {
			return true
		}
	}
	for _, r := range p.Reviewers {
		if strings.Contains(strings.ToLower(r.Login), q) {
			return true
		}
	}
	return false
}

func (m *Model) sortPRs(prs []gh.PR) {
	sort.SliceStable(prs, func(i, j int) bool {
		a, b := prs[i], prs[j]
		switch m.sortBy {
		case sortUpdated:
			return a.UpdatedAt.After(b.UpdatedAt)
		case sortAge:
			return a.CreatedAt.Before(b.CreatedAt)
		case sortComments:
			if a.Comments() != b.Comments() {
				return a.Comments() > b.Comments()
			}
		case sortChurn:
			ca, cb := a.Additions+a.Deletions, b.Additions+b.Deletions
			if ca != cb {
				return ca > cb
			}
		default:
			if sa, sb := a.Status(), b.Status(); sa != sb {
				return sa < sb
			}
		}
		return a.UpdatedAt.After(b.UpdatedAt)
	})
}

func minStatus(prs []gh.PR) gh.Status {
	best := gh.StatusDraft
	for _, p := range prs {
		if s := p.Status(); s < best {
			best = s
		}
	}
	return best
}

func latestUpdate(prs []gh.PR) time.Time {
	var t time.Time
	for _, p := range prs {
		if p.UpdatedAt.After(t) {
			t = p.UpdatedAt
		}
	}
	return t
}
