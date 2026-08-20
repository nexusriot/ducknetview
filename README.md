# ducknetview 🦆

Minimalistic **network monitoring TUI** written in **Go**.

Current version: **0.3.0**

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
    - **Gateway and DNS** — the default route in use and the nameservers configured, on one line
    - **Path latency** — TCP-handshake round trip to the default gateway (and any extra targets in the config), with a short history each
    - **Top talkers (session)** — the processes and remote hosts that have moved the most data since launch, with the session grand total
    - **Connection churn** — connections opened and closed per poll, as a chart. A spike is how a leak or a scan announces itself
    - Panels appear once they have something to say, and step aside on a terminal too short to hold them
    - Selected interface summary with RX/TX rate, cumulative RX/TX totals, and sparkline charts
    - **Total throughput** across all real interfaces (loopback excluded) with current + session-peak RX/TX and sparklines
    - **External IP** (fetched from `api.ipify.org`, auto-refreshed every 30 s, manual refresh with `ctrl+e`)
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
    - Bind addresses are **colored by exposure**: orange = reachable off-host (`0.0.0.0`/`::` or a routable address), amber = private/LAN, dim = loopback
    - Listeners that **appeared since the last poll** are highlighted (green protocol) with a count in the header — a new listener can signal a backdoor or a regression
    - **Service name** column (well-known ports, e.g. `443 → https`, from `/etc/services` with a built-in fallback)
    - **USER** column showing each listener's owning user
    - **Baseline** — press `b` to record the current listeners as the expected set; anything outside it is flagged in red on the protocol cell, counted in the header and on the Overview, and reported by `--check-baseline`. `a` accepts just the highlighted listener into the baseline (and takes it back out), which is what you want when one new service is fine but the rest of the diff is not; `B` clears the baseline
    - Selectable list (`↑↓` moves a row cursor); copy the highlighted row with `y`
    - **Sortable** (`s` cycles `proto → port → pid → process → user`, `S` reverses)
    - Search (`/`) by port, address, protocol, service, user or process name

- **Processes tab**
    - Processes ranked by network connection count (top 80)
    - Connection count, listening-socket count, and **CPU% / memory%** per process (CPU is measured over the poll interval, not averaged over the process lifetime)
    - **RX / TX throughput** per process, summed across its connections — press `t` to swap the columns between the current rate and the **session total** (the sort keys follow the mode)
    - **USER** column, and a **CONTAINER** column that appears when any process is running in a container (docker / podman / containerd / CRI-O / LXC)
    - **`A` folds processes that share a name into one row** — a browser is forty processes, and the useful row is their sum. Counts, CPU, memory and throughput add up; columns whose members disagree (different users, say) show `*`
    - Selectable list (`↑↓` moves a row cursor); copy the highlighted row with `y`
    - **Sortable** (`s` cycles `conns → listen → cpu → mem → rx → tx → pid → name → user`, `S` reverses)
    - Search (`/`) by process name, PID, user or container id — searching a container id doubles as a per-container filter

- **Connections tab**
    - All active (non-listening) TCP/UDP connections
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
    - Selectable list (`↑↓` moves a row cursor); copy the highlighted row (or group) with `y`
    - **Sortable** (`s` cycles `pid → proto → state → local → remote → process → scope → age → rx → tx`, or `conns → host` in grouped view; `S` reverses)
    - Search (`/`) by local / remote address (raw IP or resolved name), process, service, state or protocol

- **Routes tab**
    - The default route actually in use, the configured nameservers (local stubs marked as such), the full IPv4 and IPv6 routing tables, and the neighbour (ARP) table with a best-effort MAC vendor
    - Read on its own slow cadence, since none of it changes often. IPv6 neighbours are not shown: the kernel keeps them in netlink only, and the view says so rather than leaving a silent gap

