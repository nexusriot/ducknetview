# Changelog

All notable changes to ducknetview. Versions are the value of
`internal/version/version.go`, which the Makefile, `build-deb.sh` and
`DEBIAN/control` all read.

## 0.5.1 — 2026-10-12

A documentation audit of the two large rounds that preceded it, plus the two
code fixes it turned up. No new features.

### The help overlay was lying

0.4.0 made the key bindings configurable and gave the overlay a footer saying
"Keys shown are your own bindings". Fifteen of its thirty-six rows still
hardcoded their key, so somebody who rebound `p`, `z`, `b`, `l` or a dozen
others was shown the default and told it was theirs. Every row now reads from
the keymap, and a test rebinds every action to an unmistakable key and asserts
the overlay shows it — which fails against the old code.

### `n` / `N` are rebindable

Match navigation was the only pair of keys the handler understood that the
keymap did not, which quietly made "rebind any action" untrue. They are now
`search-next` and `search-prev`.

### Documentation

- **New `Metrics` section in the README**: all nineteen exported series with
  their types and labels, which were previously named nowhere — awkward for a
  feature whose entire point is being scraped. It marks the nine that only a
  running session can report, and gives the alert rule for
  `ducknetview_last_poll_age_seconds`, the one that reveals a monitor which is
  up but no longer working. The man page gained a shorter `METRICS` section.
- **New event-kind table**: the sixteen `kind` values, with the level each is
  raised at and what raises it. These are what `--log-events` writes, what
  `--on-alert` receives in `DNV_EVENT_KIND` and what labels
  `ducknetview_events_total` — so they are what a hook or an alert rule matches
  on, and none of them were documented.
- **Corrected**: `listener_up` escalates to alert level when the listener is
  **off-baseline**, not when it is exposed off-host. The exposure is carried in
  the event's detail; the escalation is the baseline. The roadmap repeated the
  same mistake from the other direction, claiming nothing fires on baseline
  drift at all — it now describes the real gap, which is narrower: drift that
  was already present when the session started is never reported, because the
  first poll is deliberately the baseline.
- `--from`'s own `--help` line did not mention `ssh://[user@]host`, so the
  flag's most capable mode was invisible to anyone who only ran `--help`.
  `--daemon` now mentions `SIGHUP` there too.
- The project layout had not caught up with `--check-config` or the exporter.
- Two new guards in `docs_test.go`: every exported metric must appear in the
  README's metrics table, and every event kind the `ui` package can record must
  appear in the event-kind table. These are the two interfaces other software
  is written against, and both are easy to extend without remembering a table
  exists.

## 0.5.0 — 2026-10-11

Tier 1 of the 0.4.0 roadmap: the three loose ends that round left.

### The exporter serves what the session already knows

`--metrics-listen` used to re-sample the host on every scrape — correct, but
throwing away everything a running session is already holding. The handler now
asks the update loop for a copy of its state (a `StatsRequest` carrying a reply
channel, because the model is owned by that loop and a mutex over it would have
to be taken by every renderer too) and renders from that. Scrapes went from
roughly a second to sub-millisecond, and the scrape gained what only a running
session can report:

- `ducknetview_last_poll_age_seconds` — **the one metric that can reveal a
  monitor which is up but no longer polling**, which otherwise looks healthy in
  every other series here. `-1` before the first poll, so "never" is
  distinguishable from "just now".
- `ducknetview_polls_total`, `ducknetview_session_uptime_seconds`,
  `ducknetview_paused`, `ducknetview_build_info{version=…}`
- `ducknetview_session_bytes_total{direction=…}` — the session's accounted
  traffic
- `ducknetview_events_total{kind=…}` and `ducknetview_alerts_total`, counted
  rather than derived from the 500-entry ring a long session overflows
- `ducknetview_alert_hook_failures_total`
- `ducknetview_remote_up{host=…}` for a live remote session, absent locally
  rather than exported as a meaningless constant

The host metrics are unchanged, so an existing dashboard keeps working — there
is a test asserting the live scrape is a superset of the sampled one. A session
that cannot answer within two seconds falls back to sampling, which is slower
but always answerable. The TUI's own `--metrics-listen` is served the same way;
binding is now separate from attaching the session, so a taken port still fails
at startup rather than later.

### `--check-config`

The loader is deliberately forgiving — an out-of-range value falls back to its
default, a malformed one is skipped — so that a hand-edited file can never
wedge the interface. The cost of that forgiveness is silence: a watchlist regex
that does not compile, a latency target with no port, a hidden column key that
names nothing, a binding for an action that does not exist, a misspelt key, a
theme that does not exist, an `asn_database` that has moved, a negative
threshold, a malformed baseline entry — all of them simply do nothing, for
ever. `--check-config` reports every one at once, says what was done about it,
and exits `1` (or `2` when the file cannot be parsed at all, which means the
whole file is being ignored rather than one value). Unknown keys are found by
comparing the file against the struct tags, so the check cannot drift from what
the loader understands.

