# Media contracts and remaining gaps (#101, PRD §27.3)

The 2026-10-24 probes used separate disposable profiles and two newly labelled synthetic media
TEST users. Synthetic identities exercise Antares authorization; they do not test Google OIDC.
Production stayed on `458820cee34c1b280e3eb3d5d76fe9241be2f2cf`. Phase 0 remains FAIL and
operation remains limited to trusted users.

## Verified transport

The deployed Hermes source accepts inline `image_url`/`input_image` content. A scoped
`POST /v1/runs` containing a text part and a PNG data URL returned `202`; polling reported a
failed run because the configured `custom` provider returned `HTTP 401: invalid key`.
A non-image data URL sent to `/v1/chat/completions` was refused with
`400 unsupported_content_type`. This establishes an input protocol, but does not prove that
the selected model can process an image.

Neither authenticated `/v1/models` nor `/api/model/options` advertised input modalities or
vision support. The `vision` toolset is enabled/configured; its `vision_analyze` tool still
is not a guarantee that the chat model receives inline images. Antares' capability discovery
exists; the earlier statement that `/api/capabilities` is a 501 placeholder was obsolete.
It does not currently report an image-send capability, so an image-send control remains hidden.

The actual `/v1/capabilities` response advertises browser-control artifact paths but reports
`browser_extension_control.enabled: false`. A scoped PNG upload returned
`404 browser_control_disabled`. The advertised limits are 10 MiB and a 300-second TTL,
not the earlier fixture's 1-byte limit. Source in `gateway/platforms/api_server.py` also
makes downloads one-shot and principal/transport scoped. These routes cannot be treated as
a generic durable generated-media gallery or enabled by Antares changing shared settings.
`audio_api` remains false; that flag alone says nothing about whether a TTS agent tool works.

Sanitized HTTP fixtures:

- [`t101-capabilities.body.json`](../hermes/fixtures/t101-capabilities.body.json)
- [`t101-inline-image-admission.body.json`](../hermes/fixtures/t101-inline-image-admission.body.json)
- [`t101-inline-image-result.body.json`](../hermes/fixtures/t101-inline-image-result.body.json)
- [`t101-invalid-image-input.body.json`](../hermes/fixtures/t101-invalid-image-input.body.json)
- [`t101-artifact-upload.body.json`](../hermes/fixtures/t101-artifact-upload.body.json)

## Acceptance status

| Requirement | Verified behavior | Remaining work |
|---|---|---|
| MEDIA-EXT-001 | Existing API stages png/jpeg/gif with magic/declared-format agreement, 8 MiB/25 MP limits, owner-bound IDs and private responses. Live spoofed MIME, SVG, byte and pixel refusals passed. | Composer thumbnail/progress/retry/camera UI is not built. It needs a verified usable selected-model input capability before images can be sent. |
| MEDIA-EXT-002 | Inline image run admission exists upstream. Model metadata supplies no vision guarantee; inference failed with provider 401. | Fix upstream authentication and establish selected-model vision support, then implement owner-resolved image input in Antares. No input is silently discarded. |
| MEDIA-EXT-003 | User B receives 404 on user A's staged image GET/DELETE; A retains access. Authenticated download matches the uploaded bytes and is `private, no-store` plus `nosniff`. | Generated-image ingestion/display/download replay remains unverified: provider authentication prevents generation, and browser artifacts are disabled and one-shot. Capture a real owned output reference first. |
| MEDIA-EXT-004 | No generated-video result or runnable video job was observed. | Upstream output/job contract and successful generation are required before progress, playback and retry UI. |
| MEDIA-EXT-005 | Existing private audio staging is covered by package tests; no successful generated audio result was captured. | TTS output contract and a supported transcription path remain unverified. Do not infer them from agent tool names or invent native audio routes. |
| MEDIA-EXT-006 | Existing tool-progress handling is tested; these failed model turns produced no media tool lifecycle. | Capture a successful media tool run before claiming media activity/output presentation. |
| MEDIA-EXT-007 | Media is not rendered, so there is no eager preview fetch. Existing staged asset download is an attachment response. | Media rendering, range streaming and §14 media benchmarks are not delivered. Add and measure them with the first verified media consumer. |

The protocol exists, while several model/output guarantees remain unverified. This report does
not close #101, claim complete multimedia support, or turn missing UI into a passed acceptance test.

## Commands and evidence

The authenticated helper `POST /provision` created one new random profile for the upstream probe.
Scoped calls: `GET /v1/capabilities`, `GET /v1/models`, `GET /api/model/options`,
`POST /v1/artifacts/upload`, `POST /v1/chat/completions`, `POST /v1/runs`, and
`GET /v1/runs/{id}`. Read source through `docker exec -u hermes hermes`; no shared config changed.

The two separate media TEST users each obtained their own backend-selected profile through the
existing tenant pipeline. Live Antares calls used `POST /api/assets`, owned/foreign/unauthenticated
`GET /api/assets/{id}` and owned/foreign `DELETE /api/assets/{id}`. Thirteen checks passed;
the created asset was deleted through its owner's API after retaining evidence. Credentials
and raw responses remain protected:

- `/var/backups/antares/probe-101-20261024T101700Z`
- `/var/backups/antares/media-boundary-20261024T101751Z`

Runnable regressions:

```sh
go test ./internal/chat -run 'TestImageStagingOwnerOnlyAndValidated|TestImageUploadRefusals|TestAssetOwnerOnly' -count=1
```

`TestImageStagingOwnerOnlyAndValidated` proves B cannot GET/DELETE A's image, A keeps access,
and image staging sends zero Hermes requests. `TestImageUploadRefusals` covers spoofed MIME,
SVG/WebP refusal, byte/pixel limits and no leftover rows.