- **Events tab**
    - A timestamped log of what *changed*, which the live tables can't show you: listeners appearing and disappearing, watchlist hits, the first connection to each new public host, and interfaces going up, down, appearing or vanishing
    - Levels are colour-coded (info / warn / **alert**) and the tab label carries an alert count, so a new listener is visible from any other tab
    - `/` searches the log and `l` cycles a level filter (all / warn and above / alerts only)
    - **Thresholds and rules**, all opt-in through the config, each firing at most once a minute per subject: throughput per process or per connection (`alert_process_bps`, `alert_connection_bps`), round-trip time (`alert_rtt_ms`), retransmits per minute on one socket (`alert_retrans_per_min`), a process whose connection count only ever grows (`alert_conn_growth_polls` — a leak), and a process reaching many new hosts at once (`alert_fanout_hosts` — what a scan looks like from the inside). With `--on-alert` each alert also runs a command of your choosing
    - **`listener_moved`** — a service that disappears and reappears on the same port under the same process is reported as one event rather than two, and as an **alert** when the move widens its exposure (`127.0.0.1:5432` → `0.0.0.0:5432` is the case worth catching)
    - `d` acknowledges alerts: the tab badge clears without destroying the log
    - Holds the most recent 500 events in memory; `c` clears them, `e` exports them to CSV, and `--log-events` writes every event to a file as it happens
    - One poll can contribute at most 20 events per kind, with an explicit "N more … suppressed" entry so truncation is never silent

