# ghpr

A terminal dashboard for the GitHub pull requests you care about. Leave it open
on a second monitor: it polls GitHub, groups your PRs by repository, and shows
what each one is waiting on — review state, CI, unresolved comments, conflicts
and age — refreshing itself as things change.

```
ghpr octo-dev · authored · 11 open · 5 need work                                 ● 31s  4725/5000 pts/hr
       # TITLE                                           STATUS    CHECKS  REV    CMT         DIFF   AGE
────────────────────────────────────────────────────────────────────────────────────────────────────────
  ▾ acme/starfield (3)  CHANGES
▸    #44 Introduce structured logging across             CHANGES   ✗ 2/3   ±      31!      +398/-0    9w
     #96 feat(scheduler): priority lanes for long-runni… FAILING   ✗ 3/4   ·      17!    +1.1k/-90   20h
     #84 Move connection pooling behind a configurable … DRAFT     ✓ 1/1   ·        2    +2.3k/-18    4d
  ▾ acme/design-docs (1)  FAILING
      #9 Split the                                       FAILING   ✗ 1/2   ·      40!      +509/-0   18w
```

## Install

```sh
go install github.com/jbonatakis/ghpr@latest
```

Or from a clone:

```sh
go build -o ghpr . && ./ghpr
```

## Auth

`ghpr` uses, in order: `$GITHUB_TOKEN`, `$GH_TOKEN`, then `gh auth token`. If you
already use the GitHub CLI, nothing to do. The token needs `repo` scope to see
private pull requests.

## Usage

```sh
ghpr                                  # your open PRs, polled every 30s
ghpr -interval 15s                    # poll faster
ghpr -mode review-requested           # PRs waiting on your review
ghpr -query 'org:acme'                # add any GitHub search qualifier
ghpr -once                            # plain-text snapshot, no TUI
```

| flag | default | meaning |
| --- | --- | --- |
| `-interval` | `30s` | how often to poll (minimum 5s) |
| `-max` | `200` | most PRs to track |
| `-mode` | `authored` | `authored`, `review-requested`, or `involved` |
| `-query` | – | extra search qualifiers, e.g. `org:acme` |
| `-api` | – | GraphQL endpoint, for GitHub Enterprise Server |
| `-config` | – | print the config file path and exit |
| `-seed` | `1h` | fill the activity feed in from this far back at startup (`0` starts it empty) |
| `-links` | `true` | clickable pull request references (`-links=false` to disable) |
| `-once` | – | print a snapshot and exit — good for scripts and cron |
| `-why-seed` | – | account for the startup backfill pull request by pull request, and exit |

## Keys

| key | action |
| --- | --- |
| `↑`/`k`, `↓`/`j` | move | 
| `pgup`/`pgdn`, `g`/`G` | page, jump to top/bottom |
| `enter`/`o` | open in browser (or fold, on a repo row) |
| `y` | copy the PR url |
| `d`/`tab` | toggle the detail pane |
| `space` | fold the repo under the cursor (remembered between sessions) |
| `h` | hide the PR or repo under the cursor — press again to bring it back |
| `H` | show hidden items so you can unhide them |
| `z` | fold or unfold every repo |
| `t` | toggle grouping by repo |
| `e` | activity feed: show it, again to step into it, again to hide it |
| `s` | cycle sort: attention → updated → oldest → comments → diff size |
| `m` | cycle mode: authored → review-requested → involved |
| `D` | show or hide drafts |
| `O` | choose which organizations appear |
| `/` | filter the list — or the activity feed, when the feed has the keys |
| `r` | refresh now |
| `esc` | back out — the feed's filter, then the feed, then the list's filter |
| `?` | help |
| `q` | quit (`ctrl+c` too; `esc` never quits) |

## Organizations

Press `O` for the organization picker. It lists every org you have pull
requests in, with a checkbox each:

```
 ghpr — organizations

   Choose which organizations appear in the dashboard. Saved to your config.

 ▸ [✓] octo-dev                    4 pull requests
   [✓] acme                  7 pull requests

   11 shown · 0 hidden

   space   show or hide
   o       only this one
   a       show all
   n       hide all
   enter   save
   esc     cancel
```

