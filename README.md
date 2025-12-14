# ducknetview 🦆

Minimalistic **network monitoring TUI** written in **Go**.

Built with:
- **Bubble Tea** + **bubbles** (UI framework)
- **tcell** (terminal backend)
- **gopsutil** (network / process info)

The focus is a clean, usable terminal UI even on hosts with many interfaces (Docker, bridges, veth).

---

## Features

- **Overview**
    - Hostname, uptime, timestamp
    - Selected interface summary
    - RX/TX rate with mini charts

- **Interfaces tab**
    - Scrollable interface list
    - Auto-updating details (no Enter required)
    - Per-interface RX/TX charts

- **Ports tab**
    - Open listening TCP / UDP ports
    - PID and process name (best-effort)
    - Scrollable list
    - Search (`/`) by port, address, protocol or process

- **Processes tab**
    - Processes ranked by network connections
    - Scrollable list
    - Search (`/`) by process name or PID

---

## Key bindings

### Global

| Key | Action |
|-----|--------|
| `←` `→` | Switch tabs |
| `tab` / `shift+tab` | Cycle tabs |
| `q` | Quit |

### Lists / Viewports

| Key | Action |
|-----|--------|
| `↑ ↓` | Scroll |
| `PgUp / PgDn` | Page scroll |
| `Home / End` | Jump |

### Search (Ports / Processes)

| Key | Action |
|-----|--------|
| `/` | Start search |
| `Enter` | Apply search |
| `Esc` | Exit search |
| `Ctrl+u` | Clear query (while searching) |

---

## Build & run

```bash
go mod tidy
go run .
```

Build binary:

```bash
go build -o ducknetview
./ducknetview
```

---

## Notes

- Process ↔ port mapping may require elevated privileges depending on OS.
- Per-process bandwidth is **not** implemented (would require eBPF / netlink accounting).
- Primarily tested on Linux.

---

## License

MIT
