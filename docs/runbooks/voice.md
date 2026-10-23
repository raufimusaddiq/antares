# Voice: what Hermes exposes and where Antares stops (issue #82, PRD §21.4)

| Part | Status | Where it runs |
|---|---|---|
| Record, timer, 5 min ceiling, mic errors, preview, discard | built | browser only (`web/src/Recorder.tsx`); nothing is uploaded |
| Dictation (audio → composer text) | **BLOCKED_BY_UPSTREAM** | Deployed Hermes 0.22.2: `audio_api: false`, `stt` and `tts` toolsets disabled, no transcription route. Antares will not call a speech provider itself. Nothing is inserted into the composer and nothing is auto-sent. |
| Audio attachment to a chat | not built | `/v1/capabilities` lists no file-input route, so an uploaded recording could not reach Hermes. |
| Staging a recording server-side | built, explicit only | `POST/GET/DELETE /api/assets`, owner-bound, audio only, magic-byte checked, 10 MiB each, 50 MiB per account, stored in SQLite so a refused upload leaves no row. No UI uploads yet: there is no consumer. |
| Playing assistant audio | not built | Hermes returns no audio artifact reference on the deployed version (tts disabled). Playback of a recording the user just made is client-side. When Hermes returns audio, it must be ingested through an owned asset (MEDIA-002), not played from a provider URL. |

Re-probe when the owner enables the `stt`/`tts` toolsets: capture a fixture before building on it.

## Retention of staged audio

Staged recordings do **not** expire and there is no cleanup job: they are deleted only by their
owner with `DELETE /api/assets/{assetId}` (204; someone else's id is a 404). No UI calls it yet, so
an account that fills its 50 MiB (`STORAGE_QUOTA`) can only recover through that API call. Nothing
uploads from the UI today, so this cannot happen from the app. When a UI that uploads is added, it
must ship with the delete control. Reads (`GET`) share the per-account artifact rate limit.
