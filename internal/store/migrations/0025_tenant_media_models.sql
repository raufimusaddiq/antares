-- The default image and video generation models an admin sets in Admin → Assistant media (docs/runbooks/media-models.md).
-- They live on the tenant policy row, so the same version check that stops an account becoming ready under a changed
-- tool policy stops it under changed models. '' means "the plugin's own default". A row made only to hold models has
-- toolsets = 'null' (no custom tool policy: the configured default applies).
ALTER TABLE tenant_tool_policy ADD COLUMN image_model TEXT NOT NULL DEFAULT '';
ALTER TABLE tenant_tool_policy ADD COLUMN video_model TEXT NOT NULL DEFAULT '';
