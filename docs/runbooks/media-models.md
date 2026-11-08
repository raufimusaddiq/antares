# Runbook — default image and video models

Admin → Assistant → **Assistant media** sets the model every account's assistant uses to generate images and videos
(`GET`/`PUT /api/admin/media-models`, audited as `media_models.set`). The choices come from Kenari's own model list
(`https://kenari.id/v1/models`): the models served on its `images` and `videos` endpoints that accept a text prompt
(an edit-only or clip-only model cannot be driven by the plugins). "Default" leaves the plugin's own pick
(`agnes-image-2-5-flash:free`, `veo-3.1-lite`).

How it applies:

1. The choice is stored on the tenant policy row (`tenant_tool_policy.image_model` / `video_model`, migration 0025), so
   the policy version changes with it and an account cannot become ready under a stale choice.
2. Saving puts every ready or failed account back through verification, like a tool policy save. Each run sends the
   models to the provisioning helper (`imageModel`, `videoModel`), whose policy step writes
   `image_gen.kenari-image.model` and `video_gen.kenari-video.model` into the profile's own `config.yaml`
   (an empty value is written too, so clearing the choice resets the profile).
3. The helper is deployed separately from the image: **a new helper build must be installed before the choice takes
   effect** (`deploy/verify-helper-revision.sh <sha>`), and the two plugins copied into `/opt/data/antares-plugins/`
   ([`media.md`](media.md), "Install"). An older helper rejects the new fields with 400, so verification fails visibly
   rather than silently ignoring the choice.

If Kenari's list cannot be read, the lists are empty and the screen says so: the saved choice may stay and the defaults
can be restored, but no new model can be chosen. Video needs the Video generation tool switched on in Assistant tools and
is billed per second by Kenari.
