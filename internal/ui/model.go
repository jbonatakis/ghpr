package ui

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/jbonatakis/ghpr/internal/config"
	"github.com/jbonatakis/ghpr/internal/gh"
)

// freshFor is how long a PR stays highlighted after we notice it changed.
const freshFor = 60 * time.Second

// maxEvents caps the activity feed's backlog.
const maxEvents = 200

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

	spin   spinner.Model
	now    time.Time
	width  int
	height int
}

// New builds the initial model.
func New(cfg Config) Model {
	ti := textinput.New()
	ti.Prompt = "filter: "
	ti.Placeholder = "repo, title, label…"
	ti.PromptStyle = stBold
	ti.CharLimit = 80

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
		spin:        sp,
		now:         now,
		nextFetch:   now,
		loading:     true,
		fetchSeq:    1,
		width:       100,
		height:      30,
	}
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
		Grouped:        m.grouped,
		HideDrafts:     m.hideDrafts,
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

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(m.fetchCmdSeq(m.fetchSeq), tickCmd(), m.spin.Tick)
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
		// Only animate while a poll is in flight; an idle dashboard should not
		// repaint ten times a second for hours on end.
		if !m.loading {
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
	})
	m.record(events, msg.res.FetchedAt)

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
	m.record(events, now)
	m.rebuild()
	m.restoreCursor(selected)
	return m
}

// record files activity events and marks the pull requests they touch.
func (m *Model) record(events []gh.Event, at time.Time) {
	if len(events) == 0 {
		return
	}
	for _, e := range events {
		m.changed[e.Key] = at
	}
	m.events = append(m.events, events...)
	if len(m.events) > maxEvents {
		m.events = m.events[len(m.events)-maxEvents:]
	}
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
