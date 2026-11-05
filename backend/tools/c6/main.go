// Command c6 drives the C6 staging acceptance and rollback rehearsal (docs/C6-STAGING-REHEARSAL.md)
// against DISPOSABLE environments only. It is a test tool: it is never part of a release.
//
//	c6 whoami   -db URL                      isolation proof (database, server address, version)
//	c6 createdb -admin URL -owner URL -app URL [-prefix rokh_e2e]   new throwaway database (prints env lines)
//	c6 dropdb   -admin URL -name NAME        drops a database created by createdb (name must match the prefix)
//	c6 snapshot -db URL -out FILE            per-row digests, sums and invariants of schema rokh
//	c6 compare  -before A -after B -mode MODE [-volatile t1,t2]   see compare.go
//	c6 phase    NAME -url BASE -state FILE   HTTP scenarios (see phases.go)
//
// Every database command refuses a server that is not on a loopback address unless
// C6_ALLOW_NON_LOOPBACK=yes (never set it for anything but a throwaway container network).
package main

import (
	"context"
	"fmt"
	"os"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: c6 whoami|createdb|dropdb|snapshot|compare|phase ...")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var err error
	switch os.Args[1] {
	case "whoami":
		err = cmdWhoami(ctx, os.Args[2:])
	case "createdb":
		err = cmdCreateDB(ctx, os.Args[2:])
	case "dropdb":
		err = cmdDropDB(ctx, os.Args[2:])
	case "snapshot":
		err = cmdSnapshot(ctx, os.Args[2:])
	case "compare":
		err = cmdCompare(os.Args[2:])
	case "phase":
		err = cmdPhase(ctx, os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "c6:", err)
		os.Exit(1)
	}
}
