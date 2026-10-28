# Media contracts and remaining gaps (#101, PRD §27.3)

Updated by the #101 finish branch: sections marked **(finish)** supersede the earlier 2026-10-24 text.

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

## Model capability source (finish)

Per the owner (#101 comment, 2026-10-24): the model catalogue and capabilities come from **kenari.id**, the
provider Hermes is configured with, not from Hermes' model inventory. `GET https://kenari.id/v1/models` is public
and unauthenticated; it publishes `modalities.input` / `modalities.output` per model (read 2026-10-24: 111 models,
combinations such as `[text,image,file]→[text]`, `[text]→[image]`, `[text]→[audio]`, `[text,image]→[video]`).
`deepseek-v4-1-flash`, the model Hermes runs today, lists `image` input. A trimmed real copy is
[`internal/kenari/testdata/models.json`](../../internal/kenari/testdata/models.json).

- `internal/kenari` reads it with no credential, no cookie and no redirects, caches 10 minutes, serves a last good answer
  for at most an hour during an outage, then fails closed. A model it does not list is *unknown*, never "text only".
- Execution stays Hermes. The model checked is the model run: for an image turn Antares resolves the chat's saved
  choice (else the profile default Hermes reports, which says *which* model, not what it can do), checks that id in the
  catalogue, and sends that same provider/model explicitly in the run body. `TestImageSendUsesCheckedModelAndInlineBytes`
  and `TestImageSendChecksTheChatsChosenModelNotTheDefault` assert the id on the wire.
- Fail closed, visibly: text-only model, unlisted model, catalogue unreachable or not configured → `GET /api/chats/{id}/input`
  reports `available:false` with a reason, the composer hides the picker and refuses a pasted/dropped image with that
  reason, and `POST …/messages` with `imageAssetIds` answers `422 IMAGE_INPUT_UNAVAILABLE`. An image is never dropped to
  let a text-only send through.
- Deployment note: the Antares container needs outbound HTTPS to `kenari.id`. Without it image input stays off
  (`CATALOGUE_UNAVAILABLE`); nothing else changes. No new configuration was added: the URL is a constant.

## Verified image transport (finish)

Read from the deployed container's source, read-only (`docker exec -u hermes hermes …`, no request made):
`POST /v1/runs` takes `input` as a string or a messages list and uses the last message's `content`
(`gateway/platforms/api_server_runs.py` `_run_user_message`, line 670). Unlike `/v1/chat/completions`
(`_normalize_multimodal_content`, `api_server.py:503`) the runs path does **not** normalise content parts, so Antares sends the
canonical parts the agent pipeline understands: `{"type":"text"}` and `{"type":"image_url","image_url":{"url":"data:image/…;base64,…"}}`.
`agent.run_conversation` accepts a list `user_message` (`agent/conversation_loop.py:167`). The whole request is capped at
10,000,000 bytes (`MAX_REQUEST_BYTES`, `api_server.py:235`); base64 adds a third, so Antares allows 4 images and 6 MiB of
image bytes per message. The history read (`inline_images=false`, `hermes_cli/web_routers/sessions.py:690`) renders each image as `[image]`. That marker is
**not** used to find a turn. A thumbnail is linked only to a transcript turn that verifies: the run reached Hermes (has a Hermes
run id; a run refused before admission is never a candidate), the turn sits at the same position among the chat's user turns as
the run does among the chat's accepted runs, and the turn's text equals the run's own text (salted digest stored at send,
`turn_digest`) followed by exactly as many `[image]` placeholders as images sent. Any miss (steered or compacted history, rewritten
text, failed admission, literal `[image]` typed by the user) leaves the turn unlinked. "Try again" re-sends a turn's text and
images together from that turn of the chat on screen; an unlinked or since-deleted image turn is not re-sent without its images,
its text goes back into the box instead.

**Not verified live**: that the selected model actually *receives and describes* the image through `/v1/runs`. That needs a
credentialed run, which this development authorization excludes. See "Open" below.

## Acceptance status (finish)

Status words follow PRD §27.3's table: IMPLEMENTED / TESTED / UPSTREAM_BLOCKED / OPEN.

