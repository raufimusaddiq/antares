# Multiplex listener diagnosis

Issue #15. Measured 2026-10-22 against Hermes 0.22.2, upstream
`d923a5eb487a17c0a68801bba9ef0f56c81040a8`. Source references below name files in
the deployed Hermes tree, not Antares. Container paths are retained where needed
to reproduce commands; deployment addresses, process IDs, model names and decorative
CLI output are omitted or replaced by labeled placeholders. No credential values
were captured in this report.

## Verdict

**Fixable through operator-owned Hermes configuration. No Antares application
workaround or upstream listener patch is needed for the failure reproduced here.**
The container's `API_SERVER_KEY` exists only in its process environment. The
default profile's `.env` and built secret scope do not contain that key. Once a
second profile makes the gateway multiplex, the scoped config reload deliberately
stops inheriting process credentials, including for `default`. The API platform
disappears before any attempt to bind its port.

The exact provisioning change is to supply **`API_SERVER_KEY` in the default
profile's `.env`**, using the existing runtime secret, then restart the gateway.
Each tenant needs its own different key in its own `.env`. This is an operator
step: Antares must not write Hermes files or expose credentials through its API.
Listener host/port/CORS can remain container environment settings. No
`gateway.profile_routes` change is involved.

The provisioning correction passed an in-memory config test, including port
propagation and refusal to inherit the process key into a secondary profile.
It was **not applied to the live profile**, and a corrected multiplex listener
and authenticated tenant routes were **not tested on the wire**. This report does
not assert that every subsequent tenancy requirement will pass.

**Phase 0 remains failed.** The filesystem-isolation failure in
[PHASE0-FINDINGS.md](PHASE0-FINDINGS.md) is unchanged. This diagnosis corrects that
report's suggestion that `gateway.profile_routes` controls HTTP prefix routing;
it does not establish multi-user safety.

## Exact code path

| Deployed source | Condition and consequence |
| --- | --- |
| `hermes_cli/gateway_multiplex_mode.py:124-168,228-258` | With no explicit flag, `implicit_multiplex_blocker()` returns `SINGLE_PROFILE_REASON` when `len(profiles_to_serve(multiplex=True, include_parked=True)) < 2`. With two eligible profiles and no migration blocker, `resolve_multiplex_mode()` sets `config.multiplex_profiles = True`. An explicit true also enables it. |
| `gateway/run.py:1831-1869` | `load_gateway_config_for_runner()` first loads unscoped config and resolves the mode. If multiplexing is false it returns that load. Otherwise it calls `set_multiplex_active(True)` and reloads under `_profile_runtime_scope(default_home)`. |
| `gateway/run.py:1772-1818`; `agent/secret_scope.py:390-418` | The scope comes from the profile `.env`, hydrated external secret sources and managed environment. It does not generally copy process credentials into the mapping. |
| `agent/secret_scope.py:94-110,174-246`; `gateway/config.py:186-193` | Multiplexing makes `serves_routed_profile()` true even for the default profile. A non-global key missing from an installed scope returns the caller's default instead of falling through to `os.environ`. `gateway.config._getenv()` uses that scoped reader. `API_SERVER_KEY` is deliberately absent from the global allowlist. |
| `gateway/config_env.py:306-314` | `_api_server()` reads `key = getenv("API_SERVER_KEY")`; **`if not _has_usable_api_server_key(key): return`**. Only after this condition does it enable the platform, store the key, and read port/host/CORS. With no YAML platform block and a missing scoped key, no API platform is added. |
| `gateway/run_startup.py:1173-1205,1437-1440` | Startup iterates enabled platforms, also rejecting a multiplex primary without its own usable credential. When the enabled-platform count is zero it logs `No messaging platforms enabled.` and continues running cron. There is no failed TCP bind in this case. |
| `gateway/platforms/api_server.py:4421-4468` | An adapter that does reach `connect()` separately requires a usable key of at least 16 characters. Setting only `enabled: true` cannot bypass credential validation. |

The relevant gate, verbatim from `gateway/config_env.py:306-314`:

