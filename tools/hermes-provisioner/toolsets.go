package main

import (
	"encoding/json"
	"strings"
)

// disabledToolsets is every toolset a new tenant profile must have off: the
// floor that crosses the profile boundary (hermes.DeniedToolsets in the main
// module, docs/runbooks/tenant-toolsets.md) plus every other toolset the
// deployed Hermes (v0.22.2) reports that is not in the allowed default
// (web, vision, image_gen, skills, memory, todo, clarify). This module cannot
// import the main one; TestDisabledToolsetsCoverTheFloor in the main module
// keeps the two in step.
var disabledToolsets = []string{
	// boundary-crossing floor
	"terminal", "file", "code_execution", "delegation", "cronjob",
	"session_search", "connections", "browser",
	// not needed for v1 chat
	"video", "video_gen", "x_search", "tts", "stt", "kanban", "context_engine",
	"discord", "discord_admin", "yuanbao", "computer_use", "a2a",
}

// toolsetsConfigKey is where Hermes persists a per-profile suppression list.
// agent.disabled_toolsets is applied last in Hermes' toolset resolution, over
// every platform list, so it holds for the api_server platform.
const toolsetsConfigKey = "agent.disabled_toolsets"

// disabledToolsetsValue is the JSON array `hermes config set` parses.
func disabledToolsetsValue() string {
	b, _ := json.Marshal(disabledToolsets)
	return strings.TrimSpace(string(b))
}