| Requirement | Status | Evidence |
|---|---|---|
| MEDIA-EXT-001 | IMPLEMENTED, TESTED | Paste, drop, file picker, and a camera button on coarse-pointer devices; chip with name/type/size/thumbnail/progress/retry/remove (`web/src/ImageAttachments.tsx`, `ChatImages.test.tsx`, `ImageAttachments.test.tsx`). Magic bytes, spoofed MIME, SVG/WebP refusal, 8 MiB/25 MP limits, owner-bound ids, `private, no-store`: existing `TestImageStagingOwnerOnlyAndValidated`, `TestImageUploadRefusals`. Real camera capture is browser/device dependent and was not run on a device. |
| MEDIA-EXT-002 | IMPLEMENTED, TESTED (Antares side); live model reception OPEN | Kenari-sourced capability, pinned model id, refusal cases: `TestImageSendUsesCheckedModelAndInlineBytes`, `TestImageSendRefusedWhenVisionIsNotConfirmed`, `TestHermesInventoryDoesNotDecideVision`, `TestCreateRunSendsImagesInline`, `ChatImages.test.tsx`. |
| MEDIA-EXT-003 | User-sent images: IMPLEMENTED, TESTED. Generated images: IMPLEMENTED, TESTED (#168) | Thumbnail, lightbox, download, history replay while the asset exists, "no longer available" after deletion: `TestHistoryLinksOwnImagesAndSurvivesDeletion`, `TestAssetPreviewHeadersAndRange`, `ImageAttachments.test.tsx`. Cross-owner: `TestImageCrossOwnerDenied` (B cannot send, preview or delete A's image; same 404 as an unknown id). Generated: "Generated media" below (`TestGeneratedMediaIsServedOnlyToTheUserItWasIssuedTo`). |
| MEDIA-EXT-004 | UPSTREAM_BLOCKED | No generated-video result or async job contract has been observed; video output goes through the same blocked delivery as images. |
| MEDIA-EXT-005 | PARTIAL | Owned-audio player (`OwnedAudio`: native controls, `preload="none"`, Range-capable source, download) is built and tested; nothing feeds it generated audio (blocked below). Voice input (Recorder) is unchanged. TTS tool output and a native `/v1/audio/speech` are not conflated and neither is wired. |
| MEDIA-EXT-006 | IMPLEMENTED, TESTED | The history API seals each `MEDIA:<host path>` into an opaque id bound to the user (`internal/chat/genmedia.go`, `TestMediaTagsAreSealedAndNeverCarryThePath`); a streaming reply's tags are taken out of the text. No host path or profile name is in the DOM, a URL or a request (`ChatImages.test.tsx`). |
| MEDIA-EXT-007 | PARTIAL | Lazy thumbnails, on-demand audio, `Range` support via `http.ServeContent` (`TestAssetPreviewHeadersAndRange`). §14 media benchmarks were not run. |

## Generated media: the earlier blocker (history; delivered by #168, see "Generated media" below)

Generated images and audio are returned by Hermes' tools as `MEDIA:<path>` where `<path>` is a file on the Hermes host
(`tools/tts_tool.py:358`). Hermes resolves those tags to inline data URLs only in `/v1/chat/completions` and
`/v1/responses` (`_resolve_media_to_data_urls`, called from `api_server_openai_routes.py:980,1179,1422`, images only,
≤ 5 MiB). **`/v1/runs`, the route Antares uses, never calls it**, so the tag reaches Antares unresolved. Antares may not
read Hermes' data directories (AGENTS.md), and the artifact routes are one-shot, principal-scoped and disabled
(`404 browser_control_disabled`, above). Nothing was invented around this.

Smallest change that unblocks it, all on the Hermes side, none in Antares:
apply the same tag-to-bytes resolution (or an owner-scoped, durable media download route) to the `/v1/runs` final output,
transcript and events. A config-only alternative does not exist: enabling `browser_extension_control` exposes browser control,
which §9A.6 forbids enabling automatically, and its downloads are one-shot.

## Open

- Live vision proof: one credentialed run through a disposable scoped profile with `internal/chat/testdata/scene-320x240.png`
  against `deepseek-v4-1-flash`, confirming the answer describes the picture. Excluded by this authorization; needs the
  owner's go-ahead.
- Generated image/audio/video display (blocked above), TTS/transcription contracts, §14 media benchmarks.
- Camera capture on a real phone; real-browser paste/drag behaviour (jsdom only here).

## Commands and evidence (2026-10-24 probes)

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

## Generated media (#168, 2026-10-28)

Hermes' own tools generate the media; Antares only shows it.

- **Images.** Hermes' bundled `openai` image backend does not work against Kenari: Kenari answers with a `data:` URL
  unless `response_format: "b64_json"` is sent, and Hermes refuses to fetch `data:` URLs. `deploy/hermes-plugins/kenari-image`
  is a small Hermes image backend that asks for `b64_json` on the profile's own endpoint (`OPENAI_BASE_URL`/`OPENAI_API_KEY`,
  which every tenant profile already has) and saves the image to the profile's cache like any backend. Default model
  `agnes-image-2-5-flash:free`; set `image_gen.kenari-image.model` in a profile to change it.
- **Install** (once per Hermes data volume, and after changing the plugin): copy it to `/opt/data/antares-plugins/`:

      docker cp deploy/hermes-plugins/kenari-image hermes:/opt/data/antares-plugins/
      docker exec -u 0 hermes chown -R hermes:hermes /opt/data/antares-plugins

  The provisioner's policy step (every provision and rotate) copies it into the profile's `plugins/` and sets
  `plugins.enabled: [kenari-image]` and `image_gen.provider: kenari-image`. Existing profiles get it on their next
  rotate (admin `tenant-verify`). Hermes loads plugins per profile when the gateway first serves it, so restart the
  Hermes container after installing or changing the plugin.
- **Delivery.** A reply names a generated file as `MEDIA:<path>` in the profile's cache. The history API replaces each
  tag with `MEDIA:<kind>-<id>`, the path sealed with the server's key and bound to the user, so no path or profile name
  reaches the browser. The web app shows the file under the reply (image with a lightbox, audio and video with native
  controls and seeking) from `GET /api/media?id=…`, rate-limited with the other file reads. Antares opens the id for
  that user only and asks the provisioner's `POST /media` with the user's **own** profile; the provisioner serves only
  `/opt/data/profiles/<that profile>/cache/` (checked again inside the container after resolving symlinks), media
  extensions only, at most 25 MiB. A refused or missing file is a 404, a helper that cannot answer a 502. A live
  (streaming) reply shows its text only; the media appears with the turn from history.
- **Single trusted user.** Hermes' media tools can load local image paths, including another profile's (#161). Fine
  while Antares has one trusted user; disable `vision`/`image_gen` for tenants or confine the resolver upstream before
  onboarding a second, untrusted one.
