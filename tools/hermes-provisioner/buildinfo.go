package main

import "runtime/debug"

// revisionUnavailable is reported when the binary carries no VCS stamp (built
// outside a git checkout or with -buildvcs=false). It is never a fake sha.
const revisionUnavailable = "unavailable"

// buildRevision reads the commit the binary was built from out of the binary
// itself. This is the only source: a constant or ldflag could drift from it.
func buildRevision() (revision string, modified bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return revisionUnavailable, false
	}
	return revisionFrom(info)
}

func revisionFrom(info *debug.BuildInfo) (string, bool) {
	revision, modified := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			revision = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	if revision == "" {
		return revisionUnavailable, false
	}
	return revision, modified
}
