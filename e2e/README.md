# e2e

Everything this tool reports comes from a live host: `/proc`, the kernel's
`sock_diag` and the rtnetlink neighbour cache. None of it can be checked
against a mock, because a mock would be a copy of the parser's own
assumptions — which is exactly where the bugs are.

So the suite runs **inside a container, and the container is the host under
test**. Its network namespace is its own, so the interfaces, routes, listeners
and connections are whatever the suite puts there: it opens known sockets,
moves a known number of bytes across them, and then asserts that the program
reports them.

```bash
make e2e                     # build the image and run everything
./e2e/run.sh -run TestDaemon -v
```

Nothing on the host machine is read or written: no volumes, no ports, no host
paths, and the external-IP fetch is pointed at a closed port so nothing leaves
the container. The one thing it needs is a working Docker.

## What it covers that the unit tests cannot

| Area | Why only here |
|------|---------------|
| `probe.SockStats` | Per-socket byte counters and RTT come from a netlink dump of a real kernel. The fixture writes ~90 kB and the counters have to show it |
| `probe.Collect` | Attributing a socket to a PID means reading `/proc/<pid>/fd` for every process |
| Other network namespaces | The suite re-executes itself into a fresh network namespace (`CLONE_NEWNET`) and the snapshot taken from outside has to list its listener and connection — labelled, classed `netns` rather than exposed, with byte counters read by `sock_diag` after `setns`. `run.sh` grants the container `CAP_SYS_ADMIN` and `CAP_NET_ADMIN` for this; without them the test skips |
| `probe.Routes` / `Neighbours` / `Resolvers` | Parsers for files only a real host has |
| Every non-interactive mode | `--json`, `--once`, `--metrics`, `--top`, `--diff`, `--check-baseline`, `--save-baseline`, `--check-config`, `--usage-all`, `--from` (a snapshot file; `ssh://` is not exercised), `--version` — each is a process that has to start, do its job and exit with the right code |
| `--daemon` | Polling, event detection, the alert rules and their recovery, the hook, `SIGHUP` reload and a clean `SIGTERM` only happen in a long-running process |
| `--metrics-listen` | The exporter is answered from the live session's own state, over HTTP |
| The TUI | Bubble Tea takes the terminal into raw mode and reads keys from it, so the suite drives it on a real pseudo-terminal (`pty_linux.go`, a dozen ioctls rather than a dependency) |
| Persistence | A view saved by pressing keys in the TUI has to be in the config file afterwards, and `--view` has to open into it |

## Layout

```
Dockerfile       the image: builds the binary, then runs this suite inside it
run.sh           build + run, with no host state involved
harness_test.go  the fixture (known sockets, known traffic) and the runner
util_test.go     assertions shared across the suite, incl. a Prometheus parser
cli_test.go      the non-interactive modes
daemon_test.go   the daemon, the exporter, the event log and the alert hook
tui_test.go      the TUI, driven on a pty
netns_test.go    sockets in another network namespace (a container, minus the runtime)
pty_linux.go     the pseudo-terminal itself
```

Everything is behind the `e2e` build tag, so `go test ./...` does not pick it
up. Run outside the container it skips rather than testing whatever binary
happens to be installed.
