# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this project is

notus-swap adds features on top of [llama-swap](https://github.com/mostlygeek/llama-swap), a proxy that swaps local LLM servers on demand. It was built for one Linux server with AMD GPUs, but nothing in it may be tied to one machine.

## Commands

```bash
go test ./...                                   # all tests
go test ./internal/capture -run TestStream      # one test
go vet ./...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-X main.version=dev" -o notus-swap ./cmd/notus-swap
```

Run it locally against a llama-swap:

```bash
go run ./cmd/notus-swap -listen 127.0.0.1:18080 -upstream http://127.0.0.1:8081 -db notus-swap.db
```

The web UI is in `web/` (Svelte 5, Vite, Tailwind 4). Go embeds `web/dist`, so build the UI before `go build` or the binary serves a placeholder:

```bash
cd web && npm install && npm run build   # then go build as above
npm run check                            # type-check the UI
npm run dev                              # live-reload UI on :5173; proxies /notus/api to 127.0.0.1:18080, or to $NOTUS_DEV_API if set
```

Each flag can also be set in the `env` file, as `NOTUS_LISTEN`, `NOTUS_UPSTREAM`, or `NOTUS_DB`. The SQLite driver is `modernc.org/sqlite`, which is pure Go, so builds need no cgo. That also means `-race` doesn't work.

## Code layout

- `cmd/notus-swap`: the main program. It sets flags, serves `/notus/api/health`, and runs hourly body pruning.
  - `listen.go`: when systemd's `notus-swap.socket` started notus-swap (`LISTEN_PID` and `LISTEN_FDS`), it uses the socket on file descriptor 3 and ignores `-listen`. systemd keeps that socket open across restarts, so requests during a restart wait instead of being refused.
  - Shutting down ends every GET request at once (`endOnShutdown`), because the live feed and log streams never end on their own. Other requests get 10 seconds to finish.
- `internal/proxy`: the reverse proxy to llama-swap. It flushes streamed chunks right away and answers 503 when llama-swap is down.
- `internal/capture`: middleware that records POST requests under `/v1/` and `/upstream/`.
  - It tees the response while it streams.
  - It finds the first-token time by scanning SSE lines.
  - It reads usage and llama.cpp `timings` out of the response.
  - Reasoning text comes from `reasoning_content`, or from `reasoning`, which is vLLM's name for it.
  - Cached prompt tokens (`requests.cached_tokens`) come from `usage.prompt_tokens_details.cached_tokens`, or else `timings.cache_n`. `prompt_tokens` includes the cached ones. `prompt_per_second` counts only the tokens actually processed. Requests stored before this column existed were filled in once at startup by `Store.FillCachedTokens`.
  - The server's build (`requests.build`) comes from `system_fingerprint`, which llama-server puts in every response, such as `b11146-7fe450e19`. It says nothing about the flavor (ROCm, Vulkan). When a response has no `system_fingerprint`, the build comes from the model's `/props` instead. Requests stored before this column existed were filled in once at startup by `Store.FillBuilds`.
  - The model's command (`requests.cmd_hash`): when llama-swap reports a model ready, `learnCmd` in `main.go` reads its `cmd` from llama-swap's `GET /running`. That text already has every macro filled in. It is split into arguments like a shell would, the port from the model's `proxy` address becomes `${PORT}`, and the first 12 hex digits of the SHA-256 name it. The arguments are kept once per hash in `model_cmds`. Only `cmd` counts: the model's `env` list isn't in `/running`. Older requests have no hash.
  - The source (`requests.client_ip`, `requests.user_agent`): the address is the first one in `X-Forwarded-For`, or else `X-Real-IP`, or else the connection's address (`ClientIP`). The headers are trusted, because notus-swap only runs on a trusted network. Requests stored before these columns existed have no source. `web/src/lib/source.ts` turns the User-Agent into a short name, such as Codex or curl.
  - When a stream breaks, `ReverseProxy` panics with `http.ErrAbortHandler`. The middleware recovers, records the request, then panics again.
  - Without llama.cpp `timings` (vLLM), notus-swap measures the speeds itself (`measureSpeeds`). The first token time is then the first Chat Completions chunk, even one with no text. vLLM sends that chunk once the prompt is done. On 2026-09-29 some vLLM streams stalled after it and then sent every token within a second, which gave speeds of up to 6,000 tok/s. `Remeasure` fixed the stored ones once at startup.
  - A request whose stream's last event arrived (`data: [DONE]`, or `response.completed`) is `done` even if the client then hangs up. Codex hangs up right after `response.completed`.
  - The OpenAI Responses API (`/v1/responses`, used by Codex) is read too. Its deltas build the live output, and the whole answer in `response.completed` replaces them. Its status becomes the Chat Completions finish reason (`stop`, `tool_calls`, `length`). Its events carry no `system_fingerprint`, so these requests take the build from `build_info` in the model's `/props`, stored in `model_props`. `web/src/lib/responsesInput.ts` shows its `instructions` and `input` items as chat messages.
- `internal/capture/retry.go`: Cancel and Retry, from buttons in the Requests page's expanded row.
  - Cancel (`POST /notus/api/requests/{id}/cancel`, `Hub.Cancel`): the capture handler gives each request a context that the hub can cancel, with the cause `ErrCancelled`. That ends the request to llama-swap, so llama-server stops working on it. A client still waiting gets a 499 with an OpenAI-style error (a 4xx, so clients don't send it again on their own). A client already streaming sees its stream stop. The request is stored as `cancelled`, with the output so far.
  - Retry (`POST /notus/api/requests/{id}/retry`, `capture.Retry`): sends the stored body to the same path again, through the capture handler, in the background. The answer is only stored and shown live; nothing else reads it. The new request's `requests.retry_of` names the original. Headers aren't stored, so the retry has only `Content-Type: application/json`; a llama-swap with `apiKeys` would refuse it. Requests whose body was deleted, or was too large to store in full, can't be retried.
- `internal/capture/issues.go`: flags problems in a request's output. `Check` runs when each request finishes, and results go to the `request_issues` table (kind, `warning` or `info`, and the numbers behind it as JSON). `requests.finish_reason` and `requests.n_ctx` are stored too, so flags outlast the bodies.
  - Kinds: `token_limit`, `server_limit`, `context_full`, `cut_off` (cause unknown), `thinking_budget`, `context_error`, and `context_near_full` (info).
  - Only OpenAI-style `/completions` and `/responses` paths are checked, and only responses that arrived in full. llama-server marks every Responses answer `completed`, even one that stopped at a limit, so for those the checks find limits from the token counts. Fill-in-the-middle requests are skipped, and so are small limits set on purpose (64 tokens or less, or 16 or less when only thinking came back).
  - The context size comes from llama-server's `/props`, fetched through `/upstream/<model>/props` when llama-swap reports a model ready (`Monitor.OnReady`), and stored in `model_props` under the model's ID and each alias. Asking a model that isn't loaded would load it, so it's only asked while ready. When a model's size is first learned, its requests stored without one are checked again.
  - Raising `IssueChecksVersion` makes the next startup check every request that still has its bodies again.
  - Mutes are the `issue_mutes` setting, a list of `{kind, model}`. Muted issues are still stored. The UI text for each kind is in `web/src/lib/issues.svelte.ts`. The Requests page filters by issue, and the expanded row shows an Issues box. Unmuted warnings also show as the first chips in the Notable column, in amber even with Colorful off (`rowChips` in `notable.ts`). `context_near_full` gets no chip, because it was too noisy. Failed requests get no chip either, because the Status column shows them.
- `internal/capture/notable.go`: the tags for the Requests page's Notable column, stored as a JSON list in `requests.tags`.
  - Kinds: `tool_calls` (with each call's name and arguments, cut to 80 characters), `thinking` (what the request asked for, the words that came back, and how many tokens they took), `no_thinking`, `images`, `json`, `schema`, one per kind of call other than Chat Completions (`responses`, `completions`, `fim`, `embeddings`, `rerank`, `messages`), `not_streamed` (only for calls that can stream), and `cache_miss` (a prompt over 8,000 tokens with under 10% cached).
  - Thinking tokens (`Parsed.ReasoningTokens`) come from `usage.completion_tokens_details.reasoning_tokens` or `usage.output_tokens_details.reasoning_tokens` when the server sends them. Otherwise they are an estimate: the output tokens times the share of the output's characters that are thinking (tag field `est`). While in flight, the count is the chunks that carried thinking (`reasoning_chunks` on the live feed). The chip shows the count, such as `7.2k`.
  - `RequestTags` runs when a request arrives, and its tags go out with the live `start` event. `Tags` runs when it finishes and puts the answer's tags first.
  - Raising `TagsVersion` makes the next startup work out the tags again for every request that still has its bodies. Older requests without bodies have no tags.
  - The UI's labels and popover text are in `web/src/lib/notable.ts`. One popover serves the whole page (`popover.svelte.ts`, `ChipPopover.svelte`).
- `internal/capture/slots.go`: while a request waits for its first token, `SlotPoller` reads llama-server's `/slots` once a second through `/upstream/<model>/slots`, and sends a `progress` event on the live feed: prompt tokens done so far, cached tokens, and the speed.
  - `/slots` doesn't give the prompt's full size. `n_prompt_tokens` grows a batch ahead of `n_prompt_tokens_processed`, so the UI shows "so far" and no percentage.
  - The processed count moves a batch at a time (1,024 tokens about every 2 s in one test), so the speed runs from the first step seen to the latest, and needs two steps.
  - It only matches when one request waits on the model and one slot is processing with nothing generated yet. It asks only models that are ready. If `/slots` fails (such as `--no-slots`), it logs once and stops asking that model until it loads again.
  - The UI shows these live values dimmed, and the stored numbers replace them when the request ends. Live Out t/s is output chunks divided by the time from the first chunk to the latest.
- `internal/capture/hub.go`: the in-memory list of requests in flight. It feeds `/notus/api/live`, and `Subscribe` returns a snapshot in the same step, so the UI never misses or repeats output.
- `internal/api`: JSON for the UI. `GET /notus/api/requests` (filters: `q`, `model`, `state`, `before` for paging), `GET /notus/api/requests/{id}` (bodies plus parsed output; live output while in flight), and `GET /notus/api/live` (SSE: `hello`, then `start`, `delta`, `progress`, and `finish`). `delta` carries `tools`, the names of tool calls that start in it.
- `web/`: the Svelte UI, served under `/notus/` by `web/embed.go`. `src/lib/live.svelte.ts` holds the one shared live connection. `web/public/.gitkeep` exists so each build recreates `dist/.gitkeep`, which keeps `go build` working before the UI is built.
- `internal/llamaswap`: follows llama-swap's `GET /api/events` feed, which is server-sent events. Each event is `data:{"type":..., "data":"<JSON string>"}`. It uses only `modelStatus` events, which carry the full model list, including stopped models, on every state change. If the feed drops, it reconnects every 2 s. llama-swap's route table is in its `internal/server/server.go`. `/running` and `/api/models/unload/{model}` exist too.
- `internal/gpu`: reads AMD cards from sysfs (`/sys/class/drm/card*/device`, vendor `0x1002`) every second, for any number of cards. It reads power from `power1_average`, falling back to `power1_input`, both in microwatts. Temperature is the junction sensor if present, otherwise edge. It also reads VRAM and busy percent. Any card with under 4 GB of VRAM is marked `integrated`. On some Ryzen CPUs, the built-in graphics' power sensor is labelled `PPT` and reads the whole CPU socket, not the graphics. `Reading.SocketPower()` detects this. Such a card is left out of every GPU total and used as the CPU reading whenever RAPL can't be read (`cpu_source` is `"ppt"` or `"rapl"`). Other built-in graphics count toward the status bar's GPU total, labelled `iGPU`, on purpose. Use `-drm-root` to point it at a fake folder when testing on a machine without these cards.
- `internal/metrics`: the one-second sampling loop. It reads the GPUs and CPU, stores samples, keeps `Latest()` for the status bar, and keeps an hour-long ring of GPU watts. It also records model state changes from the llama-swap watcher, with load time measured from starting to ready.
  - `Account` works out each finished request's GPU energy (`requests.energy_j`) and how long it waited behind other requests (`requests.queued_ms`). The rules are in `Share` (`share.go`).
  - A request is being worked on from when llama.cpp started its prompt until it ended. That start is counted back from the end using llama.cpp's `prompt_ms` and `predicted_ms`. Without timings, it is the first prompt progress or output seen, or else the arrival time.
  - Each second's power is split evenly between the requests being worked on. A request waiting its turn gets none, and that wait is its `queued_ms`. When nothing is being worked on, such as during a model load, the power is split between the waiting requests.
  - With one slot, Codex sends a short summary request while its main request runs. Before this, the waiting request took half the power, which gave it a very high energy per token. `Recount` fixed the stored requests that overlapped others, once at startup.
  - Time to first token on the Dashboard and in the Models page's builds leaves out `queued_ms`. The Requests table still shows what the client saw.
- `internal/store/samples.go`: the `samples` table. `tier` is the bucket size in seconds (1, 60, 3600). Source names:
  - `gpu:<pci>`: one dedicated card, named by PCI address because card numbers can change between boots
  - `igpu:<pci>`: built-in graphics
  - `gpus`: all dedicated cards added up
  - `cpu`
  - `system`: the at-the-wall estimate

  `Rollup` runs every minute and can safely run again over the same window. 1-second rows are kept for 24 h and 1-minute rows for 30 days. `Series` averages into about 600 buckets.
- `internal/rapl`: CPU package power from `/sys/class/powercap/intel-rapl:N/energy_uj`, calculated from the change between two readings. The file is root-only unless the udev rule in `docs/install.md` step 9 is installed. The status bar's "system ≈ W" is `(dedicated GPUs + CPU + NOTUS_SYSTEM_BASE_WATTS) / NOTUS_PSU_EFFICIENCY`, with defaults of 35 W and 0.90. Built-in graphics are left out because the CPU package reading already includes them. This estimate was chosen over needing a smart plug.
- `GET /notus/api/dashboard?range=15m|1h|6h|24h|7d|30d|all` (`all` starts at the oldest stored request, sample or model event, via `Store.Earliest`) returns everything the Dashboard draws: series, cards, requests in range, model events, and energy for today and for the range. The layout is stored with `GET/PUT /notus/api/settings/dashboard_layout`. `POST /notus/api/models/{m}/load` loads a model by sending a request to `/upstream/{m}/health` in the background. `POST /notus/api/models/{m}/unload` passes the unload on to llama-swap.
- `web/src/pages/Dashboard.svelte` holds the widget list and the default layout. gridstack handles moving widgets. Positions are kept outside Svelte state, so dragging doesn't re-render the widgets. Charts use uPlot through `lib/dashboard/UChart.svelte`. It rebuilds a chart only when `options` changes, and updates it in place when `data` changes. The time window is a plain `Window` object that the widget updates in place, so refreshing the data doesn't rebuild the charts.
  - Model colours come from `lib/modelColors.svelte.ts`, the same on every page. Models take the 9 colours in llama-swap's config order, so neighbours in the config never match. Models no longer in the config come after, in name order. The palette was checked for contrast on the surface colour; check any new colour the same way.
  - Zoom (`lib/dashboard/zoom.svelte.ts`): scrolling over a chart zooms around the cursor on both axes. Time zooms on every chart together; y zooms only on the chart under the cursor (not the Timeline's model lanes, `zoomY={false}`). Dragging a zoomed chart pans it, and a double-click or the header's Reset button ends every zoom. No data is fetched again. Choosing another range ends the zoom. The y zoom lives in `UChart`, so it outlasts a rebuild of the chart.
  - Each widget's `options` reads its data inside `untrack`, after `void`-reading the keys that should rebuild it. Without this, arrays that are new on every refresh rebuilt the charts every few seconds, which dropped drags and double-clicks.
  - Y axes use round tick values (`niceSplits` in `charts.ts`), with fewer ticks on short charts. Prompt processing and energy per token use a log scale, because a few large values flattened the rest. Each scatter chart names the request under the cursor.
  - Widget settings: in Edit layout, a widget with settings shows a gear in its header. The settings are kept per widget in the saved layout's `opts`. The GPU power, VRAM and temperature gauges choose how to show more than one card (`byMode`): "Show highest only", "Split" (one piece of the arc per card), "Stacked" (every card filled from the start of the one arc, emptiest on top; the first card in the widget's colour, the others pink, cyan, then blue), or "Combined" (one arc for the total). Defaults: power Combined, VRAM Split, temperature Stacked. Temperature has no Combined, because adding temperatures means nothing. The per-model scatter charts choose their legend: "Dots only" (the default: each model's dot in a small box over the chart's top right corner, with its name as the tooltip), "Names", or "Hidden". The readout under the cursor still names the model. "Names" shortens them (`shortNames.ts`): models whose names start with the same word drop the start they share, cut at a "-".
- `internal/llamaconfig` and `web/src/pages/Config.svelte`: the config editor. The paths come from `NOTUS_LLAMA_SWAP_CONFIG` and `NOTUS_LLAMA_SWAP_BIN`; without a config path, the editor is off and the API answers 503.
  - Checks: YAML syntax with yaml.v3, reporting the error line. Then the installed llama-swap runs `-validate -config <temp file>`, so validation matches the running version. Its config package is internal and can't be imported. A llama-swap too old for `-validate` answers "flag provided but not defined", and the check then reports that it couldn't run.
  - Routing check (`RoutingProblems` in `routing.go`): notus-swap's own check for model names in the routing settings that `models` doesn't define, because llama-swap won't start with them. It copies llama-swap's rules from its source, and runs even when llama-swap's check can't. It covers group members and scheduler priority keys (a model ID or alias), and matrix vars (a model ID only), evict cost keys and set expressions (a var or a model ID). Only the engine chosen by `routing.router.use` is checked. The older top-level `groups` and `matrix` are checked too. The problems come back as `routing_problems` (line and message) from `Check`, the editor marks the first one, and they block a save like a failed llama-swap check does. "Save anyway" skips them too, in case this check is wrong for a newer llama-swap.
  - Saving: the save carries the hash of the version the edit started from, and gets a 409 if the file changed since. Before writing, the old file is copied to `<config dir>/config-backups/config.yaml.<timestamp>`, and the newest 20 are kept. The new file goes to a temporary file that is renamed into place, keeping the file's permissions. llama-swap's `-watch-config` checks the file's modified time and size every 2 s, so the rename triggers a reload.
  - Waiting saves: config reloads can interrupt streams. With requests in flight, `PUT /notus/api/config` validates the edit (`Editor.CheckSave`) and returns 202. `llamaconfig.Held` writes it when requests finish, checking the original hash again. A newer save replaces the waiting one. `GET /notus/api/config/held` reports the pending save and last result; `POST /notus/api/config/held/now` starts it immediately, and `DELETE /notus/api/config/held` cancels it.
  - The UI checks 700 ms after the last key. Save opens a diff review. "Save anyway" skips the routing and llama-swap checks; YAML must still be valid.
  - Unsaved edits survive navigation and reloads through `localStorage` (`config_draft`). The draft keeps the original file and hash, so saving returns 409 if the file changed on disk.
  - The Config, Dashboard, Models and Logs pages load their code only when opened (`import()` in `App.svelte`), which keeps the first load around 170 kB.
- `GET /notus/api/status`: feeds the status bar. The UI polls it every 2 s, and it answers from the monitors' cached values.
- `internal/selfupdate` and `web/src/lib/system/Manage.svelte`: the Settings page's Update and Roll back buttons.
  - Source: Gitea when `GITEA_URL`, `GITEA_REPO`, `GITEA_USER` and `GITEA_TOKEN` are all set, using basic auth with the token as the password. Otherwise GitHub releases of `NOTUS_GITHUB_REPO` (default `wispborne/notus-swap`), with `GITHUB_TOKEN` if set. An empty `NOTUS_GITHUB_REPO` turns updates off. `Updater.Source` names the one in use, and the Settings page shows it. `scripts/update-notus-swap.sh` follows the same rule.
  - Install: download `notus-swap-<os>-<arch>` and its `.sha256` from `<server>/<repo>/releases/download/<tag>/<file>` (the same layout on both), then check the checksum. Rename the running binary to `<exe>.prev` and move the download into its place. Write `<exe>.update.json`, then shut down cleanly. systemd's `Restart=always` starts the new binary.
  - Planned restarts (update, roll back, the Restart button) go through `internal/restart`. `Plan.Ask` records the restart, and `main.go` shuts down once `Hub.Idle` says no requests are in flight. Until then the server keeps answering, and `/notus/api/health` and `/notus/api/system` report `restarting` with the count in flight. Update and roll back answer 409 while a restart waits. `POST /notus/api/restart/notus-swap/now` (the "Restart now" button) skips the wait; answers still streaming then get 10 seconds.
  - Cancellation: `POST /notus/api/restart/notus-swap/cancel` cancels a waiting restart. `UndoInstall` restores the running binary and removes the download and marker; no previous version remains for rollback. `UndoRollback` swaps the binaries back. If undo fails, the restart stays pending.
  - `internal/idlewait` handles the wait for config saves and restarts. `Start` replaces a waiting job; `Now` skips the wait. `Cancel` runs undo under the lock, preventing the job from starting during undo. Running jobs cannot be cancelled. `restart.Plan` keeps the first restart; `llamaconfig.Held` replaces a waiting save with the newest one.
  - At startup, `CheckOnStart` counts attempts in that marker. On the third start without a confirmation, it swaps `.prev` back and exits. After 30 s of running, `Confirm` removes the marker and writes `<exe>.update-result.json`.
  - Manual rollback swaps the binary and `.prev`, so rolling back twice returns to where it began.
  - `Updater.Watch` checks for releases every 15 minutes. `/notus/api/status` sends `update_available` from that check, and the sidebar puts a dot on the Settings icon when it is true.
  - The same page restarts llama-swap (`systemctl restart $NOTUS_LLAMA_SWAP_UNIT`, allowed by the polkit rule) and notus-swap. It also sets body retention: the `retention` setting `{days, gb}` overrides `-keep-days` and `-keep-gb`, and saving it prunes at once.
  - The same section shows the database size from `Store.DiskUsage`: file and `-wal` sizes, free pages, and the body total. It avoids SQLite's `dbstat` table, which would read the whole file.
- `internal/llamaupdate` and `web/src/lib/system/LlamaUpdates.svelte`: the Settings page's llama-swap and llama.cpp updates, from GitHub releases.
  - `Manager.Watch` checks every hour, using 3 API calls: llama-swap's latest release, llama.cpp's latest release, and llama.cpp's 10 newest releases for the newest nightly. GitHub allows 60 calls an hour per IP without `GITHUB_TOKEN`. The status poll's `update_available` includes a newer llama-swap or weekly llama.cpp release.
  - Each item's Changelog button lists recent releases with their notes. llama-swap and llama.cpp use `GET /notus/api/llama-updates/{component}/changelog` (`Manager.Changelog`, 1 API call, cached 15 minutes). A llama.cpp nightly's notes are cut down to the title of its change. notus-swap's list is `changelog` in `GET /notus/api/system`.
  - Installs run one at a time in the background (`Manager.Start`). The page polls `GET /notus/api/llama-updates` every second while one runs, for the step and the bytes downloaded. Downloads are checked against the SHA-256 in GitHub's API (`digest`).
  - llama-swap: `-version` prints `version: v258 (...)`, and the running one answers `GET /api/version`. An install extracts the binary, runs `<new> -validate -config <config>`, renames the old binary to `<bin>.bak` (the same name as llama-swap's own update script), restarts `NOTUS_LLAMA_SWAP_UNIT`, and waits up to a minute for `/api/version` to report the new tag. If it doesn't, the binaries swap back and llama-swap restarts again. Roll back swaps the binary and `.bak`.
  - llama.cpp: a weekly release (`v0.5.0`) has no downloads, only `nightly-tag.txt` naming its build (`b11146`). Nightly builds are pre-releases, with files named `llama-<build>-bin-<flavor>.tar.gz` holding one top-level folder. Each build unpacks into `NOTUS_LLAMA_CPP_DIR/llama-<build>-bin-<flavor>`, and the relative link `current` is replaced in one rename. The build `current` pointed at before is kept. Older builds of the same flavor are deleted unless `/proc/*/exe` shows a program running from one. Roll back points `current` at the newest other build.
  - Tests that need shell scripts or folder links skip on Windows. To run them on the dev machine, build the test binary with `GOOS=linux go test -c` and run it in WSL.
- `internal/autoload` and `web/src/lib/system/DefaultModel.svelte`: the Settings page's default model. When llama-swap is up with no model loaded for the configured number of minutes (15 by default), notus-swap loads the chosen model through `/upstream/<model>/health`.
  - The setting is `default_model` (`{enabled, model, minutes}`), read and saved with `GET/PUT /notus/api/default-model`. Saving clears a pause and starts the countdown again.
  - The countdown resets while any model is loaded (any state but stopped) and while llama-swap is down. It restarts when notus-swap restarts.
  - An unload from the web UI (one model or all) pauses it until any model next starts loading. The pause is kept in memory only.
  - The `starting` and `ready` events of its own load are stored with `model_events.auto` set. The Dashboard's swaps list and the Models page's loads mark them.
  - llama-swap's scheduler makes a request for another model wait until a load in progress finishes, so a request that arrives during an automatic load waits for both loads.
  - Models with a `ttl` would repeatedly unload and reload, so the page warns about them. `llamaconfig.TTLs` reads each model's `ttl` (or `globalTTL`) from the config file; there is no warning without `NOTUS_LLAMA_SWAP_CONFIG`.
- `scripts/update-notus-swap.sh`: the shell updater, for when the web UI can't be reached. See `docs/install.md`.
- `web/src/pages/Logs.svelte` and `web/src/lib/logs/`: the Logs page. One panel or two side by side, each reading one plain-text stream: llama-swap's `/logs/stream/proxy`, `/logs/stream/upstream` (all models' output), `/logs/stream/<model>`, or notus-swap's own `/notus/api/logs/stream`. llama-swap's streams pass through the proxy untouched. Each stream sends its history (llama-swap keeps 100 KB) and then new text. `LogStream` reconnects when a stream ends, waiting 3 s and doubling up to 30 s while it fails. `ansi.ts` is copied from llama-swap and keeps its MIT notice.
  - `internal/logbuf` keeps notus-swap's newest 256 kB of log in memory. `main.go` writes the log to both stderr and this buffer.
  - Hidden models: `privacy.logLine` drops lines naming a hidden model. The all-models log can't be split by model, so `privacy.logSource` turns it off while any model is hidden. One model's log is still available.
- `web/src/pages/Models.svelte`: every model in llama-swap's config, from the status poll, with load, unload, and unload all (`POST /notus/api/models/unload`). Numbers come from `GET /notus/api/models` (`Store.ModelStats`: totals, speeds averaged over the last 50 requests, average load time). Speeds, here and by build, leave out requests that were more than half cached (`mostlyFresh` in `samples.go`). A row opens `lib/models/ModelDetail.svelte`: the model's log, its loads (`GET /notus/api/models/{m}/events`), its speed on each server build and command (`GET /notus/api/models/{m}/builds`, token-weighted, finished requests only, speeds and TTFT without mostly-cached requests; the UI numbers commands in order of first use and lists the flags that changed, from `lib/models/cmdDiff.ts`), and recent requests. Models seen in requests but no longer in the config are listed after. Links marked `data-nav` change page without a reload, and `/notus/requests?model=M` opens Requests filtered to M.
- `internal/store`: SQLite. `requests` holds metadata and is kept for good. `bodies` holds request and response bodies and gets pruned. Schema changes are appended to `migrations` and tracked with `PRAGMA user_version`.
- `docs/install.md` and `docs/runner-setup.md`: the guides for installing notus-swap and for setting up Gitea runners. They use `YOUR_USER`, `owner`, and `example.com` in place of real names.
- Releases: `.github/workflows/release.yml` and `.gitea/workflows/release.yml` do the same thing on each push to `main` that changes more than Markdown files and `docs/`: check and build the UI, run `go vet` and the tests, build `notus-swap-linux-amd64` and its `.sha256`, and publish a release tagged `YYYY.MM.DD.HHMM-<sha7>`. Gitea ignores `.github/workflows` while `.gitea/workflows` exists.

## Machine-specific details

Nothing about one machine goes in a tracked file: no host names, hardware, user names, paths, addresses, domains or firewall rules. That includes docs, code comments, and tests. Anything that differs between machines is a setting in the `env` file (with a flag of the same name where it makes sense), never a value in the code. When a change moves something into `env`, say which lines to add on existing servers so they keep working as before.

## Goals

- **Request/response inspection.** Easier viewing of requests and responses. Show responses while they are still streaming, next to the request that produced them.
- **Dashboard and metrics.** More and better graphs. Show GPU power draw. Let the user configure the dashboard.
- **Management.** Restart llama-swap from the web UI. Edit the llama-swap config from the web UI. Update llama-swap and llama.cpp from the web UI.
- **Deployment.** Hands-off updates from the web UI, with automatic roll back.
- **Keep taking upstream llama-swap updates** with little or no merge work.

## Architecture

notus-swap is a **separate layer**, not a fork. This is decided.

- notus-swap is its own service. It sits in front of llama-swap as a proxy and has its own web UI. llama-swap stays unmodified, so it can be updated freely.
- All clients go through notus-swap. llama-swap listens on localhost only.
- Ports: notus-swap takes over `:8080`. llama-swap moves to `127.0.0.1:8081`. So the reverse proxy and firewall in front of the server need no changes.
- URL layout: notus-swap's own UI and API live under `/notus/`. Every other path (`/v1`, `/ui`, `/api`, `/upstream`, and so on) passes through to llama-swap and is still captured. A GET of `/` itself redirects to `/notus/`. llama-swap's own UI is still at `/ui/`.
- No login. The site must only be reachable from a trusted network, never the open internet.
- `notus-swap.service` uses `Wants=`, not `Requires=`, on `llama-swap.service`. notus-swap stays up while llama-swap is down, shows that in the UI, and answers proxy requests with a 503.
- The UI rebuilds only the parts of the llama-swap UI that are actually used.
- Request/response capture: the proxy copies streamed responses as they pass through and stores both the request and the response in SQLite.
  - Only inference calls are captured: POST requests under `/v1/` and `/upstream/`. Everything else passes through without being stored, including GETs such as `/v1/models`.
  - Request and response bodies are deleted after 90 days, or sooner once they pass 20 GB, oldest first. Both limits are settings in the UI.
  - Metadata is kept for good. This covers model, status, timing, token counts, and energy.
- Graphs: combine data from llama-swap's API with the proxy's own captured data. llama-swap also keeps its own SQLite database, `activity.db`, which may be another data source.
- GPU power: read `/sys/class/drm/card*/device/hwmon/hwmon*/power1_average` (value is in microwatts), or use `amd-smi`. This does not depend on llama-swap.
- A server can have several GPUs. All GPU code must handle any number of cards:
  - Sample power, VRAM, temperature, and utilisation per card.
  - Store samples keyed by card.
  - Show totals, with a per-card breakdown in the Dashboard and status bar.
- Config editing: validate the YAML, then write the file. llama-swap already runs with `-watch-config`, so it reloads when the file changes.
- If a feature truly needs a hook inside llama-swap, send a small PR upstream instead of keeping a fork.

### Features

- **Rebuilt from llama-swap's UI:** models (load and unload), logs, activity, performance, and hardware. The playground is not rebuilt, because Open WebUI covers it. llama-swap is MIT-licensed. Copy and adapt its Svelte components where that saves real work, and keep the MIT notice. Copied code becomes notus-swap's own and is not kept in sync with upstream.
- **Request viewer:**
  - A list of requests, filterable by model, status, and time.
  - In-flight requests appear at the top and update live as tokens stream in.
  - Opening one shows the request messages and the response side by side.
  - Markdown is rendered. Reasoning text and tool calls are shown separately. A raw JSON toggle is available.
  - Each request shows time to first token, tokens/sec, and energy used.
- **Graphs:**
  - generation and prompt-processing tokens/sec per model
  - time to first token
  - request count and number of requests in flight
  - GPU power over time
  - energy per request (joules per token)
  - VRAM used
  - GPU temperature and utilization
  - model load time and swap count
- **GPU sampling:** read sysfs every 1 second and store the readings in SQLite. Keep 1-second data for 24 hours, 1-minute averages for 30 days, and 1-hour averages for good.
- **Dashboard:** one dashboard on a drag-and-drop, resizable grid. Pick widgets from a list and set the time range. The layout is saved on the server. Several saved dashboards may come later.
- **Config editor:** a YAML editor (CodeMirror 6) with a live check, a diff review before saving, the last 20 backups kept, and a "save anyway" option when llama-swap's check fails or can't run. The YAML check always applies. Details are under Code layout.

- **Hidden models** (for taking screenshots without revealing some models):
  - The Settings page lists models with a hide switch for each, plus one "Show hidden models" toggle. Both are server-side settings (`hidden_models`, `show_hidden`).
  - Hidden models are still captured and measured like any other model.
  - While the toggle is off, no hidden model name appears anywhere in the UI: status bar, Requests (rows, filters, live feed), and Dashboard (lanes, tables, charts, swaps, readouts).
  - Totals that don't name a model, such as power, stay as they are.
  - Filter in one place per data source; don't scatter checks across components.
  - The UI side is `web/src/lib/privacy.svelte.ts`. `privacy.visible(model)` checks a single model. `privacy.dashboard(data)` filters the Dashboard's data once, before any widget sees it. The settings arrive with every `/notus/api/status` poll, so a change on one device reaches every open page within 2 s. `App.svelte` shows no page until the settings have been read once.
  - The places that filter are the status bar's loaded models, the Requests rows and model dropdown, the Dashboard data, the models table, and the in-flight list. Any new place that shows a model name must go through `privacy.visible`.
  - On the Settings page, hidden names stay covered until clicked while the toggle is off, so that page can be screenshotted too.

### UI

- Six pages:
  - **Dashboard.** Performance and hardware are widgets here, not separate pages.
  - **Requests.** llama-swap's activity list merged with the request viewer.
  - **Models.**
  - **Logs.**
  - **Model Config** (the llama-swap config editor; its route is still `/notus/config`).
  - **Settings** (route `/notus/system`, file `pages/System.svelte`). Restart llama-swap, update or roll back notus-swap, llama-swap and llama.cpp, and settings such as retention limits, the default model, hidden models and the theme.
- Navigation is a collapsible left sidebar. Its pages can be dragged into another order, saved per browser (`sidebar_order`, `lib/navOrder.svelte.ts`). The collapse button is at the bottom. The current page has a green bar on the sidebar's left edge. The sidebar stays in view while the page scrolls. A thin status bar across the top of every page shows, by default in this order:
  - whether llama-swap is up
  - how many requests are in flight
  - VRAM and RAM in use
  - GPU watts, then the system estimate
  - which models are loaded. This is last because its width changes as models load and unload.

  Items can be dragged on the bar, or moved and hidden from the "⋯" menu at its right end (`lib/statusItems.svelte.ts`). The choice is saved per browser (`statusbar_items`).
- Scrollbars are styled to the theme in `app.css`.
- Icons must look crisp. Draw them as inline SVG, not text characters or emoji, which render differently on each system. Icons are filled, solid shapes, not hollow outlines. Put straight lines on whole pixels: a viewBox whose units are pixels at the drawn size, whole-pixel stroke widths, and pixel sizes around the icon so it doesn't sit on a half pixel. Where an icon can't avoid half pixels, `shape-rendering="crispEdges"` snaps straight edges. The sidebar icons are in `lib/NavIcon.svelte`, and the chip icons in `lib/NotableChips.svelte`.
- Tooltips: give elements a plain `title`. `lib/tooltip.ts` replaces the browser's tooltip on every page: it shows at once and follows the mouse. It removes the title while the mouse is over the element and puts it back after. The Notable chip popover follows the mouse the same way until clicked.
- Dark themes only. The default is the "Sigma" theme from TriOS:
  - primary `#40D7A3`
  - secondary `#18FFFF`
  - surface `#21242B`
  - surface container `#282C34`

  Extra chart colors must stay easy to tell apart on these surfaces.
- Other themes: the Settings page has a theme picker, saved per browser (`theme`, `lib/theme.svelte.ts`). Sigma is the default. The others are TriOS themes ([REDACTED], Player, One Dark, Independents, Lavender, Knights of Ludd, Sindrian Diktat), in `src/themes.css` as `:root[data-theme=...]` blocks.
  - Each sets surface, panel, primary and secondary. The other shades are mixed from those by the rules at the top of `themes.css`. Some TriOS colours were changed so they don't look like the warning, error or thinking colours; the file lists them.
  - Warning, error, violet, model colours, chip colours and chart series stay the same in every theme. Only theme colours change: write them as `var(--color-...)` or Tailwind classes, never as hex. Canvas charts can't use `var()`, so `charts.ts` reads them with `cssColor` when a chart is built.
  - An inline script in `index.html` sets `data-theme` before the page draws, so a reload doesn't flash Sigma.
- Dense spacing, like Grafana, on Dashboard, Requests, and Logs. Normal spacing on Config and Settings.
- Models and Logs follow llama-swap's pages.
- **Requests page:**
  - A dense, full-width table with one row per request. In-flight requests are listed first and update live.
  - The prompt column shows about 20 characters, then an ellipsis.
  - The Notable column (after Prompt, on by default) shows up to four small chips, each a few characters or an icon. Chips about the answer, and images, are green; the rest are grey. Hovering or tapping a chip opens a popover. Five or more chips show three and `+N`.
  - The "⋯" menu next to Columns has a Colorful option, saved per browser (`requests_colorful`). With it on, chips take a color by kind (the `colors` map in `web/src/lib/notable.ts`): tool calls cyan, images green, JSON and schema blue, cache miss amber, the rest grey. The thinking chip shows the level asked for as four pips stacked in a column, lit from the bottom: 1 (low) to 4 (max). For effort `max` (above `xhigh`), the top pip glows: pink (`#ff7a9a`) with Colorful on, near-white with a glow in the chip's own colour with it off. The glow is three stacked `drop-shadow`s (1, 3 and 6 px), because one is too faint around a pip this small. It has no pips when the request named no level. With Colorful on, thinking stays violet and also gets more solid (text and background opacity, `thinkingStrength`) the higher the level asked for, in four even steps: text 60% to 100%, background 8% to 20%. Effort names map directly. Token budgets split at 2,048, 8,192 and 32,768. No level given is coloured as medium.
  - The token and speed columns sit under group headers. The "⋯" menu picks the grouping, saved per browser (`requests_grouping`): by phase (Prompt: tokens, t/s; Output: tokens, t/s), the default, or by kind (Tokens: in, out; Speed t/s: in, out). Its "Show units" option (`requests_units`) adds a dimmed `t` or `t/s` after each value.
  - The Source column (on by default) shows the client's short name and its address. Hovering shows the full User-Agent.
  - Columns can be dragged into another order, by their headers or in the Columns menu. The order is saved per browser (`requests_column_order`). Choosing a grouping puts the four token and speed columns back side by side. "Reset to default" resets the order and the column settings too.
  - Column settings: a column with settings has a gear next to it in the Columns menu, which opens its choices below it. They are saved per browser (`requests_column_settings`), and `columnSettings` in `Requests.svelte` lists them. The Model column chooses between the model's name (from the `name` in llama-swap's config) and its ID, defaulting to the name. A model without a name shows its ID either way. The model filter's list follows the same choice. `lib/modelNames.ts` finds a model by ID or alias.
  - Cache % is on by default in place of Cache, which is still in the column picker. A thin bar under each value shows the percent. With Colorful on, a prompt of 8,000 tokens or more turns amber under 10% cached, and a softer amber under 50%; other bars are green. With it off, bars are grey. Status is a default column, because failed and cancelled requests get no chip. Saved column choices carry a version (`requests_columns_version`), so new default columns can be added to them once: version 2 added Notable, version 3 added Status, and version 4 added Source.
  - Heatmap (`lib/heat.ts`): the number columns right of Cache are drawn dimmed, and values worse than usual are brighter, up to full strength. Text strength was chosen over a background colour. How much is dimmed is a slider in the "⋯" menu, from Off to 60%, 30% by default, saved per browser (`requests_heat`). It doesn't depend on Colorful. Each value is compared with the median of the finished requests listed: high is bad for token counts, TTFT, duration, energy, watts and J/tok, and low is bad for speeds. Speeds, TTFT, watts and J/tok compare within the same model. Prompt speed leaves out prompts more than half cached. A value up to 1.25 times worse than the median stays fully dimmed. It reaches full strength at a set factor per column (`full`): 1.5× for output speed and watts, 2× for prompt speed and J/tok, 4× for the rest. A group needs 5 values before it gets a median.
  - Clicking a row expands it in place. The expanded row shows:
    1. A header with the ID, status, model, and time, plus a "Copy as curl" button. A request in flight has a Cancel button (it asks first); a finished one has Retry, which opens the new request. A retry links to its original.
    2. Metric chips: TTFT, token counts, prompt and generation speed, duration, energy, joules per token, and model load time.
    3. A timing bar split into model load, prompt processing, and generation.
    4. Tabs: "Side by side" (the request messages next to the response), "Raw request", and "Raw response".
  - Images in a request (OpenAI, Responses API, and Anthropic forms, found by `lib/images.ts`) show as a row of thumbnails above the messages, as thumbnails in collapsed messages, and larger in open ones. Clicking any of them opens `lib/ImageViewer.svelte`: fitted to the window or at actual size, with the format, pixel size and file size, arrow keys between images, Download, and Open in new tab.
  - Each part of the expanded row has a copy icon (`lib/CopyButton.svelte`): each message, reasoning, tool call, the answer, the tool list, and the full request and raw response. Over plain HTTP the browser has no clipboard API, so it falls back to `execCommand('copy')`.
- **Dashboard:** a configurable grid that holds:
  - gauges and summary tiles
  - a shared-timeline stack as one wide widget, with in-flight, swaps, and energy-per-token panels beside it
  - a model table
  - per-model speed charts
- Desktop first. On a phone, the status bar, the Dashboard, and Requests must work well. The Dashboard is read-only on a phone, with widgets stacked in one column. Editing the dashboard layout and the config editor are desktop only.

### Stack

- Go backend. The Svelte frontend is built into the same single binary.
- One binary makes self-update simple: download, swap, restart.
- Frontend libraries:
  - Tailwind for styling, to match llama-swap's UI so copied components fit in.
  - uPlot for time-series charts.
  - gridstack.js for the dashboard grid.

## Conventions

- Use `micro` as the CLI editor in all instructions and scripts.
- Commands in the docs must work in bash and in zsh with oh-my-zsh:
  - Use shell functions, not command strings stored in variables. zsh doesn't split `$VAR` into separate words.
  - Give helpers long, specific names. oh-my-zsh defines many short aliases, such as `gr`.
