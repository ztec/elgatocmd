package main

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"

	"elgatolight/internal/selfupdate"

	"github.com/spf13/cobra"
)

func (app *commandApp) selfUpdateCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "self-update",
		Short: "Update this executable from the latest release",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			jsonOutput := app.config.GetBool("json")
			progressf := func(format string, values ...any) {}
			if !jsonOutput {
				progressf = func(format string, values ...any) {
					fmt.Fprintf(command.OutOrStdout(), format+"\n", values...)
				}
			}
			result, err := selfupdate.Run(app.ctx, selfupdate.Config{
				ReleaseAPI:     app.config.GetString("release.api"),
				CurrentVersion: applicationVersion(),
				GOOS:           runtime.GOOS,
				GOARCH:         runtime.GOARCH,
				Force:          app.config.GetBool("self_update.force"),
				Progressf:      progressf,
			})
			if err != nil {
				if errors.Is(err, fs.ErrPermission) {
					return fmt.Errorf("%w; rerun elgatolight self-update with sudo", err)
				}
				return err
			}
			if jsonOutput {
				return printJSON(command.OutOrStdout(), result, true)
			}
			if !result.Changed {
				fmt.Fprintf(command.OutOrStdout(), "Already up to date at %s (%s).\n", result.Version, result.Channel)
				return nil
			}
			fmt.Fprintf(command.OutOrStdout(), "Updated %s to %s at %s.\n", result.PreviousVersion, result.Version, result.ExecutablePath)
			fmt.Fprintln(command.OutOrStdout(), "Restart any running elgatolight daemon service to use the new version.")
			return nil
		},
	}
	command.Flags().String("release-api", selfupdate.DefaultReleaseAPI, "Forgejo-compatible release API base URL")
	command.Flags().Bool("force", false, "reinstall even when the release version matches")
	if err := app.config.BindPFlag("release.api", command.Flags().Lookup("release-api")); err != nil {
		panic(fmt.Sprintf("bind self-update --release-api: %v", err))
	}
	if err := app.config.BindPFlag("self_update.force", command.Flags().Lookup("force")); err != nil {
		panic(fmt.Sprintf("bind self-update --force: %v", err))
	}
	return command
}
