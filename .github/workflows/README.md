# Workflows

`ci.yml` runs on every push to `main` and every PR into it: Go build, vet and race tests;
frontend typecheck and build; and two guard jobs that fail on a credential-shaped file or a Docker
socket reference.

The `go` and `web` jobs are written to no-op cleanly while the repository has no `go.mod` or
`web/package.json` yet — the baseline is specification-only. They start doing real work the moment
the first implementation lands.

**Hermes Review** is a GitHub App, not a workflow in this directory. It is installed on the
repository by the owner, and its check run is the required status check on `main`.