```python
def _api_server(config: GatewayConfig) -> None:
    """Require a usable key: an unauthenticated adapter refuses to start and the reconnect watcher would spin."""
    key = getenv("API_SERVER_KEY")
    if not _has_usable_api_server_key(key):
        return
    extra = _enable_from_env(config, Platform.API_SERVER, pop_marker=True, warn=False).extra
    extra["key"] = key
    _csv_extras(extra, (("cors_origins", getenv("API_SERVER_CORS_ORIGINS")),))
    _env_extras(extra, (("port", "API_SERVER_PORT", _INT), ("host", "API_SERVER_HOST"), ("model_name", "API_SERVER_MODEL_NAME")))
```

The documented shared-listener topology is present in this build:

- `gateway/config.py:304-317` lists `api_server` and `webhook` in
  `SHARED_LISTENER_MIRROR_PLATFORMS`, with API mirror path `/v1`.
- `gateway/run_adapters.py:1287-1295` explicitly skips a second API adapter for a
  secondary profile. For API traffic it is the default adapter that registers
  native routes and their `/p/{profile}` mirrors
  (`gateway/platforms/api_server.py:4480-4495`). Secondary API servers do not
  need their own TCP listeners or their own adapter instances.
- `gateway/platforms/api_server.py:1647-1716` validates the requested profile and
  enters its runtime scope. `_expected_api_key()` at lines 1536-1552 reads the
  named profile's key and fails closed when missing. It does not authorize a
  tenant with the default key.
- `gateway/platforms/shared_ingress.py:56-75,111-125` publishes other secondary
  port-binding adapters' applications instead of binding another port; the
  default listener forwards their prefixed requests. This cannot provide an
  ingress when the default listener itself never started.
- `hermes_cli/container_boot.py:101-145` registers named s6 slots down unless
  standalone; `default` runs the multiplexer. Slot registration is consistent
  with the intended topology, not evidence of a competing listener.

## Live reproduction and restoration

The baseline config was copied privately on the host for byte comparison, never
committed or printed. The profile and all inspection commands ran as user
`hermes`. Only profile create/delete and the two authorized container restarts
mutated the live installation. No model, toolset, `.env` or YAML edits were made.

1. Establish the baseline:

   ```sh
   docker exec -u hermes hermes hermes profile list
   docker exec -u hermes hermes curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8642/health
   ```

   Observed: only `default`; HTTP `200`.

2. Create one empty, disposable profile:

   ```sh
   docker exec -u hermes hermes hermes profile create muxdiag15 --no-alias --no-skills
   ```

   Observed immediately afterward:

   ```text
   after_create_health=200
   /run/service/gateway-muxdiag15/down
   profiles= ['default', 'muxdiag15']
   implicit_blocker= None
   ```

   The last two lines came from `profiles_to_serve(multiplex=True,
   include_parked=True)` and `implicit_multiplex_blocker()` in a separate,
   read-only Python process. Creating the profile also registers its s6 slot
   (`hermes_cli/profiles.py:1435-1443,1920-1936`); these were not separated in a
   live experiment.

3. Restart, then wait for gateway startup to settle:

   ```sh
   docker restart hermes
   docker exec -u hermes hermes /command/s6-svstat /run/service/gateway-default
   docker exec -u hermes hermes /command/s6-svstat /run/service/gateway-muxdiag15
   docker exec -u hermes hermes curl -s -o /dev/null -w '%{http_code}\n' http://localhost:8642/health
   ```

   Boot reconciliation and supervisor output, with the process ID omitted:

   ```text
   reconcile: profile=default prior_state=running action=started
   reconcile: profile=muxdiag15 prior_state=None action=registered
   up (pid <omitted> pgid <omitted>) 26 seconds
   down (not started yet)
   settled_health=000
   ```

   Matching `logs/gateway.log` lines (container-local timestamps):

   ```text
   2026-10-22 12:58:48,306 INFO gateway.control_socket: Gateway control socket listening at /opt/data/gateway.sock
   2026-10-22 12:58:49,799 WARNING gateway.run: No messaging platforms enabled.
   2026-10-22 12:58:49,800 INFO gateway.run: Gateway will continue running for cron job execution.
   ```

   `hermes gateway status` simultaneously reported:

   ```text
   Gateway is running (supervised by s6 (container supervisor))
   ```

   `000` is curl's no-HTTP-response result, not an HTTP status. The gateway
   process and control socket survived while the API did not start.

