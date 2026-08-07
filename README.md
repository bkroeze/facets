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
4. the directory name of the nearest Jujutsu workspace (`.jj`).

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

Close a task with a completion message and at least one typed evidence value, or reopen it:

```sh
facets tasks close T-123 \
  --message "Implemented and verified" \
  --evidence "test:go test ./..." \
  --evidence "commit:<sha>"
facets tasks reopen T-123
```

Delete without an interactive prompt by repeating the exact task ID as confirmation:

```sh
facets tasks delete T-123 --confirm T-123
```

Every task subcommand supports `--help`, for example:

```sh
facets tasks create --help
facets tasks close --help
```

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
