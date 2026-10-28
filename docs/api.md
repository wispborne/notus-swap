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
| `q` | Text to find in the start of the prompt or in the model name. |
| `issue` | Only requests flagged with this kind of problem, such as `token_limit` or `context_full`. `any` matches any kind. Muted kinds don't count. |

Each request has, among other fields:

- `id`, `started_at`, `first_token_at`, `finished_at`
- `path` (such as `/v1/chat/completions`), `model`, `streaming`, `state`, `status_code`, `error`
- `prompt_tokens` (includes cached ones), `cached_tokens`, `completion_tokens`
- `prompt_per_second`, `predicted_per_second` (output speed), `prompt_ms`, `predicted_ms`
- `energy_j` (GPU energy in joules), `queued_ms` (time spent waiting behind other requests)
- `finish_reason`, `n_ctx` (the model's context size), `build` (the llama.cpp build)
- `client_ip`, `user_agent`
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
| `progress` | Prompt processing progress while waiting for the first token. |
| `finish` | A request ended. Fetch `/notus/api/requests/{id}` for its final numbers. |
| `settled` | A finished request's energy and wait times were worked out again. Fetch it again for the new numbers. |

A comment line (`: ping`) is sent every 20 seconds to keep the connection open. If the stream ends, connect again; the new `hello` holds everything still in flight.

## Models

```bash
curl -s http://llm.example.com/notus/api/models
```

Returns `{"stats": [...], "props": {...}}`. `stats` has each model's request count, tokens, cache rate, energy, load count and average load time, and in `current` its median speeds on its current build. `props` has what each model's llama-server last reported: context size, slots, and whether it takes images or audio, calls tools, or thinks.

| Endpoint | Returns |
|---|---|
| `GET /notus/api/models/{model}/events?limit=N` | The model's state changes, newest first: `at`, `from`, `to`, and `load_ms` when a load finished. |
| `GET /notus/api/models/{model}/builds` | The model's speeds on each llama.cpp build and launch command, with each command's arguments. |
| `GET /notus/api/speed-filter` | Which requests count toward speeds. |

Speeds are medians over requests that count. By default that means at least 128 output tokens for output speed, at least 2,048 new prompt tokens for prompt speed, and no other request running alongside.

## Hardware and energy over time

```bash
curl -s --compressed 'http://llm.example.com/notus/api/dashboard?range=24h'
```

`range` is `15m`, `1h`, `6h`, `24h`, `7d`, `30d` or `all`. The answer is gzipped when the client accepts it.

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
| `GET /notus/api/health` | `{"ok": true, "version": "..."}`, plus `restarting` while a restart waits for requests to finish. |
| `GET /notus/api/system` | notus-swap's version, update state, changelog, retention settings and database size. |
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

## Endpoints that change things

`GET /notus/api/` lists these too. They load, unload and restart models, cancel and retry requests, edit llama-swap's config, start and stop llama-swap, update or roll back notus-swap, llama-swap and llama.cpp, and save settings. They exist for the web UI's buttons, and are not described here in detail. Each one's handler in `internal/api/` has a comment saying what it takes and returns.
