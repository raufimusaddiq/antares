package main

import (
	"encoding/json"
	"errors"
	"strings"
)

// The tenant toolset policy is operator configuration, not a constant:
// PROVISIONER_ALLOWED_TOOLSETS (comma separated; default defaultAllowed) names
// the toolsets a tenant profile keeps, and everything else Hermes knows
// (knownToolsets) is written to the profile's agent.disabled_toolsets. Set the
// same value as ANTARES_TENANT_ALLOWED_TOOLSETS in Antares: if the provisioner
// allows more than Antares does, verification fails toolset_policy.
//
// This module cannot import the main one, so the lists below are mirrored and
// pinned by TestProvisionerToolsetListsMatchAntares in the main module.

// knownToolsets is every toolset the deployed Hermes (v0.22.2) reports from
// /v1/toolsets. Hermes accepts any name in agent.disabled_toolsets (measured,
// docs/runbooks/tenant-toolsets.md), so a newer build's extra toolsets are
// the one thing this list cannot cover: add them here when they appear.
var knownToolsets = []string{
	"web", "browser", "terminal", "file", "code_execution", "vision", "video",
	"image_gen", "video_gen", "x_search", "tts", "stt", "skills", "todo",
	"kanban", "memory", "context_engine", "session_search", "connections",
	"clarify", "delegation", "cronjob", "discord", "discord_admin", "yuanbao",
	"computer_use", "a2a",
}

// floorToolsets cross the profile boundary and can never be allowed
// (hermes.DeniedToolsets, docs/runbooks/tenant-toolsets.md).
var floorToolsets = []string{
	"terminal", "file", "code_execution", "delegation", "cronjob",
	"session_search", "connections", "browser",
}

// defaultAllowed is the documented default tenant set (hermes.DefaultAllowedToolsets).
var defaultAllowed = []string{"web", "vision", "image_gen", "skills", "memory", "todo", "clarify"}

// toolsetsConfigKey is where Hermes persists a per-profile suppression list.
// agent.disabled_toolsets is applied last in Hermes' toolset resolution, over
// every platform list, so it holds for the api_server platform.
const toolsetsConfigKey = "agent.disabled_toolsets"

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// disabledFor returns knownToolsets minus allowed, in known order. It refuses
// an allowed set that includes a floor toolset or a name Hermes does not
// report, so a typo cannot silently leave a toolset on.
func disabledFor(allowed []string) ([]string, error) {
	if len(allowed) == 0 {
		return nil, errors.New("allowed toolsets must not be empty")
	}
	for _, n := range allowed {
		if contains(floorToolsets, n) {
			return nil, errors.New("toolset " + n + " crosses the profile boundary and cannot be allowed")
		}
		if !contains(knownToolsets, n) {
			return nil, errors.New("toolset " + n + " is not a known toolset")
		}
	}
	var out []string
	for _, n := range knownToolsets {
		if !contains(allowed, n) {
			out = append(out, n)
		}
	}
	return out, nil
}

// policyFromEnv parses PROVISIONER_ALLOWED_TOOLSETS; empty means the default.
func policyFromEnv(raw string) ([]string, error) {
	allowed := defaultAllowed
	if strings.TrimSpace(raw) != "" {
		allowed = nil
		for _, n := range strings.Split(raw, ",") {
			if n = strings.TrimSpace(n); n != "" {
				allowed = append(allowed, n)
			}
		}
	}
	return disabledFor(allowed)
}

// toolsetsValue is the JSON array `hermes config set` parses.
func toolsetsValue(disabled []string) string {
	b, _ := json.Marshal(disabled)
	return string(b)
}
