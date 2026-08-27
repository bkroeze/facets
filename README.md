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
| `--format <human\|json\|toon>` | `toon` | Set the stdout format. `human` renders clean terminal text with ANSI colors. |
| `--json` | false | Alias for `--format json`. |

## Task commands

List tasks. Open tasks are shown by default:

```sh
facets tasks
facets tasks list --status all
facets tasks list --all-projects
facets tasks list --all
facets tasks list --status all --all-projects --fields id,title,priority,updated
facets tasks --status closed --fields id,title,priority,updated
```

`--status` accepts `open`, `closed`, or `all`. `--fields` accepts a
comma-separated subset of `id`, `title`, `status`, `priority`, `assignee`, and
`updated`. `--all-projects` skips project discovery and lists tasks from every
enabled provider project. `--all` implies `--all-projects` and includes
disabled projects too. Both flags are valid together. Cross-project task IDs
use the `project#task` form, such as `alpha#T-123`. Existing status and field
filters still apply.

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
newline-delimited JSON event per line by default. Pass the global `--format`
flag before the command to select human or TOON output instead.

```sh
facets tasks daemon
facets tasks daemon --interval 5s --refresh-timeout 30s
```

Only one task daemon runs per user. Each provider refresh is canceled after 10
seconds by default, and the next refresh waits for the configured interval
after the prior one finishes. The daemon periodically repeats its latest event
so a detached stdout consumer is detected and the singleton lock is released.
A state-free `heartbeat` event may appear before the first provider result. A
`snapshot` event replaces the prior project tree. An `error` event is
recoverable; consumers should retain the last valid snapshot while the daemon
retries. Runtime diagnostics go to stderr, never into the stdout protocol. Run
`facets tasks daemon --help` for the complete event fields and timing limits.


## Daily focus and today summary

Save today's focus without prompting:

```sh
facets focus "Plan the day"
```

`facets focus` requires one non-empty argument and stores the focus with the
current local-day boundary. `facets focus --help` shows the command contract.

Read the current focus without querying the task provider:

```sh
facets --format json focus show
```

This returns `{"focus":null}` when no focus is set for the current local day.

Show the current focus, open `facets.top=true` tasks, and completion counts:

```sh
facets today
facets today --format json
```

When no focus exists, an interactive terminal prompts for it and persists the
answer. Non-interactive execution returns a usage error; set the focus first
with `facets focus "<text>"`. The output includes project and task identities,
titles, and counts for all tasks and top tasks completed during the local day.

## Project commands

```sh
facets projects list
facets projects list --all
facets projects show facets
facets projects set facets directory=/home/user/Projects/facets
facets projects disable thornwear
facets projects enable thornwear
facets --json projects list --all
```

`projects list` pulls projects from the selected provider and records them in
the local registry. Disabled projects are hidden from the default list and
from the Quickshell task snapshot. Use `projects list --all` to include them
at the end, with their names in parentheses. The registry stores provider
source, first-seen and last-seen timestamps, provider metadata, local settings,
and a disable timestamp without deleting historical data. Set `directory` for
each project so activity summaries can scope OMP sessions:

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

Facets has a shared Quickshell panel in
[`quickshell/facets/FacetsPanel.qml`](quickshell/facets/FacetsPanel.qml):
It shows active Facets projects, expands each project into its open Kata tasks,
and opens a floating Kata TUI in the selected project directory.
Each open task has a star indicator: `★` when selected and `☆` when not.
Toggling it persists the provider metadata `facets.top` as the string
`"true"` or `"false"`, which controls whether the task appears in today's
top-task list.

- Omarchy Quattro loads it as a first-class panel plugin inside the existing
  `omarchy-shell` process.
- Older Omarchy installations can still use the standalone
  [`quickshell/facets/shell.qml`](quickshell/facets/shell.qml) wrapper.

Do not start a second Quickshell process for the Quattro integration. Quattro
hosts panels, bars, notifications, and OSDs in one long-running shell.

### Prerequisites

Install these before enabling the widget:

- Quickshell 0.3 or newer;
- Facets and Kata, with Kata JSON API version 1 configured;
- Omarchy Quattro with Hyprland and UWSM;
- `xdg-terminal-exec`, normally supplied by Omarchy;
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

### Install on Omarchy Quattro

From the Facets checkout:

```sh
just install-quattro
omarchy-shell shell rescanPlugins
omarchy plugin enable facets --section center
omarchy-restart-shell
```

`install-quattro` installs the `facets` binary to `~/.local/bin/facets` and
copies the plugin manifest, panel, and bar widget to
`~/.config/omarchy/plugins/facets`. The manifest declares the bar widget's
default section as `center`; `--section center` makes that placement explicit.
`omarchy plugin enable facets --section center` records the widget in
`~/.config/omarchy/shell.json`; Quattro then loads it in the existing shell
process.

If an older installation has `facets` only in the top-level `plugins` list,
disable it once before enabling it with the center placement:

```sh
omarchy plugin disable facets
omarchy plugin enable facets --section center
```

The center widget uses
`$XDG_DATA_HOME/facets/facets.svg` (falling back to
`$HOME/.local/share/facets/facets.svg`) and toggles the Facets panel when
clicked. When today's focus is set with `facets focus "<text>"`, the widget
shows that focus beside the icon, truncated to 40 characters with an ellipsis.
It refreshes the focus title every 30 seconds.

Use Quattro's shell IPC as the launcher:

```sh
omarchy-shell shell toggle facets
omarchy-shell shell summon facets
omarchy-shell shell hide facets
```

The `toggle` call opens the Facets panel when hidden and closes it when shown.
The shell IPC command is required for Quattro; do not add a second
`quickshell --config` autostart entry.

