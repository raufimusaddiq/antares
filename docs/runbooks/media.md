# Media: what Hermes exposes and where Antares stops (issue #101, PRD §27.3)

Evidence: `docs/hermes/fixtures/capabilities.body.json`, `models.body.json`, `toolsets.body.json`
(deployed Hermes 0.22.2). Antares has no capability-discovery route yet (`GET /api/capabilities` is
the 501 placeholder), so **no UI preview or image-send control is shown**: nothing reports image input
as available, and an unconfigured feature is `unsupported`, not displayed.

| Item | Status | Evidence / what exists |
|---|---|---|
| MEDIA-EXT-001 upload part | built (API only) | `POST /api/assets` accepts png/jpeg/gif, header-decoded (magic + format must match the declared type), 8 MiB, 25 MP, 50 MiB per account, owner-bound id, `private, no-store`, `nosniff`. webp not accepted (no dimension check without a new dependency). |
| MEDIA-EXT-001 composer UI (thumbnail, progress, retry, camera) | not built | No consumer: images cannot be sent (next row). |
| MEDIA-EXT-002 vision / image send | **UPSTREAM_BLOCKED** | `/v1/models` lists only `hermes-agent` with no modality field; `features` has no vision or file-input flag; the only upload route, `/v1/artifacts/upload`, belongs to `browser_extension_control` (`enabled: false`, `max_bytes: 1`). The `vision` toolset (`vision_analyze`) is not proof the chat model accepts images. No verified input protocol, so none is invented. |
| MEDIA-EXT-003 generated image display/download | **UPSTREAM_BLOCKED** (display) | Existing owner-checked artifact routes cover downloads; no fixture shows a generated-image reference from `image_generate` to ingest. Owned asset storage is ready for it. |
| MEDIA-EXT-004 generated video | **UPSTREAM_BLOCKED** | No video toolset or job route in `toolsets.body.json` / `capabilities.body.json`. |
| MEDIA-EXT-005 audio/TTS playback | **UPSTREAM_BLOCKED** | `audio_api: false`, `tts` disabled (see `voice.md`). |
| MEDIA-EXT-006 media tool activity | **UPSTREAM_BLOCKED** | Nothing emits media outputs to show. |
| MEDIA-EXT-007 lazy media / range streaming | not built | No media renders yet; add with the first one, with §14 benchmarks. |

Tests: `TestImageStagingOwnerOnlyAndValidated` (user B gets the same 404 as for an unknown id on
GET and DELETE of A's image, A's copy survives, zero Hermes requests), `TestImageUploadRefusals`
(spoofed type, svg, webp, pixel and byte budgets, no rows left).
Re-probe `/v1/capabilities` and `/v1/models` when the owner enables vision/TTS; capture a fixture first.
