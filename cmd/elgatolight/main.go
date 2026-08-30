package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"syscall"

	"git2.riper.fr/ztec/elgatocmd/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		if errors.Is(err, fs.ErrPermission) {
			fmt.Fprintln(os.Stderr, "USB access is denied. Run elgatolight setup --scope none, then reconnect the light.")
		}
		os.Exit(1)
	}
}