To update the Quattro plugin, pull the new checkout, rerun
`go install ./cmd/facets` and `just install-quattro`, then run
`omarchy-shell shell rescanPlugins` and `omarchy plugin enable facets --section center`
followed by `omarchy-restart-shell`.

To uninstall a checkout-installed plugin:

```sh
omarchy plugin disable facets
rm -rf "$HOME/.config/omarchy/plugins/facets"
omarchy-shell shell rescanPlugins
```

If the plugin was installed with `omarchy plugin add`, use
`omarchy plugin remove facets` instead of removing the directory manually.

### Install or update the legacy standalone app

Use this mode only on Omarchy versions without the Quattro shell plugin host.
From the Facets checkout:

```sh
go install ./cmd/facets
just install-gui
```

`install-gui` respects `XDG_CONFIG_HOME` and `XDG_DATA_HOME`, with
`$HOME/.config` and `$HOME/.local/share` fallbacks. It installs:

```text
$XDG_CONFIG_HOME/quickshell/facets/shell.qml
$XDG_CONFIG_HOME/quickshell/facets/FacetsPanel.qml
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

- Stop the hosted Quickshell process before running `facets tasks daemon
  --interval 5s` directly, or inspect that process's stderr logs instead. Only
  one daemon may run per user. By default, stdout contains only JSON events;
  provider and registry diagnostics appear on stderr.
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

For the reproducible loopback-only deployment with Tailscale Serve HTTPS and
MagicDNS, see [`docs/tailnet.md`](docs/tailnet.md). The deployment keeps local
`facets serve` usage available; it only adds an optional user service.

## Output and errors

Command results are written to stdout as [TOON](https://toonformat.dev/) by default. Use `--format human` for terminal-oriented text with ANSI colors, or `--format json` when another program needs JSON:

```sh
facets --format human tasks --status all
facets --format json projects show facets
facets --format toon tasks --status all
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

## Android app

The native Android app lives under [`android/`](android/). It is intentionally
separate from the Go CLI, web dashboard, and Quickshell widget.

### Prerequisites

- Android SDK Platform 36 and build tools installed through Android Studio or
  the Android SDK manager;
- JDK 17;
- an emulator or physical device for connected tests and release smoke tests;
- the device enrolled in the same Tailnet as the Facets workstation.

The app compiles and targets API 36 and supports Android API 26 and newer. No
machine-local SDK path, signing key, Tailnet secret, private hostname, or test
data belongs in this repository. The text-only `android/gradlew` launcher uses
an installed Gradle when available, or downloads the pinned distribution when
it is missing.

### Build and install

Run the deterministic debug build, lint, and JVM tests from the checkout:

```sh
cd android
./gradlew :app:assembleDebug :app:lintDebug test
```

Build the non-minified release artifact separately:

```sh
cd android
./gradlew :app:assembleRelease
```

With an enrolled device or emulator attached, install either artifact:

```sh
adb install -r app/build/outputs/apk/debug/app-debug.apk
# or:
adb install -r app/build/outputs/apk/release/app-release.apk
```

On first launch, open **Settings**, enter the HTTPS origin printed by
`tailscale serve status` (for example,
`https://<machine-name>.<tailnet-name>.ts.net`), and save it. Enter only the
origin: the app owns `/healthz` and `/api/v1`; do not add a path, credentials,
query, or fragment. A server that is unreachable, malformed, or on an
unsupported API version is shown as an actionable settings/sync error; it
must not be replaced with an empty project list.

### Widget setup

Add **Facets** from the launcher's widget picker, resize it as needed, and
choose **all projects** or a comma-separated bounded project-ID subset in its
configuration screen. Each widget instance stores its selection independently.
The widget renders the Room snapshot without network I/O, shows cached
projects and counts immediately, and offers a refresh action that keeps one
deduplicated WorkManager sync. Tap a project row to open that exact project.
Removing a widget removes only that instance's selection.

### Release smoke checklist

Run this checklist after installing from a clean app data state against a
reachable Tailnet server:

1. Configure the HTTPS origin; verify projects and active-task counts load.
2. Open a project, create a disposable task, edit it, close it, reopen it, and
   delete it. Confirm each state in the app and again after background refresh.
3. Place and configure the widget; tap refresh and a project row after a
   cold-start. Confirm the count and destination match the app.
4. Enable airplane mode; confirm the last Room snapshot remains visible.
   Restore connectivity, trigger refresh, and confirm the snapshot recovers
   without losing task state.
5. Stop the server or point the app at an incompatible API; confirm the UI
   reports the connection/API problem with a retry path rather than crashing
   or silently showing no data.

The same JVM checks are available through `just android-check`. Run connected
Compose/instrumentation checks on an attached device:

```sh
cd android
./gradlew :app:connectedDebugAndroidTest
```

The repository's Android workflow runs the debug build, lint, and JVM tests on
every Android change. A manually dispatched workflow provisions an API 35
emulator for connected tests. The release smoke checklist is intentionally
device/Tailnet-dependent and is not substituted with mocks.

### Troubleshooting, update, and uninstall

- `Unable to connect` or an API error: check Tailnet enrollment, `tailscale
  serve status`, `systemctl --user status facets.service`, and the HTTPS
  `/healthz` endpoint from the device.
- Empty or stale data: keep the app configured with the origin, restore network
  access, and use widget/app refresh; cached data remains available offline.
- A widget does not update: remove and re-add it after confirming WorkManager
  has network access; its configuration is per widget instance.
- Update without clearing user data: install the new APK with `adb install -r`
  or use the normal package updater.
- Remove the app and its local cache: `adb uninstall dev.barnlab.facets`.
  Remove the workstation service and Tailnet mapping separately with
  `just tailnet-uninstall`; see [`docs/tailnet.md`](docs/tailnet.md).
