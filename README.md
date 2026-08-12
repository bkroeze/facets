# Facets

Facets is a personal dashboard project. The current project facet provides:

- a project-aware `facets` CLI for viewing projects and managing tasks;
- project and task data supplied by [Kata](https://github.com/kenn-io/kata);
- a Go, HTMX, and Alpine.js dashboard shell with health, status, and error handling.

## Requirements

- Go 1.26 or newer
- The `kata` executable on `PATH`, configured with the projects you want Facets to use and supporting JSON API version 1 (`kata_api_version: 1`)

Kata is currently the only bundled provider.

## Install

From this checkout:

```sh
go install ./cmd/facets
```

Ensure Go's binary directory (normally `$(go env GOPATH)/bin`) is on `PATH`, then check the installation:

```sh
facets help
```

For development, run the command without installing it:

```sh
go run ./cmd/facets help
```

## Select a project

Task commands resolve their project in this order:

1. the global `--project <id>` flag;
2. `FACETS_PROJECT`;
3. the nearest `.kata.toml` found in the current directory or a parent;
4. the most specific configured project `directory` containing the current directory;
5. the directory name of the nearest Jujutsu workspace (`.jj`).

A Kata workspace config names its project like this:

```toml
version = 1

[project]
name = "facets"
```

Run `facets` without a command to list open tasks for the resolved project:

```sh
facets
facets --project facets
FACETS_PROJECT=facets facets
```

Global flags must appear before the command:

| Flag | Default | Purpose |
| --- | --- | --- |
| `--project <id>` | discovered | Select a project explicitly. |
| `--provider <name>` | `kata` | Select a task provider. |
| `--format <toon\|json>` | `toon` | Set the stdout format. |
| `--json` | false | Alias for `--format json`. |

## Task commands

List tasks. Open tasks are shown by default:

```sh
facets tasks
facets tasks list --status all
facets tasks --status closed --fields id,title,priority,updated
```

`--status` accepts `open`, `closed`, or `all`. `--fields` accepts a comma-separated subset of `id`, `title`, `status`, `priority`, `assignee`, and `updated`.

Show a task. Bodies longer than 1,000 characters are truncated unless `--full` is set:

```sh
facets tasks show T-123
facets tasks show T-123 --full
```

Create and edit tasks:

```sh
facets tasks create "Fix login" --body "Handle expired sessions" --priority 2
facets tasks create "Fix login" --idempotency-key login-fix
facets tasks edit T-123 --title "Clarify login error"
facets tasks edit T-123 --priority -
facets tasks edit T-123 --body "" --assignee ""
```

Priorities range from `0` to `4`; `-` clears an existing priority. Empty `--body` and `--assignee` values clear those fields.

Close a task with a completion message and at least one typed evidence value. An optional comment can be appended in the same non-interactive command:

```sh
facets tasks close T-123 \
  --message "Implemented and verified" \
  --evidence "test:go test ./..." \
  --evidence "commit:<sha>" \
  --comment "Released through the desktop flow"
facets tasks comment T-123 --body "Follow-up implementation context"
facets tasks reopen T-123
```

Append further context with `tasks comment`, or reopen the task with `tasks reopen`. All mutations are non-interactive.

Delete without an interactive prompt by repeating the exact task ID as confirmation:

```sh
facets tasks delete T-123 --confirm T-123
```

Every task subcommand supports `--help`, for example:

```sh
facets tasks create --help
facets tasks close --help
facets tasks comment --help
```

### Task snapshot daemon

`facets tasks daemon` is a foreground process for UI consumers. It writes one
newline-delimited JSON event per line regardless of the global output format:

```sh
facets tasks daemon
facets tasks daemon --interval 5s
```

A `snapshot` event replaces the prior project tree. An `error` event is
recoverable; consumers should retain the last valid snapshot while the daemon
retries. Runtime diagnostics go to stderr, never into the stdout protocol. Run
`facets tasks daemon --help` for the complete event fields and polling limits.


## Project commands

```sh
facets projects list
facets projects show facets
facets projects set facets directory=/home/user/Projects/facets
facets --json projects list
```

`projects list` pulls projects from the selected provider and records them in
the local registry. The registry stores provider source, first-seen and
last-seen timestamps, provider metadata, and local settings. Set `directory`
for each project so activity summaries can scope OMP sessions:

```sh
facets projects set thornwear directory=/home/user/Projects/thornwear
```

The registry uses `~/.local/share/facets/facets.db` by default. Set `FACETS_DB`
to use another SQLite database. Projects without a configured directory show
`??` for their OMP session total rather than attributing activity to the
wrong project.

The current CLI exposes project listing, inspection, and local metadata
configuration. Task mutations are delegated to the selected provider; Kata
task deletion remains recoverable according to Kata's archive semantics.

## Quickshell widget on Omarchy and Wayland

The app in [`quickshell/facets/shell.qml`](quickshell/facets/shell.qml) is a
single-screen Quickshell panel. It shows active Facets projects, expands each
project into its open Kata tasks, and opens a floating Kata TUI in the selected
project directory.

### Prerequisites

Install these before enabling the widget:

- Quickshell 0.3 or newer;
- Facets and Kata, with Kata JSON API version 1 configured;
- Omarchy with Hyprland and UWSM;
- `xdg-terminal-exec`, normally supplied by the Omarchy desktop;
- `facets`, `kata`, `quickshell`, `uwsm-app`, and `xdg-terminal-exec` on the
  graphical session's `PATH`.

Check the interactive environment first:

```sh
quickshell --version
command -v facets kata quickshell uwsm-app xdg-terminal-exec
facets tasks daemon --interval 5s
```

The final command should immediately print one JSON `snapshot` line and remain
running until interrupted. If it reports a provider error, fix Kata before
starting Quickshell.

Hyprland/UWSM does not necessarily inherit an interactive shell's startup
files. Inspect the imported session path with:

```sh
systemctl --user show-environment | grep '^PATH='
```

Install Facets into a directory on that path. For a Go installation this is
usually `$(go env GOPATH)/bin`; add that directory to the UWSM session
environment if it is absent, then log out and back in. Do not rely on an alias
or shell function: Quickshell launches argv directly.

### Install or update the app

From the Facets checkout:

```sh
go install ./cmd/facets
just install-gui
```

`install-gui` respects `XDG_CONFIG_HOME` and `XDG_DATA_HOME`, with
`$HOME/.config` and `$HOME/.local/share` fallbacks. It installs:

```text
$XDG_CONFIG_HOME/quickshell/facets/shell.qml
$XDG_DATA_HOME/facets/facets.svg
$XDG_DATA_HOME/facets/facets-waybar.svg
```

The target owns only those app files; it does not edit Hyprland or Waybar.
The current widget generates no cache files. Facets keeps its registry at
`$HOME/.local/share/facets/facets.db` by default. To place it under a custom
`XDG_DATA_HOME`, export
`FACETS_DB="${XDG_DATA_HOME:-$HOME/.local/share}/facets/facets.db"` in the
graphical session.

Sync projects and configure every launchable working directory:

```sh
facets projects list
facets projects set facets directory="$HOME/Projects/facets"
facets projects set thornwear directory="$HOME/Projects/thornwear"
```

The panel deliberately disables the open control for a project whose
`directory` is missing. It never falls back to the panel's own working
directory.

Run the installed config in the foreground before adding autostart:

```sh
quickshell --config facets
```

The panel starts hidden. In another terminal, exercise its public IPC methods:

```sh
quickshell ipc --config facets call facets toggle
quickshell ipc --config facets call facets -- show
quickshell ipc --config facets call facets -- hide
```

The `--` separator keeps `show` and `hide` from being interpreted as
Quickshell IPC subcommands on versions where those names are reserved.

### Start with Omarchy

Manually merge this line into `~/.config/hypr/autostart.conf`:

```ini
exec-once = uwsm-app -- quickshell --no-duplicate --config facets
```

Do not replace the file and do not edit anything under
`~/.local/share/omarchy/`; that tree is managed by Omarchy updates.

Optionally merge a toggle binding into `~/.config/hypr/bindings.conf` after
checking that the key is unused:

```ini
bindd = SUPER SHIFT, F, Facets project tasks, exec, quickshell ipc --config facets call facets toggle
```

Apply and validate only the Hyprland changes:

```sh
hyprctl reload
hyprctl configerrors
```

`hyprctl configerrors` must print nothing.

### Floating Kata terminals

The open control resolves absolute paths for `uwsm-app`,
`xdg-terminal-exec`, and `kata`, then launches:

```text
uwsm-app -a facets-kata -d "Facets Kata TUI - <project>" -- \
  xdg-terminal-exec --app-id=TUI.float \
  --title="Facets Kata - <project>" --dir=<configured-directory> -- \
  kata tui
```

Arguments remain separate argv values; project names and paths are never
interpolated into a shell command. Omarchy's stock Hyprland rules float,
center, and size only terminal windows carrying the `TUI.float` app ID.
Ordinary terminal windows retain their normal tiled behavior. Confirm the
installed behavior with:

```sh
hyprctl clients
hyprctl configerrors
```

Look for class `TUI.float`, title `Facets Kata - <project>`, and
`floating: 1`. Hyprland window-rule syntax changes between releases; verify
custom rules against the documentation matching `hyprctl version`. For
Hyprland 0.56, use the
[0.56 window-rule reference](https://wiki.hypr.land/0.56.0/Configuring/Basics/Window-Rules/).
Do not add a rule that floats the normal Ghostty, Alacritty, Foot, or Kitty
class.

### Waybar launcher

`just install-gui` preserves the specified source SVG as `facets.svg` and
installs a white Waybar rendering at
`${XDG_DATA_HOME:-$HOME/.local/share}/facets/facets-waybar.svg`. To reproduce
the Waybar launcher, manually add `image#facets` to one of the module lists in
`~/.config/waybar/config.jsonc`, then merge this module definition:

```jsonc
"image#facets": {
  "exec": "printf '%s\\n' \"${XDG_DATA_HOME:-$HOME/.local/share}/facets/facets-waybar.svg\"",
  "interval": 300,
  "size": 14,
  "tooltip": true,
  "on-click": "quickshell ipc --config facets call facets toggle"
}
```

The same click command opens the Facets panel when hidden and closes it when
visible. It depends on the Quickshell autostart entry above.

Merge this styling into `~/.config/waybar/style.css`:

```css
#image-facets {
  margin: 0 14px 0 8px;
}
```

Reload Waybar without resetting its configuration:

```sh
omarchy restart waybar
```

### Troubleshooting

- Run `facets tasks daemon --interval 5s` directly. Stdout must contain only
  JSON events; provider and registry diagnostics appear on stderr.
- Run `quickshell --path quickshell/facets` from the checkout to keep QML,
  process, and parser errors in the foreground.
- Run `quickshell ipc --config facets show` to list the `facets` target and
  its methods.
- If the panel says the daemon disconnected, verify the graphical `PATH`, then
  check that `facets` and `kata` resolve there. The panel retains its last valid
  project tree while retrying.
- If the open control is a muted dash, configure that project's `directory`
  with `facets projects set`.
- If a terminal launch fails, verify `uwsm-app`, `xdg-terminal-exec`, and
  `kata` are executable from the graphical session.
- After Hyprland edits, run `hyprctl reload` followed by
  `hyprctl configerrors`. After Waybar edits, run `omarchy restart waybar`.

### Upgrade or uninstall

To upgrade, pull the new checkout, rerun `go install ./cmd/facets`, and rerun
`just install-gui`. Quickshell reloads an active config after the QML file
changes; restart it if the process does not reload cleanly.

To uninstall the widget:

```sh
quickshell kill --config facets
config_home="${XDG_CONFIG_HOME:-$HOME/.config}"
data_home="${XDG_DATA_HOME:-$HOME/.local/share}"
rm -rf "$config_home/quickshell/facets"
rm -f "$data_home/facets/facets.svg" "$data_home/facets/facets-waybar.svg"
```

Also remove only the Facets lines you manually added to Hyprland and Waybar,
then run `hyprctl reload` and, if applicable, `omarchy restart waybar`. The
uninstall intentionally leaves the Facets binary, Kata data, Facets registry,
and any user data or cache in place.

## Web dashboard shell

Start the current dashboard shell on the default `:8080` address:

```sh
facets serve
```

Choose another listen address with a flag or environment variable:

```sh
facets serve --addr 127.0.0.1:8080
FACETS_ADDR=127.0.0.1:8080 facets serve
```

The server logs structured request and error records to stderr. It shuts down gracefully on `SIGINT` or `SIGTERM`.

The versioned JSON API root is available at `GET /api/v1`. It returns the
selected contract version and uses a stable JSON error envelope for every path
under `/api/v1`. See [`docs/api-v1.md`](docs/api-v1.md) for resource shapes,
lifecycle requests, ordering, nullability, and error codes.

## Output and errors

Command results are written to stdout as [TOON](https://toonformat.dev/) by default. Use `--json` when another program needs JSON:

```sh
facets --json tasks --status all
facets --format json projects show facets
```

Usage and operational failures also produce structured documents on stdout. Exit codes are:

| Code | Meaning |
| --- | --- |
| `0` | Success |
| `1` | Operational failure |
| `2` | Invalid command usage |

Raw provider diagnostics are not exposed by default. Set `FACETS_DEBUG=true` to write diagnostic details to stderr while keeping stdout machine-readable.

## Development

```sh
go test ./...
go vet ./...
go run ./cmd/facets serve --addr 127.0.0.1:8080
```
