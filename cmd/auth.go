package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/hairizuanbinnoorazman/gws-go/internal/auth"
	"github.com/spf13/cobra"
)

func newAuthCommand(out io.Writer) *cobra.Command {
	command := &cobra.Command{Use: "auth", Short: "Manage OAuth 2.0 authentication"}
	command.AddCommand(newLoginCommand(out), newStatusCommand(out), newLogoutCommand(out))
	return command
}

func newLoginCommand(out io.Writer) *cobra.Command {
	var clientSecret string
	var noBrowser bool
	var scopes string
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "login",
		Short: "Authorize offline access and save a refresh token",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			ctx, cancel := contextWithTimeout(command.Context(), timeout)
			defer cancel()
			return auth.Login(ctx, auth.LoginOptions{
				ClientSecretFile: clientSecret,
				NoBrowser:        noBrowser,
				Timeout:          timeout,
				Scopes:           auth.ParseScopes(scopes),
				Out:              out,
			})
		},
	}
	command.Flags().StringVar(&clientSecret, "client-secret", "", "path to a Google Desktop OAuth client JSON file")
	command.Flags().BoolVar(&noBrowser, "no-browser", false, "print the authorization URL without opening a browser")
	command.Flags().StringVar(&scopes, "scopes", "", "comma-separated OAuth scope URLs (defaults to Docs, Calendar, and Slides)")
	command.Flags().DurationVar(&timeout, "timeout", 5*time.Minute, "maximum time to wait for the browser callback")
	return command
}

func newStatusCommand(out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the current authentication status",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			status, err := auth.Status()
			if _, writeErr := fmt.Fprintln(out, status); writeErr != nil {
				return writeErr
			}
			return err
		},
	}
}

func newLogoutCommand(out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Delete locally saved OAuth tokens",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := auth.Logout(); err != nil {
				return err
			}
			_, err := fmt.Fprintln(out, "Local OAuth token removed.")
			return err
		},
	}
}
