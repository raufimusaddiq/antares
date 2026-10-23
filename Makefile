# Targets for the checks that are easy to forget the flags for.

# Bounded on purpose: CI never runs an open-ended fuzz. Raise it locally to hunt.
FUZZTIME ?= 20s

THREAT_RUN := Threat|OwnershipProperty|OwnershipOverHTTP|SSERecovery|EveryChatRoute|IDKindConfusion

.PHONY: test threat fuzz

test:
	go build ./... && go vet ./... && go test -race -count=1 ./...

# Cross-user threat suite, ownership property tests and SSE recovery properties
# on their fixed corpora. A failure names the surface: "SURFACE BROKEN [run cancel]".
threat:
	go test -race -count=1 -run '$(THREAT_RUN)' ./internal/chat ./internal/store

# The same properties as native fuzz targets, each for FUZZTIME. One target per
# invocation is a Go requirement of -fuzz. A crashing input is written to
# testdata/fuzz/<Target>/ in the package; commit it as a regression test.
fuzz:
	go test ./internal/store -run '^$$' -fuzz '^FuzzOwnership$$' -fuzztime $(FUZZTIME)
	go test ./internal/chat -run '^$$' -fuzz '^FuzzOwnershipOverHTTP$$' -fuzztime $(FUZZTIME)
	go test ./internal/chat -run '^$$' -fuzz '^FuzzSSERecovery$$' -fuzztime $(FUZZTIME)
