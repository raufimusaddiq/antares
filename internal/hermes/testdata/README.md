`skills.body.json` and `model-options.body.json` follow the response shapes read from the Hermes
source (`gateway/platforms/api_server.py` `_handle_skills`, `_handle_model_options`;
`hermes_cli/inventory.py` `build_model_options_payload`). They were **not captured on the wire**
(that needs the owner's key; see docs/hermes/PHASE0-FINDINGS.md §2). Every value is fabricated.
The capabilities and toolsets tests use the real captures in `docs/hermes/fixtures/`.