### `SIGHUP` reloads

A daemon that has to restart to pick up a new threshold throws away the session
totals, the event ring, the rate baselines and the trend — most of what makes a
long-running daemon worth running. `SIGHUP` re-reads the config in place and
keeps all of it. The reload is recorded as a `config_reloaded` event, at
warning level when part of the new config had to be ignored, so a reload that
silently halves the rules is not itself silent. A snapshot or remote session
refuses to reload: those are showing another host, and reloading would replace
their forced settings with this machine's.

### Packaging

- The unit gained `ExecReload` (so `systemctl reload ducknetview` works) and an
  `ExecStartPre=/usr/bin/ducknetview --check-config`, so it refuses to start
  rather than running with half its rules disabled.

### Fixed

- The flag-documentation guard added in 0.3.1 matched on substrings, so
  `--metrics` counted as documented wherever `--metrics-listen` appeared. It
  now matches whole flag names. (Both were genuinely documented; the weakness
  was in the check.)

## 0.4.0 — 2026-10-11

The whole 0.3.1 roadmap, tiers 1 to 3. Fourteen features; the theme running
through the first four is that everything this tool measures was only usable
while somebody was looking at it.

### Headless and scriptable

- **`--daemon`** runs the model with no renderer and no input attached: the
  same polling, rates, event detection and alert rules, writing events to
  stdout and stopping cleanly on `SIGINT`/`SIGTERM` (which is what persists the
  day's usage totals). `--log-events` and `--on-alert` previously only did
  anything while the TUI was open, which is backwards — the moment you stop
  watching is the moment you want to be told.
- **`--metrics-listen ADDR`** serves the Prometheus metrics over HTTP at
  `/metrics`, so a scraper can reach them directly instead of reading a
  cron-written textfile. A scrape costs a second of sampling and is cached for
  five, so concurrent scrapers share one measurement. Runs alongside the TUI or
  the daemon.
- **`--diff old.json new.json`** reports what changed between two snapshots:
  listeners gained and lost, processes that appeared, remote hosts contacted
  for the first time, links that went away. Exits `1` on any difference, like
  `diff(1)`. Processes are compared by name so a restart is not a change, and
  listeners by bind address so a service moving from `127.0.0.1` to `0.0.0.0`
  reads as exactly that.
- **`--from ssh://host`** is the live version of the snapshot viewer: the same
  `ssh HOST ducknetview --json` re-run on the refresh interval. Two consecutive
  fetches carry the same cumulative counters a local poll would, so throughput,
  session totals, the event log and every alert rule work on the remote host
  with nothing installed there but the binary. A failed fetch keeps the last
  view and marks the header stale rather than ending the session.

### In the interface

- **`F` follows a process or a listener into its connections** and back again.
  Seeing a process move 40 MB/s and then retyping its name into the Connections
  search was the gap `a` and `w` closed for the baseline and the watchlist. A
  folded row follows every PID it stands for.
- **Settings overlay (`,`)** — the six alert thresholds, the latency targets,
  the hidden columns and the theme had no key binding at all. The overlay edits
  them in place and writes the config as it goes; invalid input says why rather
  than being dropped.
- **`T` arranges the Processes tab as a tree** of what started what, which `A`
  (fold by name) structurally cannot answer. A process whose parent holds no
  sockets is a root rather than a hidden row, and a parentage cycle is broken
  rather than followed.
- **Themes** — `default`, `contrast` (for a light terminal or a projector,
  where the greys carrying most of the dimming are illegible) and `mono`.
  `NO_COLOR` still overrides all of them.
- **Remappable keys** — `"keys": {"closed": "Z"}` rebinds any of the ~36
  actions. An unknown action, a reserved key or one another action already owns
  is ignored rather than obeyed, so a bad config cannot leave the tool
  unusable; rebinding takes the default key away so one press never does two
  things; and the help overlay renders what is actually bound.

### New data

- **IPv6 neighbours**, which the Routes tab previously had to explain it could
  not show. `/proc/net/arp` has never carried them, so the neighbour table is
  now an `rtnetlink` `RTM_GETNEIGH` dump covering both families, with the
  kernel's own cache state (reachable / stale / incomplete / …) — reusing the
  netlink plumbing the socket counters already needed rather than adding a
  dependency. Falls back to `/proc/net/arp` where netlink is unavailable.
- **Network ownership** — point `asn_database` at an ip2asn TSV (plain or
  gzipped) and an OWNER column appears on the Connections tab, sortable, with
  the AS number and country in the detail overlay. Nothing bundled, no
  dependency, no lookup leaving the host.
- **A 24-hour trend on the Overview** — per-minute throughput peaks kept in
  `trend.json`, the only chart in the tool that survives a restart. Peaks
  rather than means, because a ten-second burst averaged over a minute looks
  like nothing; gaps render as gaps rather than compressing the time axis.
- **Interface alert rule** (`alert_iface_drops_per_min`) — errors and drops
  were collected and displayed from the start but could not fire anything,
  which made the link the one measured thing the tool could not tell you about.
  Measured as a rate, since the counters are cumulative since boot.
- **Snapshots now carry the host's configuration** — routes, neighbours,
  resolvers, Wi-Fi state and one gateway latency probe — so `--from` describes
  the remote machine's Routes tab instead of apologising for it. `--json` now
  marshals the viewer's own type, so what `--from` can read is by construction
  what `--json` writes. The gateway probe runs during the second the sampler
  already spends waiting, so it costs no extra time.

### Packaging

- The `.deb` now ships a systemd unit for `--daemon`, disabled by default:
  `DynamicUser`, no privileges, state in `/var/lib/ducknetview`, events in
  `/var/log/ducknetview/`, metrics on `127.0.0.1:9847`.
- **`build-deb.sh` was compiling `cmd/ducknetview/main.go` rather than the
  package**, which produced no binary at all the moment `main` grew a second
  file — and `mv` failing was the only sign. The .deb would have shipped with
  the man page and completions and no `ducknetview` in it.

### Internal

- The two large message handlers were extracted into `applyNetSnapshot` and
  `applyConnInfo`, which is what lets a live remote session reuse the entire
  local pipeline rather than reimplementing it. The snapshot guards added in
  the 0.3.0 review stay in the message handlers, where they belong.
- `probe.LatencySample` round-trips through JSON: it holds an `error`, which
  cannot be unmarshalled back, so a snapshot carrying a failed probe would have
  failed to load at all.
- New smoke test presses every binding on every tab at four terminal sizes and
  renders the result, plus a check that nothing overflows 80 columns with the
  tree, a drill and the owner column all on at once.

## 0.3.1 — 2026-10-10

Documentation and packaging only; no behaviour change.

- **New `Configuration` section in the README** listing every key in
  `config.json`: what sets it, what it means, and which ones have no key binding
  and are meant to be hand-edited. `hidden_columns` and `rates_in_bits` were
  previously undocumented, and the valid column keys were only discoverable by
  reading the source.
- **New `CONFIGURATION` section in the man page**, covering the same ground for
  readers who never open the README.
- Fixed a README contradiction: the Notes section claimed quick filters were
  "session-only and not persisted" while the Features section (correctly) said
  they were. They are persisted.
- Fixed the documented sort cycles: the Connections tab also cycles through
  `rtt` and `retrans`, and the grouped view through `bytes`.
- The watchlist note still said it could only be edited by hand; `w` has added
  and removed remotes since 0.3.0.
- Moved the "first poll establishes a baseline rather than reporting everything
  as new" sentence off the reverse-DNS bullet, where it had been spliced, and
  onto the event log it describes.
- Refreshed the project layout, which had not mentioned routes, latency probes,
  Wi-Fi state, metrics, usage history or the snapshot viewer.
- `--usage-all` and `--help` are now documented in the man page, and the zsh
  completion offers `--help` and the `procs` / `conns` / `neighbours` tab
  aliases that bash completion already knew about.
- `DEBIAN/control` gained `Section`, `Priority`, `Homepage` and a real extended
  description, and its `Installed-Size` is now computed from the staged tree by
  `build-deb.sh`. The hardcoded value had drifted to less than half the truth
  (7300 KiB recorded, 15192 KiB actual).
- New `cmd/ducknetview/docs_test.go`: every flag `main.go` defines must appear
  in the man page and both completions, and the version must agree across
  `version.go`, the README and the man page header. This is the drift that this
  release was mostly spent fixing.

## 0.3.0 — 2026-08-20

Seven tabs, scriptable surfaces, and the interaction loops the previous release
left half-open.

- **Routes tab** — the default route in use, the configured nameservers, the
  full IPv4/IPv6 routing tables and the neighbour (ARP) table with a
  best-effort MAC vendor.
- **Latency probes** — TCP-handshake round trip to the default gateway, with a
  short history on the Overview. Extra targets via `latency_targets`; a refused
  connection counts as a valid timing.
- **Wi-Fi radio state** on the Interfaces tab: signal in dBm with a rating,
  link quality and the noise floor where the driver reports one.
- **`--from`** browses a `--json` snapshot instead of the live host, so
  `ssh host ducknetview --json | ducknetview --from -` inspects a remote machine
  with nothing installed there. **`--metrics`** emits Prometheus text for a
  node_exporter textfile directory, **`--top`** prints the busiest processes and
  hosts, and **`--usage` / `--usage-all`** print recorded daily traffic.
- **Daily usage history** — session totals merged into `usage.json` on exit,
  40 days deep, the 50 biggest processes and hosts per day.
- **New alert rules**, all opt-in: RTT threshold, retransmits per minute,
  connection-count growth (a leak) and per-process fan-out (a scan).
- **`listener_moved`** correlates a listener vanishing and reappearing into one
  event, raised to alert level when the move widens its exposure
  (`127.0.0.1:5432` → `0.0.0.0:5432`).
- **Closed interaction loops**: `a` accepts one listener into the baseline
  instead of all-or-nothing, and `w` adds or removes a watchlist remote without
  editing JSON.
- **Process aggregation** (`A`) folds rows sharing a name into one; **bits/s**
  (`U`); **IFACE column and `i` filter**; **`d`** acknowledges alerts; a quit
  confirmation; `NO_COLOR`; configurable `hidden_columns`.
- Packaging: man page and bash/zsh completions shipped by `make install` and
  `build-deb.sh`; CI runs fmt, vet, race tests and staticcheck, cross-compiles
  for macOS, Windows and FreeBSD, and builds both .debs.

### Fixed

- **`--from` was silently overwritten by the local host**: `Init()` issued the
  live collectors unconditionally and no handler checked `snapshotOnly`, so a
  remote snapshot was replaced by this machine's data within a second. Only
  reproducible by snapshotting a *different* host.
- Snapshot mode persisted its forced settings into the user's config on quit.
- Ephemeral UDP client sockets inflated the exposed-listener count on the
  Overview, in the Prometheus gauge and in the Ports "N new" badge; all three
  now agree on `probe.IsServiceListener`.
- `alertFired` was only pruned on the throughput path, so a config with only
  quality rules grew it forever.
- The `i` interface filter was never applied to the closed-connections view
  although `anyFilterActive()` claimed it was.
- Latency targets without a port were probed forever, showing "no response".
- `--check-baseline`'s clean message counted sockets it never compares, and
  `--usage` advertised a "(0 for all)" that did nothing.

## 0.2.0 — 2026-08-11

- **Session totals** per process, host and session, with a Top-talkers panel;
  `t` swaps the throughput columns between current rate and total.
- **Closed-connection history** (`z`): the last 200 connections with their final
  counters and lifetime.
- **Alerts** on per-process and per-connection throughput, with `--on-alert` to
  run a command (argv + env, never a shell) and portable baselines via
  `--save-baseline` / `--baseline`.
- **RTT and RTX columns** from the netlink `tcp_info` dump, plus send/receive
  queue depth in the detail overlay.
- **Column specs** — every table declares its columns once, so the header, the
  sort arrow and mouse hit-testing cannot drift apart.
- **Search** gained `re:` regular expressions, a filter/highlight toggle (`\`)
  and `n` / `N` match jumping.
- Fixed `Collect`'s truncation ranking by connection count alone, which could
  drop a single-socket 500 MB/s transfer before the Processes tab saw it.

## 0.1.0 — 2026-08-08

- **Per-connection and per-process throughput** read from the kernel's own
  socket counters over netlink `sock_diag` — the same source as `ss -ti`, with
  no elevated privileges. Linux and TCP only; the columns hide themselves
  elsewhere.
- **Events tab** — a 500-entry log of listeners appearing and disappearing,
  watchlist hits, first contact with a new public host, and interface changes.
  `--log-events` writes JSONL.
- **Listener baseline** (`b` / `B`), feeding the Overview and `--check-baseline`
  for cron. UDP sockets on ephemeral ports are excluded, or the baseline is
  permanently dirty.
- CPU% sampled between polls, container ids from cgroups, USER columns, iface
  errors/drops/pps, and the version single-sourced in `internal/version`.

## 0.0.5 — 2026-07-19

- Connections tab, row-detail overlay (`enter`), process signalling (`x`),
  service names, IP scope colouring, quick filters, watchlist, CSV export
  (`e`), `--once` / `--json`, a security summary on the Overview, and config
  persistence.
- Fixed search boxes that could not receive `q`, arrows, `tab`, `/` or `ctrl+e`;
  a two-character clip on every table's rightmost column; sparkline overflow on
  wide terminals; the cursor being left off-screen after a sort or filter; and a
  counter reset wrapping an interface's byte delta into a huge spike.

## 0.0.4 — 2025-12-15

- Overview, Interfaces, Ports and Processes tabs; search; external IP; arm64
  package builds.

---

Releases before 0.3.0 were not tagged, and 0.0.6, 0.0.7 and 0.1.0 existed only
in the working tree — their contents are listed under the release that first
carried them into a commit.
