//go:build !linux

package main

import (
	"errors"

	"github.com/spf13/cobra"
)

func (app *commandApp) setupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Install the command, USB access, and optionally a daemon service",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("setup is supported only on Linux")
		},
	}
}
