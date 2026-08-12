# Facets REST/JSON API v1

This document is the stable wire contract for non-CLI Facets clients. The API is provider-neutral: responses contain normalized Facets resources and never provider-native Kata payloads or workstation-local project metadata.

The API root and error behavior are implemented by card `107e`. Project summaries, saved views, and task resource handlers are implemented by the dependent cards `qaat`, `pgcs`, and `qxg9` against this contract.

## Protocol

- Base path: `/api/v1`
- Response media type: `application/json; charset=utf-8`
- Clients SHOULD send `Accept: application/json`. An incompatible `Accept` value returns `406 not_acceptable`.
- Requests with a JSON body MUST send `Content-Type: application/json`; parameters other than those documented here are rejected.
- `GET /api/v1` returns `{"version":"v1"}`. The response and all error responses set `Cache-Control: no-store` where they can contain mutable user data.
- Every response includes `X-Request-ID`. Error bodies repeat that identifier as `error.request_id`.
- Resource identifiers are opaque, case-sensitive UTF-8 strings. Clients MUST percent-encode identifiers when placing them in paths and MUST NOT parse or synthesize them.
- Timestamps are RFC 3339 strings in UTC with optional fractional seconds. An unavailable timestamp is JSON `null`; it is never an empty string.
- Optional scalar values use explicit `null`. Collection fields are always arrays, including when empty.
- Unknown enum values must be treated as an incompatible contract rather than silently mapped to a known value.

## Ordering

Collections have deterministic default ordering:

- projects: `name` case-insensitively ascending, then `id` ascending;
- saved views: built-in views first, then `name` case-insensitively ascending, then `id` ascending;
- tasks without a saved-view order: `updated_at` descending, then `id` ascending. A null `updated_at` sorts after all known timestamps.

A saved view can replace the task ordering with one of the documented task fields and `asc` or `desc`; `id` ascending remains the final tie-breaker.

## Resources

### Project

```json
{
  "id": "facets",
  "name": "Facets",
  "description": "Personal dashboard",
  "active_task_count": 3,
  "created_at": "2026-08-12T12:00:00Z",
  "updated_at": null
}
```

Provider metadata, local project directories, and activity-database paths are never present.

| Method | Path | Success |
| --- | --- | --- |
| `GET` | `/api/v1/projects` | `200 {"projects":[Project...]}` |
| `GET` | `/api/v1/projects/{project_id}` | `200 Project` |

### Task

`status` is `open` or `closed`. `priority` is an integer from 0 through 4 or `null`. Empty `description` and `assignee` values are represented as empty strings.

```json
{
  "id": "ab12",
  "project_id": "facets",
  "title": "Add Android API",
  "description": "Define the v1 wire contract",
  "status": "open",
  "priority": 2,
  "assignee": "bruce",
  "created_at": "2026-08-12T12:00:00Z",
  "updated_at": "2026-08-12T12:30:00.123Z"
}
```

| Method | Path | Request | Success |
| --- | --- | --- | --- |
| `GET` | `/api/v1/projects/{project_id}/tasks` | Query: `view={view_id}` or `status=open\|closed\|all`; the two selectors are mutually exclusive | `200 {"tasks":[Task...]}` |
| `GET` | `/api/v1/projects/{project_id}/tasks/{task_id}` | — | `200 Task` |
| `POST` | `/api/v1/projects/{project_id}/tasks` | Create task | `201 Task` |
| `PATCH` | `/api/v1/projects/{project_id}/tasks/{task_id}` | Editable fields | `200 Task` |
| `POST` | `/api/v1/projects/{project_id}/tasks/{task_id}/comments` | Comment | `200 Task` |
| `POST` | `/api/v1/projects/{project_id}/tasks/{task_id}/close` | Completion audit context | `200 Task` |
| `POST` | `/api/v1/projects/{project_id}/tasks/{task_id}/reopen` | No body | `200 Task` |
| `DELETE` | `/api/v1/projects/{project_id}/tasks/{task_id}` | Explicit confirmation | `204` with no body |

Create request:

```json
{
  "title": "Add Android API",
  "description": "Define the v1 wire contract",
  "priority": 2,
  "assignee": "bruce",
  "idempotency_key": "android-api-card-107e"
}
```

