//go:build !linux

package cli

import (
	"errors"

	"github.com/spf13/cobra"
)

func (app *commandApp) setupCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "setup",
		Short: "Configure USB access and optionally a daemon service",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("setup is supported only on Linux")
		},
	}
}

func (app *commandApp) setupUSBCommand() *cobra.Command {
	return &cobra.Command{
		Use:    "setup-usb",
		Hidden: true,
		Args:   cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			return errors.New("USB setup is supported only on Linux")
		},
	}
}
