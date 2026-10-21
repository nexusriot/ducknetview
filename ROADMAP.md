# Roadmap

The 0.3.1 backlog — all fourteen items — shipped in 0.4.0, and this backlog's
own Tier 1 shipped in 0.5.0, and the startup baseline-drift alert in 0.6.0; see
[CHANGELOG.md](CHANGELOG.md). What follows is
what is left, re-tiered. Every item was checked against the code rather than
imagined, and nothing here is committed to.

## Tier 1 — more reach

### 1. More than one remote at a time

`--from ssh://host` watches one machine. The same mechanism pointed at several
would give a fleet overview — one row per host with its exposure, its off
-baseline count and its throughput — which is the natural next question after
watching one host live. The snapshot already carries everything such a row
needs.

Medium-to-large, and it needs a view that does not exist yet.

### 2. Per-interface 24-hour history

`trend.json` records the aggregate across all links. Keeping it per interface
would answer "was that the VPN or the LAN?" after a restart, which the current
panel cannot. The file would grow by roughly the number of real interfaces.

Small-to-medium.

### 3. Drill the other way

`F` follows a process or a listener into its connections. From a connection,
the useful direction is the opposite one: to the process, and to everything
else that process is doing. The drill machinery is already general enough.

Small.

## Tier 2 — polish

### 4. User-defined themes

Three built-in palettes, and the palette struct is already the only place
colours come from. A `themes` map in the config would let somebody match their
terminal rather than pick the nearest of three.

### 5. Rebind keys from the settings overlay

`keys` is the one setting the overlay cannot edit — it is still hand-edited
JSON — which leaves exactly the gap the overlay was built to close.

### 6. Mouse in the overlays

Rows are clickable in the tables and not in the help or settings overlays.

### 7. Search on the Routes tab

Every other scrollable view has `/`. A host with forty routes does not.

### 8. Event log export from the daemon

`e` exports events to CSV from the TUI; headless there is only the JSONL
stream. A `--dump-events` that renders the ring on exit would close it.

## Tier 3 — things deliberately not done

- **UDP throughput.** There is no per-socket accounting for UDP in the kernel,
  so it would need packet capture or eBPF — a different tool with different
  privileges.
- **Throughput on macOS and Windows.** `sock_diag` is Linux's; the columns hide
  themselves elsewhere rather than being faked.
- **Fetching the ASN table.** It is opt-in, read from disk, and no lookup
  leaves the host. A built-in downloader would quietly undo that.
