# ducknetview 🦆

Minimalistic **network monitoring TUI** written in **Go**.

Current version: **0.0.7**

Built with:
- **Bubble Tea** + **bubbles** (UI framework)
- **lipgloss** (styling)
- **tcell** (terminal backend, kept in the build for completeness)
- **gopsutil/v4** (network / process info)
- **atotto/clipboard** (copy-to-clipboard)

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
    - **Security summary** — at-a-glance counts of exposed listeners, public connections, and watchlist hits
    - Selected interface summary with RX/TX rate, cumulative RX/TX totals, and sparkline charts
    - **Total throughput** across all real interfaces (loopback excluded) with current + session-peak RX/TX and sparklines
    - **External IP** (fetched from `api.ipify.org`, auto-refreshed every 30 s, manual refresh with `ctrl+e`)
    - The "Selected interface" is whichever interface is highlighted on the **Interfaces** tab (defaults to the first interface until you pick one)

- **Interfaces tab**
    - Scrollable interface list (left pane)
    - Auto-updating details on selection change (no Enter required)
    - Per-interface RX/TX sparkline charts
    - **Hide-noise** toggle (`h`): drops loopback, docker, bridge, veth and down interfaces from the list so the real NICs stand out
    - Copy the selected interface summary to the clipboard with `y`

- **Ports tab**
    - Open listening TCP / UDP ports
    - PID and process name (best-effort)
    - Bind addresses are **colored by exposure**: orange = reachable off-host (`0.0.0.0`/`::` or a routable address), amber = private/LAN, dim = loopback
    - Listeners that **appeared since the last poll** are highlighted (green protocol) with a count in the header — a new listener can signal a backdoor or a regression
    - **Service name** column (well-known ports, e.g. `443 → https`, from `/etc/services` with a built-in fallback)
    - Selectable list (`↑↓` moves a row cursor); copy the highlighted row with `y`
    - **Sortable** (`s` cycles `proto → port → pid → process`, `S` reverses)
    - Search (`/`) by port, address, protocol, service or process name

- **Processes tab**
    - Processes ranked by network connection count (top 80)
    - Connection count, listening-socket count, and **CPU% / memory%** per process
    - Selectable list (`↑↓` moves a row cursor); copy the highlighted row with `y`
    - **Sortable** (`s` cycles `conns → listen → cpu → mem → pid → name`, `S` reverses)
    - Search (`/`) by process name or PID

- **Connections tab**
    - All active (non-listening) TCP/UDP connections
    - Local / remote address, state, PID, process
    - Remote addresses are **colored by scope** (public / private / loopback), and connections that appeared since the last poll are highlighted, with a `Δ +new/-closed` counter
    - **AGE column** — how long each connection has been seen this session (tracked across polls)
    - **Watchlist** — remotes matching your configured IPs / CIDRs / regex (see config) are highlighted in magenta
    - **Quick filters**: protocol (`p`: all/tcp/udp), IP version (`v`: all/v4/v6), state (`f`: all/established/active) and public-remotes-only (`o`); ports share the `p` / `v` filters
    - Optional **reverse DNS** for remote addresses (toggle with `r`); resolved names replace the remote IP and lookups are cached for the session
    - Optional **grouping** view (toggle with `g`): collapses connections by remote host, showing the connection count, a per-state breakdown, and the processes talking to each host
    - Selectable list (`↑↓` moves a row cursor); copy the highlighted row (or group) with `y`
    - **Sortable** (`s` cycles `pid → proto → state → local → remote → process → scope → age`, or `conns → host` in grouped view; `S` reverses)
    - Search (`/`) by local / remote address (raw IP or resolved name), process, state or protocol

