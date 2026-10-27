# Runbook — model picker, context and quota (issues #100, #115, PRD §27.1 `LLM-*`, §27.2 `CTX-*`)

## Source of truth (#115, supersedes the Hermes-inventory wording below)

Per the owner (#115 comment, 2026-10-25): the catalogue and model metadata come from **Kenari**
(`internal/kenari`, the provider Hermes runs on), not from Hermes' inventory. Hermes is only the transport.

| Question | Answer from | Where |
|---|---|---|
| Which models exist, modalities, context window, price, reasoning levels, sunset | Kenari `GET /v1/models` (public) | `kenari.Catalogue.List/Get`, `buildModels` |
| Which provider slug a run names | Hermes `provider` of the caller's own profile, usable only if `authenticated` | `transport` in `internal/chat/models.go` |
| What the run receives | `POST /v1/runs` body `provider`, `model`, `model_options.reasoning.effort` | `runBody`, `TestModelChoiceIsPerChatValidatedAndSent` asserts it on the wire |
| Quota | **Not shown to users (#146).** Hermes' `usage.windows` describe the one shared provider account; the picker no longer carries them. The authoritative account figures are the admin-only `GET /api/admin/kenari-usage` | `docs/runbooks/kenari-usage.md` |

A model Hermes' inventory does not list is **not** treated as a model Kenari lacks, and the reverse holds. The
picker never falls back to Hermes' list: a catalogue that cannot be read is `503 CATALOGUE_UNAVAILABLE`.

Endpoints: `GET/PUT /api/chats/{id}/settings` (`PUT …/model` is the same handler), `GET/PUT /api/me/model-visibility`
(replace the caller's hidden set; `[]` reveals everything), `GET/PUT /api/admin/model-policy` (global block list, audited).
Selection rules: the model must be a Kenari `chat` endpoint model, not past `sunset_at`, not on the block list; the
Hermes provider must be usable (`422 MODEL_TRANSPORT_UNAVAILABLE` otherwise); a client-supplied `provider` other than the
resolved one is refused; `reasoningEffort` must be a level Kenari publishes for the model **and** Hermes accepts
(`_REASONING_EFFORTS`, `api_server.py:291`), else `422 OPTION_UNSUPPORTED`; `serviceTier` is always refused. A chat whose
saved model is later blocked or delisted answers `422 MODEL_UNAVAILABLE` on send (nothing is sent, nothing is substituted);
`GET …/settings` reports `state` `AVAILABLE|BLOCKED|NOT_LISTED|UNKNOWN`. Hiding is a per-account view preference only: no Hermes call.

### UPSTREAM_BLOCKED (#115)
| Item | Status | Evidence |
|---|---|---|
| Service tier (`priority`) | **UPSTREAM_BLOCKED (Kenari)** | No Kenari catalogue field names a tier (`internal/kenari/testdata/models.json` carries none). Hermes' per-model `fast` flag describes Hermes' own providers, so it is not evidence for a Kenari model. Hermes does accept `model_options.service_tier` (`api_server.py:1940-1960`); the value is refused until Kenari publishes one. |
| Reasoning level `none` | **UPSTREAM_BLOCKED (Hermes ladder)** | Kenari lists `none` for some models; Hermes' effort ladder has no `none`. Dropped, not mapped. Hermes also has `reasoning.enabled`; no Kenari field says it is honoured, so Antares does not send `enabled:false`. |
| Effort levels for models Kenari marks `reasoning` without `supported_reasoning_levels` (e.g. `gemma-4-31b-it`) | **Unknown** | No levels are offered; the UI says they are not published. |
| Whether Kenari honours `provider`/`model`/effort sent by Hermes end to end | **Not probed** | A credentialed run is the owner's (AGENTS.md); the contract is read from Hermes source and asserted on the wire at the Antares→Hermes boundary only. |
| Admin UI for the block list | Not built | API only (`/api/admin/model-policy`). |
| Prices | Kenari's currency/unit, unconverted | `micro_idr_per_1m_tokens`; shown "Price (Kenari)". Not a bill. |

---


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
| `LLM-006` context window, cache price per model | **Blocked** | Neither is in the inventory (`inventory.py` has no `context_length`/`context_window`; `pricing.cache` is often null). Shown "Unavailable". Input/output modalities are not in the payload either; the owner designated the Kenari catalogue as their source (`docs/runbooks/media.md`). |
| `CTX-001` context % | **UPSTREAM_BLOCKED** | Hermes reports no per-call prompt size; run `usage` is a per-run sum (`api_server_runs.py:88-92`) and sessions carry cumulative input/output only (`api_server.py:2938`). Re-probed for #165 on image `sha256:9c5561579f3f…`. `GET /api/chats/{id}/usage` says so explicitly: `context.occupancy: "UNKNOWN"`, `carriedTokens: null` (never 0, never estimated from the lifetime total). The client reads a carried count only with `occupancy: "REPORTED"`. The UI keeps three lines apart: "Used in this chat, all replies" (lifetime), "Model context window" (provider-published size), "Context in use now" (— unknown). |
| `CTX-002` compaction indicator, manual compact | **UPSTREAM_BLOCKED** | No compress/compact route in the route table (`api_server.py:1646-1692`, `api_server_runs.py:243`) and no compaction field on run status or events; compression is visible only as a session-id rotation (`api_server.py:4134-4143`). No command is invented. `GET /api/capabilities` lists `compaction` as always unsupported with that reason, so no compact button or "nearly full" alert can appear without an upstream route (#165). |
| `CTX-003` reasoning tokens, cost | **Blocked** | `_USAGE_FIELDS` (`api_server_runs.py:88-92`) has input/output/total/cache read/cache write only. Estimated cost stays #81's. |
| `CTX-004` quota | **Superseded by #146** | Quota is the shared provider account's, so it is no longer shown to every user. Admin-only authoritative read from Kenari: `docs/runbooks/kenari-usage.md`. |
| `CTX-005` budget vs hard limit | Already built in #93 | unchanged |
| `LLM-005` hide/show, admin allow/deny policy | **Built in #115** | `model_hidden`, `model_blocked` (migration 0016); see above. |
| `LLM-008` OpenUI toggle | **Not in this slice** | separate ticket (`docs/runbooks/openui.md`). |
| `GET /api/capabilities` | **Not built** | nothing in this slice reads it; stays the 501 placeholder. |

## Relation to capability discovery (#117, merged)
Both read `GET /api/model/options`; they share one resolution. `Client.discoveryDoc` reads
`/v1/capabilities` once per call and `caps.route("model_options")` yields the advertised path, behind
the `model_options` flag (`internal/hermes/discovery.go`). Discovery asks for `include_unconfigured=false`
to decide whether models are offered at all; the picker (`internal/hermes/models.go`, `ModelCatalog`)
asks for `include_unconfigured=true` and keeps `capabilities`, `pricing` and `usage.windows`, which
discovery drops. A Hermes that does not enable or advertise the route gets no request to a guessed
path (501 `CAPABILITY_UNAVAILABLE`). The UI renders the picker only if `offers(inventory, "models")`
(`GET /api/capabilities`) is true. Tests reuse the captured `docs/hermes/fixtures/capabilities.body.json`.
The deployed container was re-checked on 2026-10-24 (same image digest, started 2026-10-23): the cited
lines are unchanged. No API call was made: that needs a tenant key, which AGENTS.md reserves.
