# ducknetview 🦆

Minimalistic **network monitoring TUI** written in **Go**.

Current version: **0.6.0** — see [CHANGELOG.md](CHANGELOG.md) for what changed,
and [ROADMAP.md](ROADMAP.md) for what might come next.

Built with:
- **Bubble Tea** + **bubbles** (UI framework)
- **lipgloss** (styling)
- **tcell** (terminal backend, kept in the build for completeness)
- **gopsutil/v4** (network / process info)
- **atotto/clipboard** (copy-to-clipboard)
- Linux **netlink** (`sock_diag`) for per-connection byte counters — no external dependency

The focus is a clean, usable terminal UI even on hosts with many interfaces (Docker, bridges, veth).

![Overview](resources/1.png)
![Interfaces](resources/2.png)
![Ports](resources/3.png)
![Processes](resources/4.png)

---

## Features

- **Overview tab**
    - Hostname, uptime, current timestamp
    - Interface count (up / down) and the active refresh interval
    - **Security summary** — at-a-glance counts of exposed listeners, public connections, watchlist hits and listeners outside the saved baseline
    - **Gateway and DNS** — the default route in use and the nameservers configured, on one line. An IPv6-only host has an IPv6 default route and is reported as such, rather than being told it cannot reach anything off-link
    - **Path latency** — TCP-handshake round trip to the default gateway (and any extra targets in the config), with a short history each. A dual-stack host gets both first hops, as `gateway` and `gateway6`: they are frequently not the same box. A link-local IPv6 next hop is probed with its zone (`fe80::1%eth0`), without which it is not dialable at all
    - **Top talkers (session)** — the processes and remote hosts that have moved the most data since launch, with the session grand total
    - **Connection churn** — connections opened and closed per poll, as a chart. A spike is how a leak or a scan announces itself
    - **Host TCP** — host-wide counters from `/proc/net/snmp` and `/proc/net/netstat`, as per-minute rates: listen overflows and drops (connections the kernel refused because a service's accept queue was full — they never become a row anywhere else), SYN cookies sent, retransmitted segments, retransmit timeouts, bad segments received and resets sent. Only the counters that are moving are listed, and the problem ones are highlighted
    - **Last 24 hours** — per-minute throughput peaks kept on disk, so "when did that spike happen" is still answerable after a restart. Every other chart in the tool dies with the session
    - Panels appear once they have something to say, and step aside on a terminal too short to hold them
    - Selected interface summary with RX/TX rate, cumulative RX/TX totals, and sparkline charts
    - **Total throughput** across all real interfaces (loopback excluded) with current + session-peak RX/TX and sparklines
    - **External IP** (fetched from `api.ipify.org`, auto-refreshed every 30 s, manual refresh with `ctrl+e`; the TUI only — `--daemon` never contacts it)
    - The "Selected interface" is whichever interface is highlighted on the **Interfaces** tab (defaults to the first interface until you pick one)

- **Interfaces tab**
    - Scrollable interface list (left pane)
    - Auto-updating details on selection change (no Enter required)
    - Per-interface RX/TX sparkline charts, with the session peak alongside the cumulative total
    - **Packet rates and link health** — packets/s in and out, plus cumulative error and drop counters (highlighted when non-zero: usually a bad cable, a full ring buffer or an overloaded driver)
    - **Wi-Fi radio state** for wireless interfaces: signal in dBm with a plain-language rating, link quality as a percentage, and the noise floor where the driver reports one
    - **Hide-noise** toggle (`h`): drops loopback, docker, bridge, veth and down interfaces from the list so the real NICs stand out
    - Copy the selected interface summary to the clipboard with `y`

- **Ports tab**
    - Open listening TCP / UDP ports
    - PID and process name (best-effort)
    - Bind addresses are **colored by exposure**: orange = reachable off-host (`0.0.0.0`/`::` or a routable address), amber = private/LAN or inside a container's own network namespace, dim = loopback. A container's `0.0.0.0` is only orange when a `docker-proxy` on the host publishes it
    - **Listeners inside containers** and other network namespaces are listed too, with a **NETNS** column (container id, `ip netns` name, or `netns:<inode>`; `host` for ducknetview's own) that appears once there is more than one namespace. Search by it to filter to one container. See [Network namespaces](#network-namespaces)
    - Listeners that **appeared since the last poll** are highlighted (green protocol) with a count in the header — a new listener can signal a backdoor or a regression
    - **Service name** column (well-known ports, e.g. `443 → https`, from `/etc/services` with a built-in fallback)
    - **USER** column showing each listener's owning user, and a **UNIT** column with the owning process's systemd service or scope (`nginx.service`, `session-2.scope`) that appears when any listener has one
    - **QUEUE column** — a TCP listener's accept queue as `waiting/backlog` (Linux, from the same `sock_diag` dump as the throughput counters), amber from half full and red near the limit. A full queue is a service that is not calling `accept()` fast enough: the handshakes are done and the connections are dropped before the application ever sees them. The detail overlay shows the fill as a percentage; the column does not exist where the kernel cannot report it
    - **Baseline** — press `b` to record the current listeners as the expected set; anything outside it is flagged in red on the protocol cell, counted in the header and on the Overview, and reported by `--check-baseline`. `a` accepts just the highlighted listener into the baseline (and takes it back out), which is what you want when one new service is fine but the rest of the diff is not; `B` clears the baseline
    - **`F` follows the highlighted listener into the connections it is serving**, which is how you find out who is actually using a service
    - Selectable list (`↑↓` moves a row cursor); copy the highlighted row with `y`
    - **Sortable** (`s` cycles `proto → port → pid → process → user → unit → queue`, `S` reverses; queue sorts by how full, not how long)
    - Search (`/`) by port, address, protocol, service, user, unit, process name or network namespace

- **Processes tab**
    - Processes ranked by network connection count (top 80)
    - Connection count, listening-socket count, and **CPU% / memory%** per process. The detail overlay adds the number of sockets in **CLOSE_WAIT** — closed by the peer, never by the process — which is how the classic "never closes its sockets" bug looks before it runs out of descriptors (CPU is measured over the poll interval, not averaged over the process lifetime)
    - **RX / TX throughput** per process, summed across its connections — press `t` to swap the columns between the current rate and the **session total** (the sort keys follow the mode)
    - **USER** column, a **CONTAINER** column that appears when any process is running in a container (docker / podman / containerd / CRI-O / LXC), and a **UNIT** column — the systemd service or scope the process runs in, the innermost one under a user manager (`pipewire.service` rather than `user@1000.service`) — that appears when any process has one
    - **`A` folds processes that share a name into one row** — a browser is forty processes, and the useful row is their sum. Counts, CPU, memory and throughput add up; columns whose members disagree (different users, say) show `*`
    - Selectable list (`↑↓` moves a row cursor); copy the highlighted row with `y`
    - **`T` arranges the rows as a tree** of what started what, which answers the question `A` cannot: a stray `curl`, a worker under its supervisor, a shell under a session. A process whose parent holds no sockets is a root rather than a hidden row, so nothing disappears
    - **`F` follows the highlighted process into its connections** — you can see it moving 40 MB/s, and this is where to. A folded row follows every PID it stands for
    - **Sortable** (`s` cycles `conns → listen → cpu → mem → rx → tx → pid → name → user → unit`, `S` reverses)
    - Search (`/`) by process name, **command line**, PID, user, container id or unit — the command line is what separates forty rows called `python3`, and searching a container id or a unit doubles as a per-container or per-service filter. The command line is shown in the row detail overlay and is deliberately never serialised: a `--json` snapshot gets piped around, and an argument list can carry a password
    - Unprivileged, sockets belonging to other users arrive with no PID at all and cannot be named or signalled. The tab says how many rather than leaving a column of dashes to look like a bug, and the count is recorded once per session as an `attribution_limited` event so a headless log explains its own gaps

- **Connections tab**
    - All active (non-listening) TCP/UDP connections, including those inside containers' network namespaces (NETNS column, searchable, as on the Ports tab)
    - Local / remote address, resolved **service**, state, PID, process
    - **RX / TX throughput per connection**, read from the kernel's own per-socket counters (Linux; TCP only). The columns are hidden entirely where the platform can't account for sockets. `t` swaps them for cumulative totals
    - **RTT and RTX columns** — smoothed round-trip time and the socket's lifetime retransmit count, both sortable. Retransmits mean loss on the path; a standing send queue (in the detail overlay) means the peer isn't draining what you hand it — together they separate "the network is bad" from "the server is slow"
    - Remote addresses are **colored by scope** (public / private / loopback), and connections that appeared since the last poll are highlighted, with a `Δ +new/-closed` counter
    - **AGE column** — how long each connection has been seen this session (tracked across polls)
    - **Watchlist** — remotes matching your configured IPs / CIDRs / regex (see config) are highlighted in magenta
    - **Quick filters**: protocol (`p`: all/tcp/udp), IP version (`v`: all/v4/v6), state (`f`: all/established/active), public-remotes-only (`o`) and only-my-processes (`u`); ports share the `p` / `v` / `u` filters, processes share `u`
    - Optional **reverse DNS** for remote addresses (toggle with `r`); resolved names replace the remote IP and lookups are cached for the session
    - Optional **grouping** view (toggle with `g`): collapses connections by remote host with its throughput, connection count, per-state breakdown and the processes talking to it — sortable by traffic, which makes it a top-talkers-by-host view
    - **Closed connections** (toggle with `z`): the last 200 connections that went away, with their final byte totals, lifetime and when they closed. Answers "what was that transfer that just finished?", which a live table structurally cannot
    - **IFACE column and `i` filter** — which link each connection actually uses, appearing when connections span more than one interface. This is how you answer "what is going over the VPN?"
    - **`w` adds the highlighted remote to the watchlist** (and removes it again), so noticing a suspicious peer and acting on it no longer means editing JSON
    - **OWNER column** — the autonomous system behind each public address, when an `asn_database` is configured. rDNS is silent for most cloud addresses, and "who is 13.107.42.14" otherwise means another tool. Sortable, so the view groups by provider
    - **`F` goes back** to the process or listener this view was opened from (see below)
    - Selectable list (`↑↓` moves a row cursor); copy the highlighted row (or group) with `y`
    - **Sortable** (`s` cycles `pid → proto → state → local → remote → process → scope → age → rx → tx → rtt → retrans → owner`, or `conns → host → bytes` in grouped view and `closed → bytes → lifetime → process → remote` in the closed-connections view; `S` reverses)
    - Search (`/`) by local / remote address (raw IP or resolved name), process, service, state or protocol

- **Routes tab**
    - The default route actually in use, the configured nameservers (local stubs marked as such), the full IPv4 and IPv6 routing tables, and the neighbour table with a best-effort MAC vendor
    - **IPv4 *and* IPv6 neighbours**, with the kernel's own cache state (reachable / stale / incomplete / …), read from an `rtnetlink` dump — `/proc/net/arp` has never carried IPv6, which is why `ip -6 neigh` exists and `arp` has no equivalent. Falls back to `/proc/net/arp` where netlink is unavailable
    - Read on its own slow cadence, since none of it changes often

- **Events tab**
    - A timestamped log of what *changed*, which the live tables can't show you: listeners appearing and disappearing, watchlist hits, the first connection to each new public host, and interfaces going up, down, appearing or vanishing
    - Levels are colour-coded (info / warn / **alert**) and the tab label carries an alert count, so a new listener is visible from any other tab
    - `/` searches the log and `l` cycles a level filter (all / warn and above / alerts only)
    - **Thresholds and rules**, all opt-in through the config, each firing at most once a minute per subject: throughput per process or per connection (`alert_process_bps`, `alert_connection_bps`), round-trip time (`alert_rtt_ms`), retransmits per minute on one socket (`alert_retrans_per_min`), a process whose connection count only ever grows (`alert_conn_growth_polls` — a leak), a process reaching many new hosts at once (`alert_fanout_hosts` — what a scan looks like from the inside), errors or drops on a link (`alert_iface_drops_per_min`, measured as a rate because the counters are cumulative since boot), a listener's accept queue filling up (`alert_listen_queue_pct`), the host dropping connections on the listen path (`alert_listen_drops_per_min`) and a process holding many sockets in CLOSE_WAIT (`alert_close_wait`). With `--on-alert` each alert also runs a command of your choosing
    - **Daemon-friendly subjects** — `listener_up`, `listener_down`, `listener_moved` and `throughput_process` name the systemd unit beside the PID (`pid 812 (nginx, nginx.service)`), because by the time anyone reads a daemon's log the PID has changed and the unit is what they know the service by
    - **`baseline_drift`** — the first poll is deliberately silent, so a daemon restarted onto a host already listening outside its baseline would otherwise say nothing about it. With a baseline saved, the first poll of a session raises one **alert** naming the off-baseline listeners (the first five, then "N more"), so `--on-alert` fires for drift that predates the session
    - **`listener_moved`** — a service that disappears and reappears on the same port under the same process is reported as one event rather than two, and as an **alert** when the move widens its exposure (`127.0.0.1:5432` → `0.0.0.0:5432` is the case worth catching)
    - **Recovery is reported too**: every sustained rule — throughput, round-trip time, retransmits, link errors, accept queues, listen drops, CLOSE_WAIT — raises a matching `*_cleared` event once its subject has been back under the threshold for a minute, including when the subject went away entirely because the transfer finished or the process exited. Without it a hook is paged and never cleared, and the log records a beginning with no end
    - `d` acknowledges alerts: the tab badge clears without destroying the log
    - Holds the most recent 500 events in memory; `c` clears them, `e` exports them to CSV, and `--log-events` writes every event to a file as it happens
    - One poll can contribute at most 20 events per kind, with an explicit "N more … suppressed" entry so truncation is never silent

- **Row details & actions** (`enter` and `e` on Ports / Processes / Connections / Events; `x` on Ports / Processes / Connections)
    - Press `enter` for a full detail overlay of the highlighted row (protocol, addresses, resolved service, exposure/scope, watchlist status, baseline status, age, throughput and RTT, and the owning process's user / container / systemd unit / PPID / command line / CPU / memory). Long command lines wrap instead of being cut off; `↑↓` scrolls, `y` copies it as plain text, and it keeps updating while open
    - Press `x` to signal the highlighted row's process — the confirmation offers `t` SIGTERM · `k` SIGKILL · `h` SIGHUP (may need privileges)
    - Press `e` to export the current (filtered / sorted) table to a timestamped CSV in the working directory

- **Headless and scriptable**
    - **`ducknetview --daemon`** runs the whole model with nothing drawing it: the same polling, the same rates, the same event detection and the same alert rules, writing events to stdout and stopping cleanly on `SIGTERM`. Everything the alerting does was previously only running while somebody was watching the TUI, which is backwards — the moment you stop watching is the moment you want to be told. **`SIGHUP` re-reads the config in place**, keeping the session totals, the event ring and the rate baselines that a restart would throw away; the reload is itself logged as a `config_reloaded` event, at warning level when part of the new config had to be ignored
    - **`ducknetview --metrics-listen :9847`** serves the Prometheus metrics over HTTP at `/metrics` instead of (or as well as) writing a textfile, so a scraper can reach them directly. It is **answered from the running session's own state** rather than by sampling the host again, which makes a scrape sub-millisecond instead of a second — and exports what only a running session knows: **seconds since the last completed poll** (the one metric that reveals a monitor which is up but no longer working), polls and events by kind, the session's accumulated byte totals, alerts raised and alert hooks that failed. It binds a socket, so give it an explicit address like `127.0.0.1:9847` unless the exporter should really be reachable from the network. Works alongside the TUI or the daemon
    - **`ducknetview --check-config`** reports everything wrong with the config at once and exits non-zero. The loader is deliberately forgiving so a hand-edited file can never wedge the interface — and the cost of that is silence: a watchlist regex that does not compile, a latency target with no port, a hidden column key that names nothing, a binding for an action that does not exist and a misspelt key all simply do nothing, for ever. Exits `1` when anything is being ignored, `2` when the file cannot be read at all. Problems that do not disable anything are reported as **warnings** and do not fail the check: a key that differs from the documented one only in case (the loader matches keys case-insensitively, so it is applied), and a latency target that does not resolve right now (DNS may simply not be up yet at boot)
    - **`ducknetview --diff old.json new.json`** reports what changed between two snapshots — listeners gained and lost, processes that appeared, remote hosts contacted for the first time, links that went away — and exits `1` when anything differs, like `diff(1)`. Processes are compared by name, so a restart is not a change; listeners are keyed by bind address, so a service moving from `127.0.0.1` to `0.0.0.0` shows up as exactly that
    - `ducknetview --once` prints a one-shot text snapshot (interfaces, ports, connections) and exits; `--json` emits a fuller snapshot (also including processes, routes, neighbours, resolvers, Wi-Fi and one gateway latency probe) as JSON for scripting
    - `ducknetview --check-baseline` compares the host's listeners against the saved baseline and exits **1** if any are unexpected (**0** when clean, **2** when no baseline has been saved) — enough for cron or a monitoring check
    - `ducknetview --save-baseline FILE` writes a **portable baseline**; `--check-baseline --baseline FILE` checks against it. Build one on a known-good host and ship it to the fleet
    - `ducknetview --tab conns` opens straight onto a tab; `ducknetview --view vpn` opens into a saved view (below), and the two compose. An explicitly empty `--view ''` prints the saved view names, one per line, and exits — the shell completions use it
    - `ducknetview --from snap.json` browses a `--json` snapshot instead of the live host, frozen and clearly marked. `-` reads stdin, so `ssh host ducknetview --json | ducknetview --from -` browses a remote host with no agent to install. Because the snapshot now carries the host's routing configuration, the Routes tab describes the remote machine rather than explaining why it cannot
    - **`ducknetview --from ssh://host` is the live version of that**: the same command is re-run on the refresh interval, and since two consecutive snapshots carry the same cumulative counters a local poll would, throughput, session totals, the event log and every alert rule work on the far host. Still nothing installed there but the binary. A failed fetch leaves the last view in place and marks the header stale rather than ending the session
    - `ducknetview --metrics` prints the host's network state in Prometheus text format, for a node_exporter textfile directory
    - `ducknetview --top 10` prints the busiest processes and hosts and exits
    - `ducknetview --usage 7` prints the recorded daily traffic totals (`--usage-all` for everything kept)
    - `ducknetview --version` prints the version
    - Every mode exits **2** on a usage error (an unknown `--tab` or `--view`, a stray argument, `--daemon` with `--from`) or when the host cannot be read

- **Refresh control**
    - `space` pauses / resumes auto-refresh (a `⏸ PAUSED` marker shows in the header)
    - `+` / `-` make the interface-sample interval faster / slower (1 s → 10 s)
    - `R` refreshes everything immediately

- **Daily usage history**
    - Traffic is merged into `usage.json` every five minutes and on exit, booked against the day it was flushed on — so a long-running daemon spreads its traffic over the right days, and a crash loses at most five minutes of it. Per day, keeping 40 days and the 50 biggest processes and hosts per day — enough to answer "where did this month's data go" without keeping a database

- **Search**
    - On the Processes tab the search covers the **command line** as well as the name, which is the only thing separating forty rows called `python3` from each other. It is never written to a snapshot, a CSV or the event log: a command line can carry a password, and a `--json` snapshot gets piped around
    - `/` filters the table; prefix the query with `re:` to use a **regular expression** (case-insensitive, and an invalid one says so instead of silently matching nothing)
    - `\` switches between **filtering** (hide non-matching rows) and **highlighting** (keep every row, mark the matches), and `n` / `N` walk the cursor between matches

- **Saved views (`V`)**
    - Half the keys in the tool exist to narrow what is on screen. Getting back to "the established TLS connections leaving over the VPN, busiest first" meant pressing six of them in the right order, every session
    - A view is the whole arrangement under a name: the tab, every per-tab search and the search mode, every sort column and direction, the five quick filters, the interface filter, the event level, and the grouping / closed / totals / name-folding / tree / hide-noise / reverse-DNS toggles. `enter` applies one, `n` saves the current screen, `d` deletes one, and saving under an existing name replaces it
    - They live in the config, so `ducknetview --view vpn` opens straight into one from a shell alias or a unit file. A drill is deliberately not recorded: it points at PIDs that will not exist next time

- **Settings overlay (`,`)**
    - The ten alert thresholds, the latency targets, the hidden columns and the theme had no key binding at all: the only way to discover them was to read this file, and the only way to set one was to quit, edit JSON and start again. The overlay edits all of them in place and writes the config as it goes
    - Numbers take `off` or an empty value to disable; list rows add a value, or remove it when given the same value again; invalid input says why instead of being silently dropped

- **Themes and key bindings**
    - `theme` selects `default`, `contrast` (for a light terminal or a projector, where the greys that carry most of the dimming are illegible) or `mono`. `NO_COLOR` still overrides all of them
    - `keys` rebinds any of the 39 actions: `"closed": "Z"`. An unknown action, a reserved key (`ctrl+c`, `tab`, the arrows, the digits) or a key another action already owns is ignored rather than obeyed, so a bad config cannot leave the tool unusable — and the help overlay renders what is actually bound

- **Persisted settings**
    - Sort keys/direction, the hide-noise / reverse-DNS / grouping / totals / tree toggles, the quick filters, the search mode, the refresh interval, the last open tab, the theme, the key bindings, the saved views and the listener baseline are saved to `ducknetview/config.json` under the user config directory (`${XDG_CONFIG_HOME:-~/.config}` on Linux, `~/Library/Application Support` on macOS) as you change them and restored on next launch. A `--daemon` session has no keyboard and never writes the file, so edits made while it runs are not reverted when it stops

Interface counters are sampled on the chosen interval (default every second). The ports, processes and connections views are all derived from a **single socket enumeration** (one `gopsutil` pass feeds all three, with shared process-name, user and container caches). Enumeration follows the chosen refresh interval but never runs more often than every 2 seconds, since it costs roughly twenty times an interface sample.

Copied rows are tab-separated, so they paste cleanly into a spreadsheet or editor. The active sort column is marked with an arrow in the table header, and clicking a header sorts by that column (clicking it again reverses).

Press `?` at any time for an in-app key-binding overlay. The mouse is supported too: click a row to select it, or use the wheel to scroll.

---

## Key bindings

### Global

| Key                  | Action |
|----------------------|--------|
| `?`                  | Toggle the help overlay (`↑↓` scrolls it — it is longer than a terminal — any other key closes it) |
| `,`                  | Settings: alert thresholds, latency targets, hidden columns, theme |
| `V`                  | Views: apply a saved arrangement, `n` saves the current one, `d` deletes |
| `←` `→`              | Switch tabs |
| `tab` / `shift+tab`  | Cycle tabs |
| `1`–`7`              | Jump directly to a tab |
| `enter`              | Show a detail overlay for the highlighted row (`↑↓` scrolls it, `y` copies it, any other key closes it) |
| `y`                  | Copy the highlighted row / selected interface to the clipboard |
| `e`                  | Export the current table view to a CSV file |
| `x`                  | Signal the highlighted row's process (Ports / Processes / Connections) — confirm with `t`/`k`/`h` (TERM/KILL/HUP) |
| `space`              | Pause / resume auto-refresh (also in a live `--from ssh://` session) |
| `+` / `-`            | Faster / slower refresh interval (`=` and `_` also work, unshifted) |
| `R`                  | Refresh everything now |
| `t`                  | Throughput columns: current rate / session total |
| `U`                  | Rates in bytes/s or bits/s |
| `q`                  | Quit (asks first); `ctrl+c` quits immediately |
| `n` / `N`            | Jump to the next / previous search match |
| `\`                  | Switch between filtering and highlighting matches |
| `p` / `v`            | Cycle protocol / IP-version filter (Ports & Connections) |
| `f`                  | Cycle connection state filter (Connections) |
| `o`                  | Toggle public-remotes-only filter (Connections) |
| `u`                  | Toggle the only-my-processes filter (Ports / Processes / Connections) |
| `b` / `B`            | Save / clear the listener baseline |
| `a`                  | Accept the highlighted listener into the baseline (Ports) |
| `A`                  | Fold processes sharing a name into one row (Processes) |
| `T`                  | Arrange processes as a tree of what started what (Processes) |
| `F`                  | Follow the highlighted process or listener into its connections; again to go back |
| `h`                  | Toggle the hide-noise interface filter |
| `ctrl+e`             | Refresh external IP now |
| `ctrl+c`             | Quit |

### Lists / Viewports

| Key            | Action |
|----------------|--------|
| `↑ ↓` / `k j`  | Move the row cursor (or scroll the interface list) |
| `PgUp / PgDn`  | Move the cursor by a page |
| `Home / End`   | Jump to first / last row |
| mouse click    | Select the clicked row (Ports / Processes / Connections) |
| mouse wheel    | Scroll the list |

### Sorting (Ports / Processes / Connections)

| Key | Action |
|-----|--------|
| `s` | Cycle the sort column |
| `S` | Reverse the sort direction |
| click | Click a column header to sort by it; click again to reverse |

### Search (Ports / Processes / Connections / Events)

| Key      | Action |
|----------|--------|
| `/`      | Start search |
| `re:…`   | Treat the query as a regular expression |
| `Enter`  | Apply search filter |
| `Esc`    | Exit search |
| `Ctrl+u` | Clear query (while searching, or when search not active to clear active filter) |

### Connections tab only

| Key | Action |
|-----|--------|
| `r` | Toggle reverse DNS for remote addresses |
| `g` | Toggle grouping view |
| `z` | Toggle the closed-connections history |
| `i` | Cycle the interface filter |
| `w` | Add / remove the highlighted remote from the watchlist |
| `F` | Return to the process or listener this view was opened from |

### Events tab only

| Key | Action |
|-----|--------|
| `l` | Cycle the level filter (all / warn+ / alerts) |
| `d` | Acknowledge alerts (clears the badge, keeps the log) |
| `c` | Clear the recorded events |

---

## Build & run

Requires Go 1.24 or newer (see `go.mod`).

```bash
go install github.com/nexusriot/ducknetview/cmd/ducknetview@latest
```

Or from a checkout:

```bash
go run ./cmd/ducknetview
```

Build a binary:

```bash
go build -o ducknetview ./cmd/ducknetview
./ducknetview
```

One-shot snapshot (no TUI), handy for scripts and cron:

```bash
./ducknetview --once     # human-readable text
./ducknetview --json     # machine-readable JSON
```

Alert on unexpected listeners (exit code 1 when the host is listening on
something outside the saved baseline):

```bash
./ducknetview --check-baseline
```

Browse another host without installing anything there:

```bash
ssh somehost ducknetview --json | ducknetview --from -
```

Expose everything to Prometheus via a node_exporter textfile:

```bash
./ducknetview --metrics > /var/lib/node_exporter/ducknetview.prom
```

Or let Prometheus scrape it directly:

```bash
./ducknetview --metrics-listen 127.0.0.1:9847
```

Watch a host with nobody watching the screen — poll, apply every alert rule,
log what changes, and serve metrics, all from one process:

```bash
./ducknetview --daemon --log-events /var/log/ducknetview.jsonl --on-alert /usr/local/bin/notify-ops --metrics-listen 127.0.0.1:9847
```

Change a threshold without losing the session:

```bash
sudo env XDG_CONFIG_HOME=/var/lib/ducknetview ducknetview --check-config && sudo systemctl reload ducknetview
```

`--check-config` checks the config of whoever runs it; the packaged unit keeps
its own at `/var/lib/ducknetview/ducknetview/config.json`, hence the
`XDG_CONFIG_HOME`.

The alert that matters most is on the monitor itself — a daemon that is up but
no longer polling looks healthy in every other metric:

```
ducknetview_last_poll_age_seconds > 60
```

See what a deploy did to a host:

```bash
./ducknetview --json > before.json && deploy.sh && ./ducknetview --json > after.json && ./ducknetview --diff before.json after.json
```

Watch a remote host live, with nothing installed there but the binary:

```bash
./ducknetview --from ssh://ops@web1
```

Keep a durable record of everything that changes while the TUI runs:

```bash
./ducknetview --log-events /var/log/ducknetview-events.jsonl
```

Build a baseline on a known-good host and check the fleet against it:

```bash
./ducknetview --save-baseline golden.json
```

Run a script for every alert (it receives the kind, subject and detail as
arguments, plus `DNV_EVENT_*` environment variables):

```bash
./ducknetview --on-alert /usr/local/bin/notify-ops
```

### Tests

```bash
make test     # unit tests
make cover    # unit tests with a coverage summary
make e2e      # the end-to-end suite, in its own container
```

The unit tests cover the model, the parsers and the renderers against fixed
input. Everything else — the netlink readers, every non-interactive mode, the
daemon, the exporter and the TUI itself — needs a live host, so the e2e suite
**runs inside a container and treats that container as the host under test**:
it opens known sockets, moves a known number of bytes across them and asserts
that the program reports them, then drives the TUI on a real pseudo-terminal.
Nothing on your machine is read or written, and nothing leaves the container.
See [e2e/README.md](e2e/README.md).

### Debian package

Two helper scripts are included for producing `.deb` packages:

```bash
./build-deb.sh           # amd64 (default)
./build-deb.sh arm64     # cross-build for arm64
./build-deb-arm64.sh     # convenience wrapper for arm64
```

Output lands in `build/ducknetview_<version>_<arch>.deb`, carrying the binary,
the man page, both completions and a systemd unit for the headless mode. The
unit is shipped **disabled** — installing a package should not start watching
your network by itself:

```bash
sudo systemctl enable --now ducknetview
```

It runs under a `DynamicUser` with no privileges (the socket counters need
none), keeps its config and history in `/var/lib/ducknetview/ducknetview/`,
logs events to `/var/log/ducknetview/`, and serves metrics on `127.0.0.1:9847`.
Edit the unit to change any of that.

Without privileges it **cannot tell which process owns another user's socket**
— the kernel only shows `/proc/<pid>/fd` to the owner, root, or a holder of
`CAP_SYS_PTRACE` — so the rules that need a process (`alert_process_bps`,
`alert_conn_growth_polls`, `alert_fanout_hosts`) and per-process usage stay
silent for those sockets, and the daemon logs one `attribution_limited` event
saying how many there are. The unit carries commented-out capability lines
that lift this; enabling them is a deliberate trade-off, since
`CAP_SYS_PTRACE` is a powerful capability.

`dpkg-deb` is required. When cross-compiling, `CGO_ENABLED=0` is used.

---

## Configuration

`ducknetview --check-config` validates everything below and says what is being
ignored and why — worth running after any hand-edit, and in a unit's
`ExecStartPre` so a daemon refuses to start rather than running with half its
rules quietly disabled. Warnings (see above) are printed but do not fail it.

Everything lives in one file, `ducknetview/config.json` under the user config
directory — `${XDG_CONFIG_HOME:-~/.config}` on Linux, `~/Library/Application Support` on macOS —
and all of it is written for you as you change things — the thresholds, latency
targets, hidden columns and theme through the **settings overlay (`,`)**, the
rest through their own key bindings. Hand-editing still works for anything the
overlay does not cover. Unknown keys are ignored and out-of-range values fall
back to the default, so an old or hand-mangled file can't wedge the UI.

| Key | Type | Set by | Meaning |
|-----|------|--------|---------|
| `hide_noise` | bool | `h` | Hide loopback / docker / bridge / veth / down interfaces |
| `refresh_ms` | int | `+` `-` | Interface sample interval; one of 1000, 2000, 3000, 5000, 10000 — anything else is ignored |
| `conns_rev_dns` | bool | `r` | Resolve remote addresses on the Connections tab |
| `conns_group` | bool | `g` | Start the Connections tab in the grouped-by-host view |
| `show_totals` | bool | `t` | Throughput columns show session totals rather than current rate |
| `aggregate_processes` | bool | `A` | Fold processes sharing a name into one row |
| `process_tree` | bool | `T` | Arrange processes as a tree of what started what |
| `rates_in_bits` | bool | `U` | Word rates in bits/s instead of bytes/s |
| `hidden_columns` | []string | `,` | Column keys to leave out of the tables (see below) |
| `latency_targets` | []string | `,` | Extra `host:port` latency probes; the default gateway is always probed |
| `views` | []object | `V` | Saved arrangements of the tables, recalled from the overlay or with `--view` |
| `theme` | string | `,` | `default`, `contrast` or `mono` |
| `keys` | map | hand-edited | Action name → key, e.g. `{"closed": "Z"}` (see below) |
| `asn_database` | string | hand-edited | Path to an ip2asn TSV; adds the OWNER column (see below) |
| `proto_filter` | int | `p` | 0 all · 1 tcp · 2 udp |
| `ipver_filter` | int | `v` | 0 all · 1 v4 · 2 v6 |
| `conn_state_filter` | int | `f` | 0 all · 1 established · 2 active |
| `public_only` | bool | `o` | Connections with public remotes only |
| `only_mine` | bool | `u` | Only processes owned by the current user |
| `search_highlight` | bool | `\` | Search highlights matches instead of filtering rows |
| `last_tab` | int | tab switch | Tab to restore on next launch |
| `ports_sort_col` / `ports_sort_desc` | int / bool | `s` `S`, header click | Ports sort column and direction |
| `procs_sort_col` / `procs_sort_desc` | int / bool | `s` `S`, header click | Processes sort column and direction |
| `conns_sort_col` / `conns_sort_desc` | int / bool | `s` `S`, header click | Connections sort column and direction |
| `group_sort_col` / `group_sort_desc` | int / bool | `s` `S`, header click | Grouped-connections sort column and direction |
| `watchlist` | []string | `w`, hand-edited | CIDR (`45.9.0.0/16`), bare IP (`8.8.8.8`) or regex (`amazonaws\|\.ru$`) tested against the remote IP and its resolved name |
| `baseline` | []string | `b` `a` `B` | Expected listeners as `proto\|bind_address` |
| `baseline_at` | RFC 3339 | `b` `a` | When the baseline was recorded |
| `alert_process_bps` | number | `,` | Alert when a process moves more than this many bytes/s |
| `alert_connection_bps` | number | `,` | Alert when one connection moves more than this many bytes/s |
| `alert_rtt_ms` | number | `,` | Alert when a connection's smoothed RTT exceeds this |
| `alert_retrans_per_min` | number | `,` | Alert when one socket retransmits more than this per minute |
| `alert_conn_growth_polls` | int | `,` | Alert after this many consecutive polls of a process's connection count only growing (a leak) |
| `alert_fanout_hosts` | int | `,` | Alert when a process reaches this many new remote hosts in one poll (a scan) |
| `alert_iface_drops_per_min` | number | `,` | Alert when one link exceeds this many errors + drops per minute |
| `alert_listen_queue_pct` | number | `,` | Alert when a TCP listener's accept queue is this many percent of its backlog (an empty queue never fires) |
| `alert_listen_drops_per_min` | number | `,` | Alert when the host drops more than this many connections per minute on the listen path (`ListenDrops`) |
| `alert_close_wait` | int | `,` | Alert when one process holds this many sockets in CLOSE_WAIT |

Every threshold is **off** when absent or `0`: the right number is entirely
site-specific, which is why there is no default. Each rule re-fires at most once
a minute per subject, and negative values are clamped to 0 rather than firing on
every poll.

`hidden_columns` takes **column keys**, not headers, and is about screen width —
a hidden column stays sortable through `s`:

```
age  closed  conns  container  count  cpu  host  iface  life  listen  local
mem  name  netns  owner  pid  process  procs  proto  queue  remote  rtt  rtx
rx  state  states  svc  tx  unit  user
```

Hiding every column of a table is ignored, since an empty table reads as a bug
rather than as a choice.

### Network ownership (`asn_database`)

Point it at an [ip2asn](https://iptoasn.com) table — the five-column TSV
(`start`, `end`, `AS number`, `country`, `AS name`), plain or gzipped, IPv4-only,
IPv6-only or combined — and an **OWNER** column appears on the Connections tab,
with the AS number and country in the detail overlay. Nothing is bundled, no
dependency is added and no lookup leaves the host; without the key the column
does not exist. Unparseable rows are skipped rather than failing the load, since
these files are concatenations, but a file with *no* usable rows is reported
rather than loading as an empty table that silently never matches.

### Key bindings (`keys`)

```json
{ "keys": { "closed": "Z", "settings": "f2" } }
```

The action names are the ones the help overlay lists. A binding is ignored —
not obeyed — when the action is unknown, the key is reserved (`ctrl+c`, `tab`,
`shift+tab`, the arrows, `pgup`/`pgdown`, `home`/`end`, `esc`, `ctrl+u`, the
digits `1`–`7`) or another action already owns the key. Rebinding an action
takes its default key away from it, so one press never does two things. Key
names are Bubble Tea's: a printable character as itself (`Z`, `!`), and
`f1`–`f12`, `ctrl+…`, `alt+…` for the rest. A name that no key ever produces is
not detected, and leaves the action unreachable. Bindings apply to the tabs
only: overlays and confirmation prompts (the kill prompt's `t`/`k`/`h`, the
views overlay's `n`/`d`) keep the letters they show on screen.

### A worked example

Bits/s, no container or service columns, a probe to the DNS resolver, a watched
netblock, and the rules that catch a bad path:

```json
{
  "rates_in_bits": true,
  "hidden_columns": ["container", "svc"],
  "latency_targets": ["1.1.1.1:53", "github.com:443"],
  "watchlist": ["45.9.0.0/16", "\\.ru$"],
  "alert_rtt_ms": 250,
  "alert_retrans_per_min": 60,
  "alert_fanout_hosts": 25,
  "alert_iface_drops_per_min": 60,
  "alert_listen_queue_pct": 80,
  "alert_close_wait": 100,
  "theme": "contrast"
}
```

Daily traffic totals live next to it in `usage.json` (40 days, the 50 biggest
processes and hosts per day) and are read by `--usage` / `--usage-all`. A day of
per-minute throughput peaks lives in `trend.json`, which is what the Overview's
"Last 24 hours" panel draws — and the only chart that survives a restart.

---

## Metrics

`--metrics` writes one scrape to stdout and `--metrics-listen ADDR` serves the
same thing at `/metrics`. From a running session (the TUI or `--daemon`) the
scrape is answered from what that session already knows, which makes it
sub-millisecond and adds the series marked **live** below. A standalone
`--metrics` run samples the host instead and emits only the host series.

| Series | Type | Labels | Meaning |
|--------|------|--------|---------|
| `ducknetview_iface_receive_bytes_per_second` | gauge | `iface`, `kind` | Current receive rate per interface |
| `ducknetview_iface_transmit_bytes_per_second` | gauge | `iface`, `kind` | Current transmit rate per interface |
| `ducknetview_iface_errors_total` | counter | `iface`, `direction` | Errors + drops since boot |
| `ducknetview_iface_up` | gauge | `iface` | 1 when the link is up |
| `ducknetview_listeners` | gauge | `exposure` | Listening sockets by reachability (`exposed`, `lan`, `local`, `netns` — inside another network namespace and not published on the host) |
| `ducknetview_listeners_off_baseline` | gauge | — | Listeners not in the saved baseline; **-1 when no baseline is saved**, which is different from clean |
| `ducknetview_connections` | gauge | `scope` | Active connections by remote scope |
| `ducknetview_connection_retransmits_total` | gauge | — | Retransmits summed over live TCP sockets |
| `ducknetview_processes_with_sockets` | gauge | — | Processes holding at least one socket |
| `ducknetview_listener_queue_ratio` | gauge | `local`, `process` | A TCP listener's accept-queue depth as a fraction of its backlog; 1 is full (Linux) |
| `ducknetview_listener_accept_queue` | gauge | `local`, `process` | Connections waiting to be accepted on a TCP listener (Linux) |
| `ducknetview_tcp_counter_total` | counter | `counter` | Host-wide TCP counters since boot: `listen_overflows`, `listen_drops`, `syncookies_sent`, `retrans_segs`, `timeouts`, `in_errs`, `out_rsts` (Linux) |
| `ducknetview_build_info` | gauge | `version` | Always 1; the label carries the version — **live** |
| `ducknetview_session_uptime_seconds` | gauge | — | How long this session has been running — **live** |
| `ducknetview_last_poll_age_seconds` | gauge | — | Seconds since the last completed socket enumeration, **-1 before the first** — **live** |
| `ducknetview_polls_total` | counter | — | Socket enumerations completed — **live** |
| `ducknetview_paused` | gauge | — | 1 while sampling is paused — **live** |
| `ducknetview_session_bytes_total` | counter | `direction` | Bytes accounted while this session has been watching — **live** |
| `ducknetview_events_total` | counter | `kind` | Events recorded, by kind (see below) — **live** |
| `ducknetview_alerts_total` | counter | — | Alert-level events raised — **live** |
| `ducknetview_alert_hook_failures_total` | counter | — | `--on-alert` invocations that failed — **live** |
| `ducknetview_remote_up` | gauge | `host` | Whether the last fetch from a `--from ssh://` host succeeded; absent for a local session — **live** |

The rule worth writing first is on the monitor itself. A daemon that is up but
no longer polling looks healthy in every other series here:

```
- alert: DucknetviewStalled
  expr: ducknetview_last_poll_age_seconds > 60 or ducknetview_last_poll_age_seconds == -1
  for: 2m
```

### Event kinds

These are the `kind` values in `--log-events` JSONL, in `DNV_EVENT_KIND` for
`--on-alert`, and on the `ducknetview_events_total` label — so they are what a
hook or an alert rule matches on.

| Kind | Level | Raised when |
|------|-------|-------------|
| `listener_up` | warn, **alert** when off-baseline | A listening socket appeared. The detail carries its exposure and the owning systemd unit |
| `listener_down` | info | A listening socket went away |
| `baseline_drift` | **alert** | The session's first poll found listeners already outside the saved baseline. Once per session; the detail lists the first five |
| `listener_moved` | info, **alert** when exposure widens | A service rebound to a different address on the same port |
| `watchlist_hit` | **alert** | A connection matched the watchlist |
| `new_destination` | info | First connection of the session to a public host |
| `iface_added` / `iface_removed` | info / warn | An interface appeared or vanished (a VPN connecting, say) |
| `iface_state` | info, warn on down | An interface changed up/down state |
| `link_errors` | warn | `alert_iface_drops_per_min` exceeded on one link |
| `throughput_process` | **alert** | `alert_process_bps` exceeded |
| `throughput_connection` | **alert** | `alert_connection_bps` exceeded |
| `latency` | warn | `alert_rtt_ms` exceeded |
| `packet_loss` | warn | `alert_retrans_per_min` exceeded |
| `conn_growth` | warn | `alert_conn_growth_polls` consecutive polls of growth — a leak |
| `fanout` | **alert** | `alert_fanout_hosts` new remotes in one poll — a scan |
| `listen_queue` | warn | A listener's accept queue reached `alert_listen_queue_pct` of its backlog |
| `listen_drops` | warn | The host dropped more than `alert_listen_drops_per_min` connections a minute on the listen path |
| `close_wait` | warn | A process holds `alert_close_wait` sockets in CLOSE_WAIT — it is not closing what its peers closed |
| `config_reloaded` | info, warn when part was ignored | `SIGHUP` re-read the config |
| `attribution_limited` | info | Sockets owned by another user have no PID, so rows cannot be named or signalled. Once per session, and never for root |
| `netns_limited` | info | Sockets in other network namespaces are incomplete: other users' namespaces are unreadable, byte counters there need root, or more than 32 namespaces exist. Once per session |
| `link_errors_cleared` | info | The link has been under `alert_iface_drops_per_min` for a minute |
| `throughput_process_cleared` | info | The process has been under `alert_process_bps` for a minute |
| `throughput_connection_cleared` | info | The connection has been under `alert_connection_bps` for a minute — including because it closed |
| `latency_cleared` | info | The connection has been under `alert_rtt_ms` for a minute |
| `packet_loss_cleared` | info | The socket has been under `alert_retrans_per_min` for a minute |
| `listen_queue_cleared` | info | The listener's accept queue has been under `alert_listen_queue_pct` for a minute |
| `listen_drops_cleared` | info | The host has been under `alert_listen_drops_per_min` for a minute |
| `close_wait_cleared` | info | The process has been under `alert_close_wait` for a minute — including because it exited |

Only alert-level events run `--on-alert`, and only they count toward the tab
badge and `ducknetview_alerts_total`. The `_cleared` kinds are deliberately
info-level: they are what a pager needs to resolve an incident, not a new one.
`fanout` and `conn_growth` have no `_cleared` counterpart — a burst is a moment
rather than a state, and no single poll can know that a scan has stopped.

---

## Project layout

```
cmd/ducknetview/   - main entry point, every non-interactive mode (--once / --json /
                     --from / --diff / --daemon / --metrics / --top / --usage /
                     --check-baseline / --check-config / --save-baseline /
                     --version) and the HTTP metrics exporter
internal/probe/    - data collection: interfaces & rates, sockets (ports/procs/conns),
                     per-socket byte counters, RTT and retransmits via netlink,
                     container ids, process enrichment, service names, IP scope &
                     listener exposure, routing tables, the netlink neighbour cache,
                     TCP latency probes, Wi-Fi radio state, ASN ownership lookups,
                     listener accept queues, host TCP counters
internal/ui/       - Bubble Tea model, views, styles & themes, sparklines, reverse DNS,
                     throughput rates, session totals, the 24h trend, closed-connection
                     history, event log, alert rules, listener baseline, row detail /
                     kill / settings overlays, quick filters, drill-down, watchlist,
                     process tree, keymap, column specs, CSV export, Prometheus host
                     and session metrics, daily usage history, snapshot viewer & diff,
                     headless mode, live remote sessions, config + its validation
internal/version/  - the single source of truth for the version number
e2e/               - the end-to-end suite and the container it runs in: the program
                     against a real host whose sockets the test controls
man/               - the manual page
completions/       - bash and zsh completions
.github/workflows/ - CI: fmt, vet, race tests, cross-compile, staticcheck, e2e, .deb
resources/         - screenshots
DEBIAN/            - control files for the .deb package
packaging/         - the systemd unit shipped with the .deb
CHANGELOG.md       - what changed in each release
ROADMAP.md         - the feature backlog, in priority tiers
```

---

## Notes

- Process ↔ port mapping may require elevated privileges depending on OS.
- Signalling a process (`x`) offers SIGTERM / SIGKILL / SIGHUP and only succeeds for processes you own (or when running with sufficient privileges); failures are reported on the status line.
- Settings are persisted to `${XDG_CONFIG_HOME:-~/.config}/ducknetview/config.json` as you change them — including the quick filters, the search mode and the last open tab. Delete that file to reset to defaults; malformed contents are ignored, and out-of-range values fall back to the default rather than wedging the UI. See [Configuration](#configuration) for the full key list.
- **Alert thresholds** have no default, because the right number is entirely site-specific; set them in the settings overlay (`,`) or by hand. See [Configuration](#configuration) for all seven rules. Each one re-fires at most once a minute per subject.
- `--on-alert` runs its command with the event's kind, subject and detail as arguments and `DNV_EVENT_KIND/LEVEL/SUBJECT/DETAIL/AT` in the environment. It is executed directly, never through a shell, so a process name or remote address can't be interpreted as a command; a hook is given 10 seconds before it is killed.
- The **baseline** is stored in that same file as a `"baseline"` array of `"proto|bind_address"` entries plus a `"baseline_at"` timestamp. Listeners inside other network namespaces are not part of it. UDP sockets on ephemeral ports are deliberately excluded: UDP has no LISTEN state, so every outbound DNS query would otherwise register as a new listener and leave the baseline permanently dirty. They are still shown in the Ports table, and `--check-baseline` reports how many listeners it actually compared rather than how many exist.
- The **watchlist** is a `"watchlist"` array in that config file: each entry is a CIDR (`45.9.0.0/16`), a bare IP (`8.8.8.8`), or a regular expression tested against the remote IP *and* its resolved hostname (`amazonaws|\.ru$`). Matching connections are highlighted and logged as events. `w` on the Connections tab adds and removes the highlighted remote; hand-editing is only needed for CIDRs and regexes.
- `e` exports the active tab to `ducknetview_<tab>_<timestamp>.csv` in the current working directory.
- Service names come from `/etc/services` when readable, with a small built-in fallback for common ports.
- **Throughput** is read from the kernel's per-socket counters (`tcpi_bytes_acked` / `tcpi_bytes_received`) over netlink `sock_diag`, the same source as `ss -ti`, and turned into a rate by diffing consecutive polls. This is Linux-only and TCP-only: UDP has no equivalent accounting, and neither do other platforms, where the RX/TX columns are hidden rather than shown empty. No elevated privileges are needed for the host's own sockets — the kernel reports other users' sockets to unprivileged callers, exactly as `ss` does; sockets inside other network namespaces (containers) need root. Rates appear one poll after a connection is first seen, since a rate needs two samples.
- **CPU%** is sampled between polls rather than averaged over the process lifetime, so a process that has just started working shows it. It can exceed 100% on multi-core hosts. The very first sighting of a PID falls back to the lifetime average, because one sample can't make a rate.
- <a id="network-namespaces"></a>**Network namespaces.** `/proc/net/*` only shows ducknetview's own namespace, so containers on bridge networks were invisible. Each poll groups processes by `/proc/<pid>/ns/net` and reads every other namespace's socket tables through one member's `/proc/<pid>/net/{tcp,tcp6,udp,udp6}`, attributing sockets to that namespace's processes by their `/proc/<pid>/fd`. Unprivileged, other users' namespace links are unreadable, so root-owned containers stay invisible; byte counters inside a namespace need `setns` and therefore root, so without it those rows show no throughput. Either is recorded once per session as a `netns_limited` event. A container listener counts as published when a `docker-proxy` forwards to it (matched by container address and port); with `userland-proxy: false` Docker publishes through iptables alone, and those listeners stay classed `netns`. At most 32 namespaces are scanned per poll. Container listeners are not baselined — the publishing host port is.
- **Container ids** are read from `/proc/<pid>/cgroup` and shortened to 12 characters. Names are not resolved: that would mean talking to each runtime's API.
- **Systemd units** come from the same read: the innermost `.service` or `.scope` on the cgroup path, preferring the unified (v2) hierarchy, then cgroup v1's `name=systemd` one. Slices are skipped (they group units; nobody starts one), and so is a container runtime's own scope, which the CONTAINER column already covers.
- Reverse DNS lookups have a 1.5 s timeout and are cached per session.
- External IP fetch uses a fresh HTTP transport each time (avoids stale keep-alive sockets after VPN / route changes).
- Copy-to-clipboard (`y`) uses `atotto/clipboard`. On Linux this shells out to `xclip`/`xsel` (X11) or `wl-copy` (Wayland) — install one of them, otherwise the copy fails with a status message.
- **Session totals** are accumulated from the per-poll deltas, so they cover what happened while the tool was watching — not the whole life of a socket that predates it. The per-host and per-process tables are capped at 2048 entries, evicting the quietest first.
- **Closed connections** keep the socket's final counters, which *are* its lifetime totals. The last 200 are retained; signalling from a closed row is deliberately disabled, since the PID may already have been recycled onto an unrelated process.
- The **event log** records *changes*, so the first poll after startup establishes a baseline rather than reporting every existing listener and interface as new — except that listeners outside a saved listener baseline are reported once, as `baseline_drift`. It keeps its last 500 entries in memory; use `--log-events` for anything that needs to outlive the session. That file is rotated at 8 MiB, keeping one previous generation as `.1`.
- **Latency probes** measure a TCP handshake, counting a refused connection as a valid timing (the RST travelled the same path a SYN-ACK would have). Only the default gateways are probed by default (one per address family), so nothing leaves the LAN unless `latency_targets` says so. A `--json` snapshot carries one such probe, measured while the sampler is already waiting out its second, so it costs no extra time.
- **`--daemon`** runs the same model under Bubble Tea with no renderer and no input attached, so there is one implementation of the polling, the rates and the rules rather than two. It takes the ordinary exit path on `SIGINT`/`SIGTERM`, which is what writes the day's usage totals — a hard stop would lose them.
- **`--from ssh://host`** executes `ssh -o BatchMode=yes HOST ducknetview --json` directly, never through a shell, and the arguments are a fixed template rather than anything taken from the target, so a host name cannot smuggle in options. One fetch is bounded at 20 seconds and a second is never started behind a slow one. `BatchMode` means it will not sit waiting for a password; set up a key or an agent first.
- **The 24-hour trend** records the *peak* of each minute rather than the mean: a ten-second burst averaged over sixty seconds looks like nothing. Gaps render as gaps rather than being compressed away, so a restart does not silently rewrite the time axis. A snapshot or a remote session never writes to it — those are another machine's numbers.
- **The process tree** uses each process's parent as reported by `/proc`. A process whose parent holds no sockets (so is not in the table) is shown as a root rather than hidden, and a cycle — `/proc` has been known to report odd parentage in namespaces — is broken rather than followed. `A` and `T` are exclusive: folding by name has already discarded the relationships the tree is drawn from.
- **Routes, resolvers and radio state** come from `/proc` and `/etc/resolv.conf`, so they are Linux-only. The **neighbour table** is an `rtnetlink` dump, which is the only place the kernel keeps IPv6 neighbours at all; it falls back to `/proc/net/arp` (IPv4 only) where that is unavailable. MAC vendors come from a small built-in list, not the full IEEE registry.
- **Reverse-DNS caches** are bounded at 4096 entries, pruned down to the addresses still connected.
- Primarily tested on Linux. The throughput columns, container ids, routing/neighbour tables and Wi-Fi state are Linux-only; everything else is portable, and CI cross-compiles for macOS, Windows and FreeBSD to keep it that way.

---

## License

MIT
