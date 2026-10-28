-- The tenant toolset policy an admin sets in Admin → Assistant tools (docs/runbooks/tenant-toolsets.md). One row at
-- most: toolsets is a JSON array of names from hermes.OfferedToolsets. With no row, ANTARES_TENANT_ALLOWED_TOOLSETS
-- (or its default) applies. It is policy, not user content.
CREATE TABLE tenant_tool_policy (
  id         INTEGER PRIMARY KEY CHECK (id = 1),
  toolsets   TEXT NOT NULL,
  updated_by TEXT NOT NULL REFERENCES users(id),
  updated_at TEXT NOT NULL
);
