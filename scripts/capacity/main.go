//go:build capacity

// Command capacity measures Antares' resource envelope on a scratch instance.
// Build tag `capacity` keeps it out of `go build ./...`. See docs/runbooks/capacity.md.
//
//	capacity upstream <addr> <public-idp-url>   fake OIDC provider + provisioner + Hermes
//	capacity server <idp-url>                   scratch Antares (env as .env.example)
//	capacity load <flags> <phase>               setup | idle | load | fill | streams | db
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: capacity upstream|server|load ...")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "upstream":
		err = runUpstream(os.Args[2], os.Args[3])
	case "server":
		err = runServer(os.Args[2])
	case "load":
		err = runLoad(os.Args[2:])
	default:
		err = fmt.Errorf("unknown subcommand %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "capacity:", err)
		os.Exit(1)
	}
}