4. Delete the probe and restore before any further live experiment:

   ```sh
   docker exec -u hermes hermes hermes profile delete muxdiag15 --yes
   docker restart hermes
   ```

   **Delete exited 1**, despite removing the profile. Relevant output:

   ```text
   Profile was deleted, but identity purge failed for /opt/data/profiles/muxdiag15/state.db: FileNotFoundError: Named profile home does not exist: /opt/data/profiles/muxdiag15. Create the profile explicitly before using it.
   Removed /opt/data/profiles/muxdiag15
   Profile 'muxdiag15' deleted.
   Error: Profile 'muxdiag15' was deleted, but its session/routing identity settlement is still pending - run: hermes profile purge-identity muxdiag15
   ```

   The final separator above replaces the CLI's typographic dash. Per the
   deployment runbook, live experiments stopped on that failure. The required
   recovery restart succeeded on its first attempt. Neither the suggested
   purge command nor manual filesystem cleanup was attempted. After restart,
   an empty `profiles/muxdiag15` directory existed without `SOUL.md`; it was
   not listed or servable. Identity settlement remains an owner follow-up.

## Environment question and isolated checks

Read-only inspection of the deployed configuration and real scope builder
returned only key-presence booleans, never secret values:

```text
process_key_present= True
profile_dotenv_key_present= False
built_scope_key_present= False
multiplex= False enabled_env= true port_env= 8642 scoped_key_present= True api_platform_enabled= True
multiplex= True enabled_env= true port_env= 8642 scoped_key_present= False api_platform_enabled= False
scope_gate_assertions=PASS
top_level_keys= ['agent', 'browser', 'model']
gateway.multiplex_profiles= None
multiplex_profiles= None
```

These assertions called `build_profile_secret_scope(default_home)`, installed
that mapping with `set_secret_scope()`, toggled `set_multiplex_active()`, then
called the deployed `_api_server(GatewayConfig())`. Mode, environment and scope
changes occurred only in that short-lived inspection process.

| Variable | What this deployed path does |
| --- | --- |
| `API_SERVER_ENABLED` | Classified global, so the scoped reader can see it. But `_api_server()` and the adapter do not consult it to enable the listener. Key presence is the env-enable gate. Changing this value alone cannot fix the failure. |
| `API_SERVER_PORT` | Classified global. `_api_server()` copies it to `extra.port` after the key gate passes; `listen_address()` also has a process-env fallback (`api_server.py:221-232`). The failed path returns before this copy. |
| `API_SERVER_KEY` | Profile scoped. Read by `_api_server()`, but the multiplex scope misses a process-only value. A usable value in the default scope restores API platform enablement. Secondary scopes must supply their own keys. |

A second in-memory check used a fabricated key, synthetic profile enumeration
and port `18642`, so the port result cannot be confused with the default `8642`.
It called the deployed mode resolver and config helper, with no gateway startup,
listener bind or profile/config write. Migration checks were stubbed clear for
the mode test; this proves the count branch, not the whole migration preflight.

```text
synthetic_profile_count= 1 multiplex= False
synthetic_profile_count= 2 multiplex= True
default_scope= missing enabled_env= true port_env= 18642 api_enabled= False
default_scope= provisioned enabled_env= true port_env= 18642 api_enabled= True
API_SERVER_ENABLED= true with_usable_scoped_key_api_enabled=True
API_SERVER_ENABLED= false with_usable_scoped_key_api_enabled=True
secondary_missing_key_does_not_inherit_process_key=True
in_memory_assertions=PASS; no profile/config writes or listeners
```

The core config regression check is reproducible without touching live profiles:

