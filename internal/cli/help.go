package cli

func topHelp() object {
	return helpDocument(
		"facets [global flags] <command>",
		"View and update project tasks; run without a command to show open tasks",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{
			{"--project <id>", "discovered", "project ID"},
			{"--provider <name>", "kata", "task provider"},
			{"--format <toon|json>", "toon", "stdout format"},
			{"--json", "false", "alias for --format json"},
		}},
		primitiveArray{"facets", "facets --project thornwear tasks", "facets --json projects list", "facets serve --addr :8080"},
		primitiveArray{"tasks", "projects", "serve", "help"},
	)
}

func tasksHelp() object {
	return helpDocument(
		"facets tasks [list|show|create|edit|close|reopen|delete|daemon]",
		"List defaults to open tasks when no task command is supplied",
		table{columns: []string{"command", "required", "description"}, rows: [][]any{
			{"list", "", "list tasks"}, {"show", "<id>", "show one task"},
			{"create", "<title>", "create a task"}, {"edit", "<id> and an edit flag", "update task fields"},
			{"close", "<id>, --message, --evidence <type:value>", "close with completion context"},
			{"reopen", "<id>", "reopen a task"}, {"delete", "<id>, --confirm <id>", "delete without prompting"},
			{"daemon", "", "stream project and open-task snapshots as NDJSON"},
		}},
		primitiveArray{"facets tasks", "facets tasks show T-123", "facets tasks create \"Fix login\" --priority 2"}, nil,
	)
}

func tasksListHelp() object {
	return helpDocument("facets tasks [list] [flags]", "List tasks in the discovered project",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{
			{"--status <open|closed|all>", "open", "task status"},
			{"--fields <list>", "id,title,status", "subset of id,title,status,priority,assignee,updated"},
		}}, primitiveArray{"facets tasks", "facets tasks list --status all", "facets tasks --fields id,title,priority,updated"}, nil)
}

func taskDaemonHelp() object {
	return helpDocument(
		"facets tasks daemon [--interval <duration>]",
		"Stream newline-delimited JSON events for UI consumers; this command always writes JSON regardless of the global output format",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{
			{"--interval <duration>", "2s", "provider polling interval from 250ms through 5m"},
			{"snapshot event", "", `{"type":"snapshot","projects":[{"id","name","directory","tasks":[{"id","title","status","priority","assignee","updated_at"}]}]}`},
			{"error event", "", `{"type":"error","message":"...","retrying":true}; retain the last valid snapshot`},
		}},
		primitiveArray{"facets tasks daemon", "facets tasks daemon --interval 5s"}, nil,
	)
}

func taskShowHelp() object {
	return helpDocument("facets tasks show <id> [--full]", "Show one task; bodies over 1000 characters are truncated by default",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{{"<id>", "required", "task ID"}, {"--full", "false", "show the complete body"}}},
		primitiveArray{"facets tasks show T-123", "facets tasks show T-123 --full"}, nil)
}

func taskCreateHelp() object {
	return helpDocument("facets tasks create <title> [flags]", "Create a task in the discovered project",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{
			{"<title>", "required", "task title"}, {"--body <text>", "empty", "task body"},
			{"--priority <0..4>", "unset", "task priority"}, {"--assignee <name>", "empty", "assignee"},
			{"--idempotency-key <key>", "empty", "deduplicate retries"},
		}}, primitiveArray{"facets tasks create \"Fix login\"", "facets tasks create \"Fix login\" --body \"Handle expired sessions\" --priority 2", "facets tasks create \"Fix login\" --idempotency-key login-fix"}, nil)
}

func taskEditHelp() object {
	return helpDocument("facets tasks edit <id> [flags]", "Replace one or more task fields; at least one edit flag is required",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{
			{"<id>", "required", "task ID"}, {"--title <text>", "unchanged", "replacement title"},
			{"--body <text>", "unchanged", "replacement body; empty clears"},
			{"--priority <0..4|->", "unchanged", "replacement priority; - clears"},
			{"--assignee <name>", "unchanged", "replacement assignee; empty clears"},
		}}, primitiveArray{"facets tasks edit T-123 --title \"New title\"", "facets tasks edit T-123 --priority -", "facets tasks edit T-123 --body \"\" --assignee \"\""}, nil)
}

func taskCloseHelp() object {
	return helpDocument("facets tasks close <id> --message <text> --evidence <type:value> [--evidence <type:value>...]", "Close a task with required completion context",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{
			{"<id>", "required", "task ID"}, {"--message <text>", "required", "completion message"},
			{"--evidence <type:value>", "required", "repeatable typed evidence: test:<command>, commit:<sha>, or pr:<url>"},
		}}, primitiveArray{"facets tasks close T-123 --message \"Implemented\" --evidence \"test:go test ./...\"", "facets tasks close T-123 --message \"Shipped\" --evidence \"commit:<sha>\" --evidence \"pr:<url>\""}, nil)
}

func taskReopenHelp() object {
	return helpDocument("facets tasks reopen <id>", "Reopen a closed task",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{{"<id>", "required", "task ID"}}},
		primitiveArray{"facets tasks reopen T-123"}, nil)
}

func taskDeleteHelp() object {
	return helpDocument("facets tasks delete <id> --confirm <id>", "Delete a task without prompting; confirmation must exactly match",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{{"<id>", "required", "task ID"}, {"--confirm <id>", "required", "exact task ID"}}},
		primitiveArray{"facets tasks delete T-123 --confirm T-123"}, nil)
}

func projectsHelp() object {
	return helpDocument("facets projects <list|show|set>", "List projects, show one project, or set local project metadata",
		table{columns: []string{"command", "required", "description"}, rows: [][]any{{"list", "", "list projects"}, {"show", "<id>", "show one project"}, {"set", "<id> key=value", "set local project metadata"}}},
		primitiveArray{"facets projects list", "facets projects show thornwear", "facets projects set thornwear directory=/home/user/Projects/thornwear"}, nil)
}
func projectsListHelp() object {
	return helpDocument("facets projects list", "List projects available from the selected provider", table{columns: []string{"name", "default", "description"}}, primitiveArray{"facets projects list", "facets --json projects list"}, nil)
}
func projectSetHelp() object {
	return helpDocument("facets projects set <id> directory=<path>", "Set local metadata for a discovered project",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{{"<id>", "required", "provider project ID"}, {"directory=<path>", "required", "working directory used to scope activity"}}},
		primitiveArray{"facets projects set thornwear directory=/home/user/Projects/thornwear"}, nil)
}
func projectShowHelp() object {
	return helpDocument("facets projects show <id>", "Show one project with a 30-day task and activity summary",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{{"<id>", "required", "project ID"}, {"summary period", "30 days", "task and recent activity window"}}}, primitiveArray{"facets projects show thornwear"}, nil)
}
func serveHelp(address string) object {
	return helpDocument("facets serve [--addr <address>]", "Run the facets web server",
		table{columns: []string{"name", "default", "description"}, rows: [][]any{{"--addr <address>", address, "listen address; defaults to FACETS_ADDR or :8080"}}},
		primitiveArray{"facets serve", "facets serve --addr 127.0.0.1:8080"}, nil)
}

func helpDocument(usage, description string, options table, examples primitiveArray, commands primitiveArray) object {
	doc := object{{name: "help", value: object{{name: "usage", value: usage}, {name: "description", value: description}}}}
	if commands != nil {
		doc[0].value = append(doc[0].value.(object), field{name: "commands", value: commands})
	}
	doc[0].value = append(doc[0].value.(object), field{name: "options", value: options}, field{name: "examples", value: examples})
	return doc
}