- **Row details & actions** (Ports / Processes / Connections / Events)
    - Press `enter` for a full detail overlay of the highlighted row (protocol, addresses, resolved service, exposure/scope, watchlist status, baseline status, age, throughput and RTT, and the owning process's user / container / PPID / command line / CPU / memory). Long command lines wrap instead of being cut off; `↑↓` scrolls, `y` copies it as plain text, and it keeps updating while open
    - Press `x` to signal the highlighted row's process — the confirmation offers `t` SIGTERM · `k` SIGKILL · `h` SIGHUP (may need privileges)
    - Press `e` to export the current (filtered / sorted) table to a timestamped CSV in the working directory

- **Non-interactive modes**
    - `ducknetview --once` prints a one-shot text snapshot (interfaces, ports, connections) and exits; `--json` emits a fuller snapshot (also including processes) as JSON for scripting
    - `ducknetview --check-baseline` compares the host's listeners against the saved baseline and exits **1** if any are unexpected (**0** when clean, **2** when no baseline has been saved) — enough for cron or a monitoring check
    - `ducknetview --save-baseline FILE` writes a **portable baseline**; `--check-baseline --baseline FILE` checks against it. Build one on a known-good host and ship it to the fleet
    - `ducknetview --tab conns` opens straight onto a tab
    - `ducknetview --from snap.json` browses a `--json` snapshot instead of the live host, frozen and clearly marked. `-` reads stdin, so `ssh host ducknetview --json | ducknetview --from -` browses a remote host with no agent to install
    - `ducknetview --metrics` prints the host's network state in Prometheus text format, for a node_exporter textfile directory
    - `ducknetview --top 10` prints the busiest processes and hosts and exits
    - `ducknetview --usage 7` prints the recorded daily traffic totals (`--usage-all` for everything kept)
    - `ducknetview --version` prints the version

- **Refresh control**
    - `space` pauses / resumes auto-refresh (a `⏸ PAUSED` marker shows in the header)
    - `+` / `-` make the interface-sample interval faster / slower (1 s → 10 s)
    - `R` refreshes everything immediately

- **Daily usage history**
    - Each session's totals are merged into `usage.json` on exit, per day, keeping 40 days and the 50 biggest processes and hosts per day — enough to answer "where did this month's data go" without keeping a database

- **Search**
    - `/` filters the table; prefix the query with `re:` to use a **regular expression** (case-insensitive, and an invalid one says so instead of silently matching nothing)
    - `\` switches between **filtering** (hide non-matching rows) and **highlighting** (keep every row, mark the matches), and `n` / `N` walk the cursor between matches

- **Persisted settings**
    - Sort keys/direction, the hide-noise / reverse-DNS / grouping / totals toggles, the quick filters, the search mode, the refresh interval, the last open tab and the listener baseline are saved to `${XDG_CONFIG_HOME:-~/.config}/ducknetview/config.json` as you change them and restored on next launch

Interface counters are sampled on the chosen interval (default every second). The ports, processes and connections views are all derived from a **single socket enumeration** (one `gopsutil` pass feeds all three, with shared process-name, user and container caches). Enumeration follows the chosen refresh interval but never runs more often than every 2 seconds, since it costs roughly twenty times an interface sample.

Copied rows are tab-separated, so they paste cleanly into a spreadsheet or editor. The active sort column is marked with an arrow in the table header, and clicking a header sorts by that column (clicking it again reverses).

Press `?` at any time for an in-app key-binding overlay. The mouse is supported too: click a row to select it, or use the wheel to scroll.

---

## Key bindings

### Global

| Key                  | Action |
|----------------------|--------|
| `?`                  | Toggle the help overlay (any key closes it) |
| `←` `→`              | Switch tabs |
| `tab` / `shift+tab`  | Cycle tabs |
| `1`–`7`              | Jump directly to a tab |
| `enter`              | Show a detail overlay for the highlighted row (`↑↓` scrolls it, `y` copies it, any other key closes it) |
| `y`                  | Copy the highlighted row / selected interface to the clipboard |
| `e`                  | Export the current table view to a CSV file |
| `x`                  | Signal the highlighted row's process — confirm with `t`/`k`/`h` (TERM/KILL/HUP) |
| `space`              | Pause / resume auto-refresh |
| `+` / `-`            | Faster / slower refresh interval |
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
| `h`                  | Toggle the hide-noise interface filter |
| `ctrl+e`             | Refresh external IP now |
| `ctrl+c`             | Quit |

### Lists / Viewports

| Key            | Action |
|----------------|--------|
| `↑ ↓`          | Move the row cursor (or scroll the interface list) |
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

### Search (Ports / Processes / Connections)

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

### Events tab only

| Key | Action |
|-----|--------|
| `l` | Cycle the level filter (all / warn+ / alerts) |
| `d` | Acknowledge alerts (clears the badge, keeps the log) |
| `c` | Clear the recorded events |

---

## Build & run

```bash
go mod tidy
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

### Debian package

Two helper scripts are included for producing `.deb` packages:

```bash
./build-deb.sh           # amd64 (default)
./build-deb.sh arm64     # cross-build for arm64
./build-deb-arm64.sh     # convenience wrapper for arm64
```

Output lands in `build/ducknetview_<version>_<arch>.deb`.

`dpkg-deb` is required. When cross-compiling, `CGO_ENABLED=0` is used.

---

## Project layout

```
cmd/ducknetview/   - main entry point + non-interactive modes (--once / --json /
                     --check-baseline / --version)
internal/probe/    - data collection: interfaces & rates, sockets (ports/procs/conns),
                     per-socket byte counters via netlink, container ids, process
                     enrichment, service names, IP scope & listener exposure
internal/ui/       - Bubble Tea model, views, styles, sparklines, reverse DNS,
                     throughput rates, event log, listener baseline, row detail /
                     kill overlays, quick filters, watchlist, CSV export, config
internal/version/  - the single source of truth for the version number
man/               - the manual page
completions/       - bash and zsh completions
.github/workflows/ - CI: fmt, vet, race tests, cross-compile, staticcheck, .deb
resources/         - screenshots
DEBIAN/            - control files for the .deb package
```

---

## Notes

- Process ↔ port mapping may require elevated privileges depending on OS.
- Signalling a process (`x`) offers SIGTERM / SIGKILL / SIGHUP and only succeeds for processes you own (or when running with sufficient privileges); failures are reported on the status line.
- Settings are persisted to `${XDG_CONFIG_HOME:-~/.config}/ducknetview/config.json` as you change them. Delete that file to reset to defaults; malformed contents are ignored. (Quick filters are session-only and not persisted.)
- **Throughput thresholds** are hand-edited into the config as `"alert_process_bps"` and `"alert_connection_bps"` (bytes per second; 0 or absent disables them). The right number is entirely site-specific, which is why there is no default. A breached threshold re-fires at most once a minute per process or connection.
- `--on-alert` runs its command with the event's kind, subject and detail as arguments and `DNV_EVENT_KIND/LEVEL/SUBJECT/DETAIL/AT` in the environment. It is executed directly, never through a shell, so a process name or remote address can't be interpreted as a command; a hook is given 10 seconds before it is killed.
- The **baseline** is stored in that same file as a `"baseline"` array of `"proto|bind_address"` entries plus a `"baseline_at"` timestamp. UDP sockets on ephemeral ports are deliberately excluded: UDP has no LISTEN state, so every outbound DNS query would otherwise register as a new listener and leave the baseline permanently dirty. They are still shown in the Ports table.
- The **watchlist** is a `"watchlist"` array in that config file, edited by hand — each entry is a CIDR (`45.9.0.0/16`), a bare IP (`8.8.8.8`), or a regular expression tested against the remote IP and resolved hostname (`amazonaws|\.ru$`). Matching connections are highlighted.
- `e` exports the active tab to `ducknetview_<tab>_<timestamp>.csv` in the current working directory.
- Service names come from `/etc/services` when readable, with a small built-in fallback for common ports.
- **Throughput** is read from the kernel's per-socket counters (`tcpi_bytes_acked` / `tcpi_bytes_received`) over netlink `sock_diag`, the same source as `ss -ti`, and turned into a rate by diffing consecutive polls. This is Linux-only and TCP-only: UDP has no equivalent accounting, and neither do other platforms, where the RX/TX columns are hidden rather than shown empty. No elevated privileges are needed — the kernel reports other users' sockets to unprivileged callers, exactly as `ss` does. Rates appear one poll after a connection is first seen, since a rate needs two samples.
- **CPU%** is sampled between polls rather than averaged over the process lifetime, so a process that has just started working shows it. It can exceed 100% on multi-core hosts. The very first sighting of a PID falls back to the lifetime average, because one sample can't make a rate.
- **Container ids** are read from `/proc/<pid>/cgroup` and shortened to 12 characters. Names are not resolved: that would mean talking to each runtime's API.
- Reverse DNS lookups have a 1.5 s timeout and are cached per session.
- External IP fetch uses a fresh HTTP transport each time (avoids stale keep-alive sockets after VPN / route changes).
- Copy-to-clipboard (`y`) uses `atotto/clipboard`. On Linux this shells out to `xclip`/`xsel` (X11) or `wl-copy` (Wayland) — install one of them, otherwise the copy fails with a status message.
- **Session totals** are accumulated from the per-poll deltas, so they cover what happened while the tool was watching — not the whole life of a socket that predates it. The per-host and per-process tables are capped at 2048 entries, evicting the quietest first.
- **Closed connections** keep the socket's final counters, which *are* its lifetime totals. The last 200 are retained; signalling from a closed row is deliberately disabled, since the PID may already have been recycled onto an unrelated process.
- The **event log** keeps its last 500 entries in memory; use `--log-events` for anything that needs to outlive the session. That file is rotated at 8 MiB, keeping one previous generation as `.1`.
- **Latency probes** measure a TCP handshake, counting a refused connection as a valid timing (the RST travelled the same path a SYN-ACK would have). Only the default gateway is probed by default, so nothing leaves the LAN unless `latency_targets` says so.
- **Routes, neighbours, resolvers and radio state** come from `/proc` and `/etc/resolv.conf`, so they are Linux-only. MAC vendors come from a small built-in list, not the full IEEE registry.
- **Reverse-DNS caches** are bounded at 4096 entries, pruned down to the addresses still connected. It records changes, so the first poll after startup establishes a baseline rather than reporting every existing listener as new.
- Primarily tested on Linux. The throughput columns, container ids, routing/neighbour tables and Wi-Fi state are Linux-only; everything else is portable, and CI cross-compiles for macOS, Windows and FreeBSD to keep it that way.

---

## License

MIT