```sh
docker exec -i -u hermes -e PYTHONDONTWRITEBYTECODE=1 hermes python - <<'PY'
from unittest.mock import patch
from agent.secret_scope import (
    set_multiplex_active, set_secret_scope, reset_secret_scope,
)
from gateway.config import GatewayConfig, Platform
from gateway.config_env import _api_server

key = "29ea184f3c7590bd6fa2e850714dc3bb70cb09d85a2f7be158e08d60a334dc91"
with patch.dict("os.environ", {"API_SERVER_KEY": key, "API_SERVER_PORT": "18642"}):
    set_multiplex_active(True)
    try:
        for scope in ({}, {"API_SERVER_KEY": key}):
            token = set_secret_scope(scope)
            try:
                config = GatewayConfig()
                _api_server(config)
                api = config.platforms.get(Platform.API_SERVER)
                assert bool(api and api.enabled) == bool(scope)
                if api:
                    assert api.extra["port"] == 18642
                    assert api.extra["key"] == key
            finally:
                reset_secret_scope(token)
    finally:
        set_multiplex_active(False)
print("missing scope disables API; provisioned scope enables API: PASS")
PY
```

## Hypotheses and limits

| Hypothesis | Finding and evidence |
| --- | --- |
| A second profile immediately removes the running listener | Eliminated for this sequence: create and slot registration left health at 200. Restart with two eligible profiles produced the failure. The count branch is independently covered by the resolver assertions. |
| A registered named s6 slot starts a competing API listener | Eliminated for this reproduction: boot registered the named slot but left it down; only the default slot was up. Source explicitly mirrors API routes instead of starting a second API adapter. Creating a profile without registering its slot was not tested live. |
| `--replace` causes API configuration to disappear | It is present in the same default s6 run script in both healthy and failed boots. `gateway/run.py:5799-5806` constructs the runner/config before assigning `bool(replace)` to adapter lock-takeover authority; the config loader has no replace argument. A live foreground launch without this flag was not tested. |
| Listener settings are all ignored in multiplex mode | Eliminated: scoped reads retained enabled=true and port=8642, and the synthetic test propagated port=18642 once the scoped key was supplied. The credential, not all environment configuration, was missing. |
| `gateway.profile_routes` registers HTTP prefixes | Eliminated by the native mirror registration and prefix middleware cited above. No such setting was changed. |
| Enabling multiplex explicitly is the missing fix | The eligible two-profile resolver test already returns true with no flag. Explicit true still follows the same scoped reload and cannot supply a missing key. No live flag edit was tested. |

No upstream defect report is proposed for the listener failure: the evidence
supports missing profile-scoped credential provisioning. The separate delete
identity-settlement error is recorded for the owner, without attributing a root
cause that this ticket did not establish. No upstream issue was posted.

Report validation: the embedded regression command passed when extracted from
this document and executed. The repository's offline contract suite passed all
27 tests (`python3 -I -m unittest discover -s scripts -p 'test_*.py' -v`). A
presence-only check confirmed that the runtime API key does not occur in this
document; the staged diff passed `git diff --cached --check`.

## Restored-state proof

Recovery log lines, with listener address and model replaced:

```text
2026-10-22 12:59:38,017 INFO gateway.control_socket: Gateway control socket listening at /opt/data/gateway.sock
2026-10-22 12:59:38,639 INFO gateway.run: Connecting to api_server...
2026-10-22 12:59:38,668 INFO gateway.platforms.api_server: [Api_Server] API server listening on http://<bind-address>:8642 (model: <omitted>)
```

Recovery checks:

```text
restored_health= 200
profile list: default only (running)
config_restored_exit=0
servable_profiles= ['default']
probe_dir_exists= True probe_SOUL_exists= False
probe_children= []
s6 gateway slots: gateway-default only
```

`config_restored_exit=0` is the exit status of `cmp -s` between the private
pre-experiment snapshot and the restored file. An empty, unlisted profile
directory remains, and the delete command reported pending identity settlement.
Probe logs and CLI-managed deletion markers were not manually removed. The API
is healthy and the only live profile is `default`; further cleanup needs the
owner because the authorized delete operation failed.
