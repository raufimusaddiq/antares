# Runbook — model picker, context and quota (issue #100, PRD §27.1 `LLM-*`, §27.2 `CTX-*`)

Read from the deployed Hermes container's source (`nousresearch/hermes-agent:latest`, read-only
`grep`/`sed`, 2026-10-24). Paths are under `/opt/hermes`. Not called on the wire: a model call or a
profile's real catalog is the owner's. Re-check when Hermes is upgraded.

| Antares | Hermes route | Evidence |
|---|---|---|
| `GET /api/models` | `GET /api/model/options?include_unconfigured=true[&refresh=true]` (per-profile route `/p/<slug>/…`, tenant key) | route row `gateway/platforms/api_server.py:1654`; handler `:2434-2451`; advertised `features.model_options` (`docs/hermes/fixtures/capabilities.body.json`), `endpoints.model_options` (`api_server.py:81`) |
| catalog shape | `{model, provider, providers:[{slug,name,is_current,models[],authenticated,warning,capabilities{model:{fast,reasoning}},pricing{model:{input,output,cache,free}},limit,usage{windows[]}}]}` | `hermes_cli/inventory.py:227-256` (payload), `:842-855` (`authenticated`/`warning`), `:542-590` (`capabilities`), `:906-986` (`pricing`), `:287-289` (`limit`), `:292-356` (`usage.windows`: `label`, `used_percent`, `resets_at`, `scope`, `:309-315`) |
| `PUT /api/chats/{id}/model` → on each run | `POST /v1/runs` body `provider`, `model`, `model_options{reasoning{enabled,effort}, service_tier}` | route `gateway/platforms/api_server_runs.py:243`; body read by `_request_agent_overrides` `api_server.py:335-357`; options decoded `api_server_request_options.py` (`_request_reasoning_config`, `_request_service_tier`) and `api_server.py:1940-1960`; effort ladder `_REASONING_EFFORTS` `api_server.py:291` |

The choice is stored per chat (`chats.model_provider/model_id/reasoning_effort/service_tier`, NULL =
the profile default) and sent on every run of that chat. A change applies from the next run and
rewrites nothing earlier. `POST /api/sessions/{id}/model` (`api_server.py:99`, `:3741`) is **not**
used: it is a browser-control model *lock* that persists a session lock; a per-run body is enough
and locks nothing.

## Rules the code enforces
- Selection is validated against a fresh inventory for the caller's own profile: provider
  `authenticated`, model listed, `reasoning` only if `capabilities.reasoning` is true, `priority`
  only if `capabilities.fast` is true. Anything else is 422 `MODEL_UNAVAILABLE`, nothing saved.
- Unreported = `null` = shown "Unavailable" (price, reasoning, tier, context window, quota).

## Upstream-blocked / not built
| Item | Status | Why (evidence) |
|---|---|---|
| `LLM-003` per-model effort list | **Partly blocked** | Hermes omits `supported_efforts` on purpose (`inventory.py:545`); only the accepted ladder (`api_server.py:291`) is known. Antares offers the ladder only for models listing `reasoning:true` and says the provider may clamp. |
| `LLM-006` context window, cache price per model | **Blocked** | Neither is in the inventory (`inventory.py` has no `context_length`/`context_window`; `pricing.cache` is often null). Shown "Unavailable". Input/output modalities are not in the payload either. |
| `CTX-001` context % | **Blocked** | Hermes reports no per-call prompt size or model context length; run `usage` is a per-run sum (`api_server_runs.py:88-92`). Percentage stays omitted (existing #81 behaviour: `carriedTokens` null). |
| `CTX-002` compaction indicator, manual compact | **Blocked** | No compress/compact route in the route table (`api_server.py:1646-1692`, `api_server_runs.py:243`) and no compaction field on run status or events; compression is visible only as a session-id rotation (`api_server.py:4134-4143`). No command is invented. |
| `CTX-003` reasoning tokens, cost | **Blocked** | `_USAGE_FIELDS` (`api_server_runs.py:88-92`) has input/output/total/cache read/cache write only. Estimated cost stays #81's. |
| `CTX-004` quota | **Built, partial** | Shown from `usage.windows`/`limit` when Hermes sends them; otherwise `PROVIDER_USAGE_UNAVAILABLE`. Multi-credential pools report `usage.accounts` (`inventory.py:356`), which is *not* shown: one account's quota is not the user's. |
| `CTX-005` budget vs hard limit | Already built in #93 | unchanged |
| `LLM-005` hide/show, admin allow/deny policy | **Not built here** | Antares-side storage/admin work, not a Hermes limit; follow-up. |
| `LLM-008` OpenUI toggle | **Not in this slice** | separate ticket (`docs/runbooks/openui.md`). |
| `GET /api/capabilities` | **Not built** | nothing in this slice reads it; stays the 501 placeholder. |
