# notus-swap API

notus-swap's web UI gets all its data from JSON endpoints under `/notus/api/`. Other programs can use them too. This page describes the ones for reading data. Every endpoint, including the ones that change things, is listed by the server itself at `GET /notus/api/`.

The examples use `http://llm.example.com`. Use your own server's address.

## Before you start

- **There is no login.** Anyone who can reach the server can call every endpoint, including the ones that restart services or change the config. Keep notus-swap on a network you trust.
- **The shapes can change.** These endpoints were made for the web UI. A release can add, rename or remove fields without warning. Fields are added far more often than removed.
- **Hidden models are not hidden here.** Hiding a model on the Settings page only hides it in the web UI. The API returns every model's data.
- **Times** are unix milliseconds unless a field's name says otherwise. Unknown values are `null`.
- **Model names:** requests store the name the client sent, which can be an alias from llama-swap's config. Filters and per-model numbers go by the model's ID, and a filter for a model also matches its aliases.
- **Inference goes through the same address.** Every path outside `/notus/` is passed on to llama-swap. So `POST /v1/chat/completions` and the other OpenAI-style endpoints work on the same host and port, and each call is recorded.

## For AI agents: MCP

notus-swap is also an MCP server, at `/notus/mcp`. An agent that speaks MCP gets read-only tools over the same data, with descriptions, so it doesn't need this page. To add it to Claude Code:

```bash
claude mcp add --transport http notus http://llm.example.com/notus/mcp
```

Other clients take the same address as an HTTP (Streamable HTTP) server. It needs no login. Sessions are kept in memory for an hour after their last use; after notus-swap restarts, clients start a new one on their own.

| Tool | What it gives |
|---|---|
| `list_requests` | Recorded requests, newest first, with the same filters as `GET /notus/api/requests`. Times can be RFC 3339 or a plain date. |
| `get_request` | One request: its body, the answer, the thinking and tool calls. Long texts are cut in the middle to `max_chars` (6,000 unless set), so the start and the end both stay. |
| `current_status` | llama-swap's state, loaded models, each GPU, CPU and system watts, RAM, and the requests running. |
| `model_stats` | Every configured model with its state, totals, speeds and capabilities. |
| `model_events` | One model's loads and unloads. |
| `model_speeds` | One model's speeds on each llama.cpp build and launch command. |
| `hardware_history` | Average, lowest and highest watts, temperature, VRAM and use for each source over a range, and the energy used. `points` adds a series. |
| `query_sql` | Read-only SQL, at most 1,000 rows. Only offered when the SQL API is on (see below). |

The MCP tools leave out hidden models while the Settings page hides them, unlike the JSON endpoints. `query_sql` can't filter them. The tools can't change anything.

To see hidden models, ask the agent to. It then calls a tool with `include_hidden`, and notus-swap asks you to approve in your MCP client, with a yes or no question that doesn't name the models. Each yes covers one call. If you decline, the call fails and nothing hidden is shown. notus-swap only asks when the answer would actually show a hidden model. A client that can't show such questions (MCP calls them elicitation) gets an error, and hidden models stay hidden.

This keeps an agent from showing hidden models without you saying yes. It is not a security feature: the JSON endpoints show hidden models to anyone who asks.

Request and response text comes from whoever sent the request. A stored prompt could hold text that tries to instruct an agent. The tools only read, so such text can't make them change anything. The server's instructions also tell the agent to treat that text as data.

## Find the endpoints

```bash
curl -s http://llm.example.com/notus/api/
```

The answer lists every endpoint with its method, path and a one-line description. An endpoint that is turned off on this server has an `off` field that says why.

```json
{
  "name": "notus-swap",
  "version": "2026.10.28.1200-abc1234",
  "docs": "https://github.com/wispborne/notus-swap/blob/main/docs/api.md",
  "endpoints": [
    {"method": "GET", "path": "/notus/api/requests", "about": "Request history without bodies, newest first. ..."},
    {"method": "POST", "path": "/notus/api/query", "about": "Read-only SQL on the database. ...", "off": "Turned off. ..."}
  ]
}
```

## Request history

### List requests

```bash
curl -s 'http://llm.example.com/notus/api/requests?limit=50&model=qwen3'
```

Returns `{"requests": [...], "models": [...]}`, newest first, without the request and response bodies. `models` lists every model ID seen, and only comes with the first page.

| Query | What it does |
|---|---|
| `limit` | How many to return. Default 100, at most 20,000. |
| `before` | Only requests with a lower ID than this. Use the last ID of one page to get the next. |
| `since`, `until` | Only requests that started at or after, or at or before, this time (unix ms). |
| `model` | Only this model, by ID or alias. |
| `state` | `in_flight`, `done`, `failed`, `client_gone`, `interrupted` or `cancelled`. |
| `q` | Text to find in the prompt previews (`preview`, `preview_end`, `last_preview`) or in the model name. |
| `issue` | Only requests flagged with this kind of problem, such as `token_limit` or `context_full`. `any` matches any kind. Muted kinds don't count. |