`title` is required and non-empty. `idempotency_key` is optional, but clients SHOULD generate a stable key before retrying a create request. Repeating the same key for the same project must not create another task.

Patch request fields are optional. At least one field must be present. `title` cannot be null or empty; `description` and `assignee` may be empty strings; `priority: null` clears priority. Lifecycle fields are not accepted by PATCH.

```json
{
  "title": "Define Android API",
  "priority": null
}
```

Comment request:

```json
{"body":"The Android DTOs now match the server contract."}
```

Close request. `message` and at least one non-empty typed evidence value are
required; `comment` is optional. Evidence uses `type:value` format: the type
contains no whitespace and both components are non-empty.

```json
{
  "message": "Implemented and verified the v1 contract",
  "evidence": ["test:go test ./internal/web ./internal/project"],
  "comment": "Ready for Android client integration"
}
```

Delete request. `confirm` must exactly equal the opaque task ID from the path. Delete is never available through `GET`.

```json
{"confirm":"ab12"}
```

### Saved view and task query

The non-deletable built-in view has ID `active`, selects `open` tasks, and defaults to newest-first ordering.

```json
{
  "id": "active",
  "name": "Active",
  "builtin": true,
  "query": {
    "statuses": ["open"],
    "assignees": [],
    "priorities": []
  },
  "order": {
    "field": "updated_at",
    "direction": "desc"
  },
  "created_at": null,
  "updated_at": null
}
```

Within `query`, an empty array means no restriction for that field. Priorities are integers from 0 through 4. Supported order fields are `title`, `status`, `priority`, `assignee`, `created_at`, and `updated_at`; directions are `asc` and `desc`.

| Method | Path | Success |
| --- | --- | --- |
| `GET` | `/api/v1/views` | `200 {"views":[SavedView...]}` |
| `POST` | `/api/v1/views` | `201 SavedView` |
| `GET` | `/api/v1/views/{view_id}` | `200 SavedView` |
| `PATCH` | `/api/v1/views/{view_id}` | `200 SavedView` |
| `DELETE` | `/api/v1/views/{view_id}` | `204` with no body |
| `GET` | `/api/v1/projects/{project_id}/views/{view_id}/tasks` | `200 {"tasks":[Task...]}` |

The built-in `active` view cannot be updated or deleted. Unsupported provider predicates return `501 unsupported_operation`; they are not silently ignored.

## Errors

Every non-success response is a JSON object with one stable machine-readable
code and a safe human-readable message. Responses never include provider
stderr, command lines, task bodies, completion evidence, local paths, or
internal error strings.

```json
{
  "error": {
    "code": "validation_failed",
    "message": "The request contains invalid values.",
    "request_id": "42",
    "details": {
      "fields": {
        "title": "must not be empty"
      }
    }
  }
}
```

`details` is optional and clients must not require it. When present, validation errors use `details.fields` keyed by request field name.

| HTTP | Code | Meaning |
| --- | --- | --- |
| `400` | `invalid_request` | Malformed path, query, or JSON document |
| `404` | `not_found` | Project, task, view, or route does not exist |
| `405` | `method_not_allowed` | Route exists but does not support the method; `Allow` is set |
| `406` | `not_acceptable` | Client does not accept JSON |
| `409` | `conflict` | Request conflicts with existing state or idempotency history |
| `415` | `unsupported_media_type` | JSON request did not use `application/json` |
| `422` | `validation_failed` | JSON shape is valid but one or more values are invalid |
| `408` | `request_canceled` | Request context was canceled before completion |
| `501` | `unsupported_operation` | Configured provider cannot perform the normalized operation |
| `502` | `provider_failure` | Provider failed without a more specific public classification |
| `503` | `provider_unavailable` | Configured provider is unavailable |
| `504` | `provider_timeout` | Provider exceeded the request deadline |
| `500` | `internal_error` | Unexpected Facets failure; details are available only in server logs |

Clients may retry `502`, `503`, and `504` with bounded backoff. They must not automatically retry mutations unless the operation is idempotent, such as create with an `idempotency_key` or an explicitly safe read.

## Compatibility

Additive fields may appear in v1 response objects. Clients must ignore fields they do not use. Removing or renaming fields, changing nullability, changing enum meaning, or changing lifecycle requirements requires a new API version. Request objects remain strict so misspelled mutation fields cannot be silently discarded.