`space` toggles one, `o` narrows to just the org under the cursor, and `enter`
saves. The choice persists across restarts. For a single repo or PR rather than
a whole org, use `h` — see [Hiding things](#hiding-things).

The file stores *hidden* orgs rather than visible ones, so an org you join later
turns up on its own instead of silently going missing. Your sort order, grouping,
draft visibility and mode are saved alongside it:

```jsonc
// ~/.config/ghpr/config.json   (or $XDG_CONFIG_HOME/ghpr/config.json)
{
  "hiddenOrgs": ["some-org"],
  "hiddenRepos": ["some-org/noisy-repo"],
  "hiddenPRs": ["some-org/repo#123"],
  "collapsedRepos": ["some-org/big-repo"],
  "mode": "authored",
  "sort": "attention",
  "grouped": true,
  "hideDrafts": false
}
```

Run `ghpr -config` to print the path. An explicit flag still wins for that run,
so `ghpr -mode involved` overrides the saved mode without changing the file.

## Hiding things

`h` dismisses whatever the cursor is on — a single pull request, or a whole
repository if you are on its header. It disappears from the list and stays gone
across restarts.

`H` reveals everything you have hidden, marked `HIDDEN` and dimmed. While
revealed, `h` puts an item back. Pressing `h` on a pull request that is only
hidden because its *repository* is hidden unhides the repository, which is
almost always what you meant.

The header keeps count, so nothing is ever withheld silently — and the two
filters are counted separately, because `H` only reveals what `h` dismissed:

```
… · 7 open · 4 in hidden orgs                          # press O to change these
… · 6 open · 4 in hidden orgs · 1 hidden               # press H to reveal this one
… · 6 open · 4 in hidden orgs · 1 hidden (shown)
```

Revealing is deliberately **not** persisted — hidden should mean hidden when you
next open the dashboard. Everything else is: hidden pull requests, hidden
repositories and folded repositories all live in the same config file.

This is separate from `O`, which filters by organization. Use `O` for whole
accounts you never care about, `h` for a specific noisy PR or repo.

## What the columns mean

**STATUS** is the one thing the PR is waiting on, in precedence order:

| | |
| --- | --- |
| `READY` | approved, checks green — merge it |
| `CHANGES` | a reviewer requested changes |
| `FAILING` | CI is red |
| `CONFLICT` | needs a rebase or merge |
| `COMMENTS` | unresolved review threads |
| `REVIEW` | waiting on reviewers |
| `DRAFT` | still a draft |

**CHECKS** is `passed/total` for the head commit, with the glyph carrying the
rollup (`✓` green, `✗` red, `•` running, `—` no checks). Skipped and neutral
checks are excluded from the total.

**REV** is the review decision: `✓` approved, `±` changes requested, `◷` review
requested but not yet given, `·` nobody asked yet.

**CMT** counts conversation comments plus every comment inside a review thread.
A trailing `!` means some threads are still unresolved.

**AGE** is time since the PR opened; **IDLE** is time since it last changed, and
turns amber past a week.

## Staying current

The dashboard polls on a fixed interval and diffs each snapshot against the last
one. Anything that moved — new comments, a check starting or finishing, an
approval, a push, a fresh conflict, a draft going ready — is marked with an
amber `●` in the left gutter for a minute, echoed on its repo header, and
recorded in the activity feed (`e`), which names who did it:

```
 11:47:04  data-warehouse#1312                             → review requested     kim-rivera
 11:47:04  design-docs#9                                   + opened               octo-dev
 11:47:04  retention-policy-enforcer-export-history#99     ★ approved             priya-shah
 11:47:04  starfield#96                                    ↑ new commits          octo-dev
 11:47:04  starfield#96                                    @ mentioned you        priya-shah
 11:47:04  starfield#84                                    ◷ review requested
 11:47:04  sensor-presence-collector#828                   ~ checks passing
 11:47:04  sensor-presence-collector#828                   » 1 new comment        morgan-bell
```

The feed is a session-wide record on purpose: it is **not** scoped to the
current mode, and the hide filters do not narrow it. Switching mode or
dismissing a pull request changes what you are working on, not what already
happened.

Reviews name the exact reviewer, pushes name the committer, and comments name
the author. Checks show no one, because no person is behind them. A comment left
inside a review thread carries no author in the cheap form of the query, so it
is attributed to the review that delivered it — and left blank rather than
guessed if even that is unavailable. All of this costs nothing extra: the query
is still 2 rate-limit points.

### Things aimed at you

Two lines are in the feed because of who the change is *for* rather than what it
is, and both cost nothing extra — they read fields the query was already paying
for, so it is still 2 rate-limit points.

**`◷ review requested`** — you were added as a reviewer on a pull request that
was already on screen. The actor column is left empty because GitHub will not
say who asked without a second, costlier query, and a guess is worse than a
blank. A re-request after you have already reviewed counts, which is the case
the review column alone hides: it keeps showing your `✓` while the request sits
underneath it. Requests aimed at a *team* are not read as yours — knowing the
team was asked says nothing about whether you are on it. In `review-requested`
mode this line does not appear, because being asked is what puts the pull
request in the list at all, and it arrives as `→ review requested` instead.

**`@ mentioned you`** — someone wrote your handle. `ghpr` scans the text it
already fetches: the description, the last three conversation comments, and each
reviewer's most recent review body. That covers the ordinary "@you can you look
at this" and nothing more — a mention buried in an older comment, or inside a
review thread, is not visible without asking GitHub for far more text than the
dashboard is worth. `@you-and-someone-else` and `@org/your-team` are not you,
and neither is `notify@you.example`. Quoting your own handle does not ping you.

A mention takes the place of the `» N new comments` line rather than sitting
next to it: two rows a second apart on the same pull request would say one thing
twice, and the quieter of the two would be the one left underneath. A mention
inside a *review* keeps the verdict, though — `★ changes requested` and
`@ mentioned you` are two different facts.

A description that names you is dated by when the pull request was opened, not
by when it was last touched. Dating it by the update time would re-announce the
same standing mention on every push and every comment for the life of the branch.

### Opening onto the last hour

A dashboard you have just launched used to start with an empty feed and tell
you nothing about the hour you spent away from it. It now fills itself in from
the first poll — `-seed 1h` by default, `-seed 0` to start blank, and a `seed`
key in the config file if you would rather not type it every time:

```
────────── activity ───────────────────────────────────────────────────────────
 10:30:00                                                  · ghpr started
 10:27:00  starfield#44                                    @ mentioned you        dana-quill
 10:21:00  design-docs#9                                   ~ checks passing
 10:16:00  starfield#44                                    » new comment          riley-shaw
 10:08:00  starfield#44                                    ★ changes requested    dana-quill
 10:04:00  design-docs#9                                   ↑ new commits
 10:03:00  design-docs#9                                   + opened               sam-okafor
```

The two searches take a while — far longer than a poll — so the feed says what
it is doing rather than sitting on the idle message:

```
────────── activity  filling in… ─────────────────────────────────────────────
 ⠋ looking back over the last 720h…
```

The spinner keeps turning until they answer, which is well after the first poll
has landed. When they do, an empty feed distinguishes the three outcomes that
otherwise look identical: `nothing in the last 720h` for a genuinely quiet
window, `could not look back over the last 720h` when the searches failed, and
the plain `watching for changes…` when no backfill was asked for at all.

If the backfill finds anything, the pane opens itself — a feed filled in behind
a closed pane answers a question you cannot see it answering. It opens on the
first stop, not the second, so the arrow keys still move the list; press `e` to
step in and scroll.

`· ghpr started` is the boundary. Above it is the ordinary feed — things ghpr
watched change between two of its own polls. Below it is reconstruction: the
same one search, read for the timestamps GitHub attaches to what it returns.
Both are real, but they are not the same kind of claim, and the line says so.

It costs extra, once. The searches run at launch and are far heavier than a
poll — nested thread comments multiply quickly — so pages are small and the
work is done before the dashboard settles. Each is best-effort: if one fails,
what the other found is still shown. Across a session left open for hours
it is noise; `-why-seed` prints what it actually cost on your account, and
`-seed 0` skips it entirely.

The backfill is **not** the polling query, and it is **not scoped to the
current mode**. It is a pair of deliberately expensive searches that run once
at launch and never again, so the thirty-second poll stays at 2 points while
the backfill gets to see an actual month:

```
is:pr archived:false involves:@me          updated:>=2026-08-28T07:00:00+00:00
is:pr archived:false review-requested:@me  updated:>=2026-08-28T07:00:00+00:00
is:pr archived:false involves:@me          updated:2026-08-28T01:00..2026-08-28T07:00
is:pr archived:false review-requested:@me  updated:2026-08-28T01:00..2026-08-28T07:00
…
```

- **Everything you touched, not just the current mode.** The feed spans every
  mode by design — switching mode does not un-happen what it saw — so filling
  it from whichever one happens to be selected contradicts the thing it is. A
  dashboard opened on `authored` would reconstruct a morning with everything
  you reviewed left out of it. Two searches because GitHub cannot express the
  union: `involves:@me` covers authoring, commenting, assignment and mentions,
  but not a review merely requested of you and not yet acted on. They overlap,
  and anything found by both is seeded once.
- **Pull requests that already finished.** Neither search says `is:open`, so
  what was merged or closed inside the window comes back too, with `✔ merged`
  and `× closed` lines of its own — for anyone who ships, that is most of the
  activity there was.
- **Only what could contribute.** Every search is bounded by `updated:`,
  because a pull request untouched inside the window has nothing to add to the
  seed and fetching it is pure waste. That bound is what pays for the wider
  scope.
- **Divided narrow-first and run four at a time.** The window is cut into
  chunks that start at half an hour and widen going back — a day becomes
  `30m, 1h30, 4h30, 13h30, 4h`; a month, eight windows ending in a fortnight.

  Narrow first because a window's latency is set by how many *pages* it needs,
  and pages within one search are sequential: the cursor for the next is in the
  answer to the last. Half an hour of activity is nearly always one page; five
  days of it can be five round trips of a query heavy enough that each is slow.
  Since windows are released in order, everything waits on the newest one, so
  making that one as cheap as possible is what decides how long the feed sits
  empty after launch.

  Widening because the count has to stay logarithmic: an even split fine enough
  to make the first window half an hour would cut a month into 1,440 searches.
  This gives sixteen — about a third more than equal chunks, and the extra ones
  are all at the old end, which is exactly where the backlog cap tends to
  abandon them unread.

  They run newest-window-first across a pool of four, and each window lands in
  the feed as it completes, so the most recent activity is readable while the
  rest is still being gathered. **In window order, not finishing order** — the pool
  answers out of sequence, and filing a late-finishing early window would drop
  newer activity in above whatever you were already reading. Because a window
  bounds `updated`, and a pull request's newest event is never later than its
  `updated`, window *n* can hold nothing newer than where window *n-1* begins:
  released in order, the top of the feed settles as soon as the first window
  lands and then stays put while everything older arrives underneath it. GitHub asks that requests for one user be made serially and
  answers a burst with a secondary rate limit, so four is a compromise rather
  than a maximum.
- **Stopped as soon as the backlog is full.** The feed keeps the newest 500
  events; because the chunks arrive newest-first, once that many are filed
  everything still queued is older and would be trimmed away on arrival. The
  remaining searches are abandoned — on a busy month that is a third of them.
- **Comments inside review threads**, with the dates that let them be placed.
  Where review happens inline rather than in the conversation tab, this is the
  discussion — the polling query can only count it.
- **Twenty conversation comments** per pull request rather than three,
  **every review** rather than the latest per reviewer, and **twenty commits**
  rather than the head alone, so a day of pushes reads as a day of pushes.

Two things still cannot be seeded at any window, because nothing in any
response dates them:

- `! now conflicting` — `mergeable` is a current state with no timestamp.
- `◷ review requested` — `reviewRequests` carries no date; only GitHub's
  timeline API has one, and that is a query per pull request.

If the feed comes up thinner than you expected, `-why-seed` says why, and is
the fastest way to tell a genuinely quiet month from activity the query cannot
see:

```sh
ghpr -why-seed -seed 720h
```

It prints every timestamp the seed reads for each pull request, whether that
one landed inside the window, and how much of the conversation was never
fetched to be dated at all — ending with the arithmetic:

```
28 events seeded, from 11 of 11 pull requests
out of reach: 14 older conversation comments, 68 review-thread comments
```

Two very different diagnoses come out of that. If the lines say `outside`, a
wider `-seed` helps. If they say `not reported by the API`, or the out-of-reach
tally dwarfs what was seeded, the window is not the constraint and widening it
will change nothing.

Nothing is guessed to fill those gaps: every seeded line has an API timestamp
behind it, and what cannot be dated is simply absent. Seeded comments are dated
one by one rather than tallied as `3 new comments`, because unlike a poll — which
only knows the difference between two totals — the seed has a time and an author
for each.

The gutter `●` still means *"changed in the last minute"*, so seeded history
does not light the whole list up at launch; only the parts of it that really are
that recent. Seeding happens once per session, not once per mode: the feed spans
every mode, and the searches overlap.

### Reading back through the feed

`e` has three stops: show the feed, step into it, put it away.

The first stop is a pane you watch out of the corner of an eye. The arrow keys
still belong to the pull request list, exactly as they always did — so the
title bar says what you are missing and how to get at it:

```
────────── activity  +39 more · e to scroll ───────────────────────────────────
```

Press `e` again and the feed takes the keys. It also grows into whatever room
the list is not using, because eight rows is right for glancing at and far too
small for reading a month back through:

```
────────── activity  4/47 ─────────────────────────────────────────────────────
 10:36:10  starfield#130                                   → review requested     sam-okafor
 10:35:33  design-docs#9                                   ✔ merged
 10:34:56  starfield#84                                    ! now conflicting
▸10:34:19  starfield#84                                    » 3 new comments       riley-shaw
```

`↑`/`↓` scroll, `pgup`/`pgdn` page, `g` and `G` jump to the newest and oldest,
`enter` opens the pull request the selected line names, `y` copies its URL, `/`
filters, and `esc` hands the keys back to the list and shrinks the pane again. The newest
event is at the top, so `g` and `G` run with the screen rather than with the
clock. The list keeps at least three rows throughout, so you never lose your
place in it.

`/` inside the feed searches the activity rather than the pull requests —
repository, number, what happened, and who did it:

```
────────── activity  1/3 · filtered from 8 ────────────────────────────────────
▸15:03:20  design-docs#12                             @ mentioned you        dana-quill
 14:58:20  starfield#44                               ★ changes requested    dana-quill
 14:57:20  starfield#44                               » new comment          dana-quill
```

The two filters are separate boxes and stay out of each other's way: narrowing
the feed leaves the list exactly as it was, and the list's filter still never
touches the feed. Neither narrows the backlog itself — these are views onto a
record that stays whole. `esc` backs out one step at a time, clearing the
filter first and handing the keys back second, and leaving the feed drops the
filter along with the scroll position.

With the pane merely open rather than stepped into, `/` still filters the list,
which is what you want with the feed sitting beside you.

A poll that lands while you are reading does not move the line you are on: the
feed only follows along live while the cursor is at the top, which is where a
feed nobody is reading sits. Leaving with `esc` returns it to the live view.
The backlog holds the last 500 events of the session.

The pull request number is never the part that gets truncated; long repository
names are shortened around it.

References in the feed — and the `#number` column in the list — are clickable:
cmd-click (ctrl-click on Linux) opens the pull request. This uses the OSC 8
escape sequence, which iTerm2, WezTerm, Kitty, Windows Terminal, VS Code and
GNOME Terminal understand and others quietly ignore. The sequence carries no
width, so the layout is identical either way; `-links=false` turns it off.

Each link points at the pull request's own URL rather than one assembled from
`github.com`, so they work against GitHub Enterprise too.

The dot means *"this changed while you were looking away"*, nothing more; it
clears itself. Identical polls never raise one. `?` lists every marker.

### Why it never guesses that a PR opened or closed

A large search is paginated, and the pages are fetched a few seconds apart. If
any pull request is updated in that window the results reorder, and one sitting
on a page boundary can be fetched in neither page. It looks like it vanished.

Treating absence as closure produced exactly the wrong answer: a PR that was
open the whole time reported `merged or closed`, then `opened` again a poll
later. So `ghpr` does not infer it. When a pull request drops out of the search
it stays on screen, and `ghpr` asks GitHub directly what became of it:

- still `OPEN` — a paging artifact. Nothing is reported and it keeps its place.
- `MERGED` or `CLOSED` — it leaves the list, and the feed says which (`✔ merged`
  or `× closed`) rather than hedging.

The look-up is one extra query costing a single rate-limit point, and only runs
when something actually disappears. If the search was cut short by `-max`, an
absent PR proves nothing and is never questioned in the first place. A pull
request that stays outside the search for several polls while remaining open —
a withdrawn review request, say — is retired quietly after a few polls rather
than carried forever.

Switching mode (`m`) starts a new search while the previous one may still be in
flight, and the searches are not equally fast — `review-requested` paginates
over other people's repositories and takes seconds, `authored` usually answers
at once. Every request is therefore stamped, and an answer to a superseded one
is discarded rather than filling the list with pull requests from the mode you
just left.

The same reasoning applies in reverse. A pull request *appearing* is not
evidence that it was opened; it may just have been missed by an earlier poll.
An appearance is therefore reported as one of three things:

- `+ opened` — it was created within the last hour, so it really is new.
- `→ review requested` — it is older, but was touched within the last fifteen
  minutes and the previous poll was a complete picture of the search. Being
  added as a reviewer bumps a pull request's updated time, so this catches
  arrivals into the `review-requested` queue. (`now involves you` and
  `now listed` are the equivalents in the other modes.)
- nothing — an old, untouched pull request that merely surfaced in our view.

Those windows are what separate the cases: the pull requests that once appeared
spuriously had last been touched between 26 and 111 days earlier.

### About that rate limit

GitHub bills the GraphQL API in **points per hour, not requests**. You get 5,000
points an hour, and each query is scored by how much data it asks for. `ghpr`
asks for one page of pull requests with their checks, reviews and comment
threads, which comes to about **3 points per poll**.

So the 30s default spends roughly `120 polls × 3 = 360 points/hour` — about 7% of
the budget. Even `-interval 5s` only reaches ~2,200/hour. The header shows what
is left (`4966/5000 pts/hr`), and `?` estimates your current burn rate.

The window is not truly push-based — GitHub has no PR event stream for this —
but at 15–30s it feels live.

### When GitHub misbehaves

Search queries that pull in review threads and check runs are expensive, and
GitHub answers a query that runs too long with a `502`. Large result sets — a
`review-requested` search across dozens of other people's repositories — are
where this shows up.

`ghpr` handles it rather than falling over:

- Pages are kept small (25 PRs) so a single query stays well inside GitHub's
  internal timeout.
- `502`/`503`/`504`/`429`, network blips and GraphQL `TIMEDOUT` errors are
  treated as **transient**: the dashboard keeps showing the last good data,
  marks the header `● retry 6s`, and backs off (5s, 10s, 20s…) until it works.
- Only after three consecutive failures does it show a hard `error:` line.
  Auth and permission failures skip the retries and surface immediately, since
  those will not fix themselves.
- Upstream messages are flattened to a single line before display. A multi-line
  HTML error page used to grow the footer and push the list off the screen.

If a pull request has more review threads than one page returns, its comment
count is shown as `31+` rather than silently undercounting.

## Development

```sh
go test ./...                                            # replays a captured API response
PREVIEW=1 go test ./internal/ui -run TestPreview -v       # render a frame to stdout
```

`internal/gh/testdata/search_authored.json` is a real GraphQL response, so the
parser, status rules and layout are all exercised against genuine data. To
re-capture it, dump the query and POST it to `api.github.com/graphql`.

`-api` also makes the binary easy to drive against a local replay server:

```sh
GITHUB_TOKEN=fake ./ghpr -api http://127.0.0.1:8791/graphql -once
```