Each request has, among other fields:

- `id`, `started_at`, `first_token_at`, `finished_at`
- `path` (such as `/v1/chat/completions`), `model`, `streaming`, `state`, `status_code`, `error`
- `prompt_tokens` (includes cached ones), `cached_tokens`, `completion_tokens`
- `prompt_per_second`, `predicted_per_second` (output speed), `prompt_ms`, `predicted_ms`
- `draft_n` and `draft_accepted`: tokens drafted by speculative decoding, and how many the model kept. `null` when the server didn't report them.
- `server_stats`: Strata's own numbers for the request, or `null` for other servers: `expert_hit_rate` (the share of expert lookups found in VRAM), `pcie_share`, `disk_mb`, `first_token_ms` (on Strata's clock), `thinking_restarts` and `thinking_repeat`. They arrive a moment after the request finishes.
- `energy_j` (GPU energy in joules), `queued_ms` (time spent waiting behind other requests)
- `finish_reason`, `n_ctx` (the model's context size), `build` (the llama.cpp build)
- `client_ip`, `user_agent`
- `preview` (the start of the last user message), `preview_end` (its end), `last_preview` (the start of the last message of any kind, such as `Tool result: ...`). The last two are empty for older requests whose bodies were deleted before they were kept.
- `tags` (the Requests page's Notable chips) and `issues` (the kinds of problem found)
- `has_bodies`: whether the bodies are still stored. Bodies are deleted after the retention period; the rest is kept for good.

To read all of history, page with `before` until a page comes back with fewer requests than `limit`:

```bash
curl -s 'http://llm.example.com/notus/api/requests?limit=1000'
curl -s 'http://llm.example.com/notus/api/requests?limit=1000&before=48211'
```

### One request

```bash
curl -s http://llm.example.com/notus/api/requests/48211
```

Returns the same fields as the list, plus:

- `request`: the request body as JSON.
- `response`: the parsed answer: `content`, `reasoning`, `tool_calls`, `finish_reason`, `usage` (token counts as the server sent them) and `timings` (llama.cpp's own timings).
- `response_raw`: the response exactly as it was sent, such as the full server-sent event stream.
- `truncated`: the stored request body was cut short because it was too large.
- `issue_details`: each problem found, with the numbers behind it.
- `live`: `true` while the request is still running. `response` then holds the output so far.

### Watch requests live

```bash
curl -sN http://llm.example.com/notus/api/live
```

A server-sent event stream that never ends on its own. Each event is one `data:` line of JSON with a `type`:

| `type` | Meaning |
|---|---|
| `hello` | Sent first. `in_flight` lists the requests running now, with their output so far. |
| `start` | A request arrived. `start` holds its model, path, prompt preview and tags. |
| `delta` | New output: `content`, `reasoning`, and `tools` (names of tool calls that began). |
| `progress` | Prompt processing progress while waiting for the first token: `processed` (tokens done, cached ones included), `cache`, `per_second`, and `total` (the prompt's full size). Each is left out when the server doesn't give it. llama-server gives all but `total`. Strata gives `total`, and `processed` only when its counter is this request's. |
| `finish` | A request ended. Fetch `/notus/api/requests/{id}` for its final numbers. |
| `settled` | A finished request's energy and wait times were worked out again. Fetch it again for the new numbers. |

A comment line (`: ping`) is sent every 20 seconds to keep the connection open. If the stream ends, connect again; the new `hello` holds everything still in flight.

With `?all=1`, the stream also carries the live feed of every other server in the Servers list (see "Other servers" below). Their events have a `server` field with the server's ID, and IDs in them are that server's own. A server's own events have no `server` field. When another server stops answering, a `peer_down` event names it: forget its requests in flight. When it answers again, its own `hello` lists them again.

## Models

```bash
curl -s http://llm.example.com/notus/api/models
```

Returns `{"stats": [...], "props": {...}}`. `stats` has each model's request count, tokens, cache rate, energy, load count and average load time, and in `current` its median speeds on its current build. `props` has what each model's llama-server last reported: context size, slots, and whether it takes images or audio, calls tools, or thinks.

| Endpoint | Returns |
|---|---|
| `GET /notus/api/models/{model}/events?limit=N` | The model's state changes, newest first: `at`, `from`, `to`, and `load_ms` when a load finished. |
| `GET /notus/api/models/{model}/builds` | The model's speeds on each llama.cpp build and launch command, with each command's arguments. For Strata, each row's `experts_in_vram` is the median share of expert work done from the VRAM cache, and its commands end with the engine's settings as `--strata.<name>=<value>`. |
| `GET /notus/api/speed-filter` | Which requests count toward speeds. |

Speeds are medians over requests that count. By default that means at least 128 output tokens for output speed, at least 2,048 new prompt tokens for prompt speed, and no other request running alongside.

## Hardware and energy over time

```bash
curl -s --compressed 'http://llm.example.com/notus/api/dashboard?range=24h'
```

`range` is `15m`, `1h`, `6h`, `24h`, `7d`, `30d` or `all`. `from` and `to` (unix ms) replace it with an exact span. Buckets sit on whole multiples of `bucket_s`, so two servers asked for the same span return matching buckets. The answer is gzipped when the client accepts it.

- `series`: GPU and CPU samples, averaged into about 600 buckets of `bucket_s` seconds. Each point has `t` (unix **seconds**), `source`, `watts`, `temp_c`, `vram_used`, `vram_total` and `busy`. Sources are `gpu:<pci>` (one card), `igpu:<pci>` (built-in graphics), `gpus` (all dedicated cards added up), `cpu`, and `system` (an estimate of the whole machine at the wall).
- `cards`: the name of each `gpu:` and `igpu:` source.
- `requests`: every request in the range, with timing, token, speed and energy fields only.
- `model_events`: model loads and unloads in the range.
- `energy`: watt-hours for the GPUs and the whole system, today and over the range.

Samples are kept every second for 24 hours, every minute for 30 days, and every hour after that.

## Right now

| Endpoint | Returns |
|---|---|
| `GET /notus/api/status` | llama-swap's state and each model's state, each GPU's reading, CPU and system watts, RAM, and requests in flight. Cheap to call often; the web UI calls it every 2 seconds. |
| `GET /notus/api/health` | `{"ok": true, "version": "..."}`, plus `restarting` while a restart waits for requests to finish. Also `server_id` (made the first time the server starts), `peer_api`, `name` (the app title, or empty), `now` (the server's clock, unix ms) and `palette_used` (how many of its config models have each automatic colour), which other notus-swap servers read. |
| `GET /notus/api/peers` | The Servers list, in order: `{"self": "<this server's ID>", "servers": [...]}`. Each server has `id`, `name`, `url` (empty for this server), `self`, `state` (`online`, `checking`, `offline`, `wrong_server`, `not_notus` or `needs_update`), and, for other servers, `since`, `last_seen`, `version`, `clock_gap_ms` and `error`. The first server is the primary. |
| `GET /notus/api/system` | notus-swap's version, update state, changelog, retention settings and database size. |
| `GET /notus/api/update-source` | Where notus-swap's own updates come from. `kind` is `""` (the environment's settings), `gitea`, `github` or `off`, with the saved `gitea_url`, `gitea_repo`, `gitea_user`, `github_repo`, and `gitea_token_set` in place of the token. `env_source` names the source the environment gives, and `source` the one in use, such as `GitHub (wispborne/notus-swap)`. |
| `GET /notus/api/logs?tail=N` | notus-swap's own log as plain text. |

## SQL

For questions the endpoints above don't answer, notus-swap can run read-only SQL on its database. It is off by default. Turn it on with `NOTUS_SQL_API=1` in the `env` file, or the `-sql-api` flag.

```bash
curl -s http://llm.example.com/notus/api/query \
  -H 'Content-Type: application/json' \
  -d '{"sql": "SELECT model, count(*), sum(energy_j) FROM requests GROUP BY model"}'
```

Returns `{"columns": [...], "rows": [[...]], "truncated": false, "ms": 12}`. Each query stops after 10 seconds and returns at most 5,000 rows; `truncated` is `true` when rows were cut. The database is opened read-only, so a query can't change anything. An SQL error comes back as a 400 with SQLite's message.

The main tables are `requests` (one row per request, kept for good), `bodies` (request and response bodies, deleted by retention), `samples` (hardware readings), `model_events`, `request_issues` and `model_props`. To see every table and its columns:

```bash
curl -s http://llm.example.com/notus/api/query \
  -H 'Content-Type: application/json' \
  -d '{"sql": "SELECT name, sql FROM sqlite_master WHERE type = '\''table'\''"}'
```

## Other servers

A notus-swap can show other notus-swap servers, added on its Settings page. Calls to another server go through this one at `/notus/peer/<id>/`, followed by the other server's own path. Only `/notus/api/...` paths and llama-swap's `/logs/...` streams are passed on. For example, another server's status:

```bash
curl -s http://llm.example.com/notus/peer/<id>/notus/api/status
```

A server that doesn't answer gets a 503 at once, such as "PC is asleep or off". A call that already came through another notus-swap gets a 508, so calls go one step at most.

## Endpoints that change things

`GET /notus/api/` lists these too. They load, unload and restart models, cancel and retry requests, edit llama-swap's config, start and stop llama-swap, update or roll back notus-swap, llama-swap and llama.cpp, and save settings. They exist for the web UI's buttons, and are not described here in detail. Each one's handler in `internal/api/` has a comment saying what it takes and returns.
