// Command geass-cli is the geass command line: it deploys and runs services on a Geass cluster. It is
// installed as `geass`. (The cluster installer is a separate program, cmd/geass.)
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/degoke/geass/internal/cli"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	code := cli.NewApp(version).Run(ctx, os.Args[1:])
	stop()
	os.Exit(code)
}