- **Row details & actions** (Ports / Processes / Connections)
    - Press `enter` for a full, un-truncated detail overlay of the highlighted row (protocol, addresses, resolved service, exposure/scope, watchlist status, state, and the owning process's user / PPID / command line / CPU / memory)
    - Press `x` to signal the highlighted row's process — the confirmation offers `t` SIGTERM · `k` SIGKILL · `h` SIGHUP (may need privileges)
    - Press `e` to export the current (filtered / sorted) table to a timestamped CSV in the working directory

- **Non-interactive snapshot** — `ducknetview --once` prints a one-shot text snapshot (interfaces, ports, connections) and exits; `--json` emits a fuller snapshot (also including processes) as JSON for scripting

- **Refresh control**
    - `space` pauses / resumes auto-refresh (a `⏸ PAUSED` marker shows in the header)
    - `+` / `-` make the interface-sample interval faster / slower (1 s → 10 s)

- **Persisted settings**
    - Sort keys/direction, the hide-noise / reverse-DNS / grouping toggles and the refresh interval are saved to `${XDG_CONFIG_HOME:-~/.config}/ducknetview/config.json` as you change them and restored on next launch

Interface counters are sampled on the chosen interval (default every second). The ports, processes and connections views are all derived from a **single socket enumeration** taken at most every 5 seconds (one `gopsutil` pass feeds all three, with a shared process-name cache).

Copied rows are tab-separated, so they paste cleanly into a spreadsheet or editor. The active sort key is shown in each table's header.

Press `?` at any time for an in-app key-binding overlay. The mouse is supported too: click a row to select it, or use the wheel to scroll.

---

## Key bindings

### Global

| Key                  | Action |
|----------------------|--------|
| `?`                  | Toggle the help overlay (any key closes it) |
| `←` `→`              | Switch tabs |
| `tab` / `shift+tab`  | Cycle tabs |
| `1`–`5`              | Jump directly to a tab |
| `enter`              | Show a detail overlay for the highlighted row (Ports / Processes / Connections) |
| `y`                  | Copy the highlighted row / selected interface to the clipboard |
| `e`                  | Export the current table view to a CSV file |
| `x`                  | Signal the highlighted row's process — confirm with `t`/`k`/`h` (TERM/KILL/HUP) |
| `space`              | Pause / resume auto-refresh |
| `+` / `-`            | Faster / slower refresh interval |
| `p` / `v`            | Cycle protocol / IP-version filter (Ports & Connections) |
| `f`                  | Cycle connection state filter (Connections) |
| `o`                  | Toggle public-remotes-only filter (Connections) |
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

### Search (Ports / Processes / Connections)

| Key      | Action |
|----------|--------|
| `/`      | Start search |
| `Enter`  | Apply search filter |
| `Esc`    | Exit search |
| `Ctrl+u` | Clear query (while searching, or when search not active to clear active filter) |

### Connections tab only

| Key | Action |
|-----|--------|
| `r` | Toggle reverse DNS for remote addresses |
| `g` | Toggle grouping view |

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
cmd/ducknetview/   - main entry point + non-interactive snapshot (--once / --json)
internal/probe/    - data collection: interfaces & rates, sockets (ports/procs/conns),
                     process enrichment, service names, IP scope & listener exposure
internal/ui/       - Bubble Tea model, views, styles, sparklines, reverse DNS,
                     row detail / kill overlays, quick filters, watchlist, CSV export, config
resources/         - screenshots
DEBIAN/            - control files for the .deb package
```

---

## Notes

- Process ↔ port mapping may require elevated privileges depending on OS.
- Signalling a process (`x`) offers SIGTERM / SIGKILL / SIGHUP and only succeeds for processes you own (or when running with sufficient privileges); failures are reported on the status line.
- Settings are persisted to `${XDG_CONFIG_HOME:-~/.config}/ducknetview/config.json` as you change them. Delete that file to reset to defaults; malformed contents are ignored. (Quick filters are session-only and not persisted.)
- The **watchlist** is a `"watchlist"` array in that config file, edited by hand — each entry is a CIDR (`45.9.0.0/16`), a bare IP (`8.8.8.8`), or a regular expression tested against the remote IP and resolved hostname (`amazonaws|\.ru$`). Matching connections are highlighted.
- `e` exports the active tab to `ducknetview_<tab>_<timestamp>.csv` in the current working directory.
- CPU% is averaged over each process's lifetime (cheap to read), not an instantaneous sample, and can exceed 100% on multi-core hosts.
- Service names come from `/etc/services` when readable, with a small built-in fallback for common ports.
- Per-process bandwidth is **not** implemented (would require eBPF / netlink accounting).
- Reverse DNS lookups have a 1.5 s timeout and are cached per session.
- External IP fetch uses a fresh HTTP transport each time (avoids stale keep-alive sockets after VPN / route changes).
- Copy-to-clipboard (`y`) uses `atotto/clipboard`. On Linux this shells out to `xclip`/`xsel` (X11) or `wl-copy` (Wayland) — install one of them, otherwise the copy fails with a status message.
- Primarily tested on Linux.

---

## License

MIT
