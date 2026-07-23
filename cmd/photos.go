package cmd

import (
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"

	"github.com/hairizuanbinnoorazman/gws-go/internal/auth"
	"github.com/hairizuanbinnoorazman/gws-go/internal/photos"
	"github.com/spf13/cobra"
)

func newPhotosCommand(out io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "photos",
		Short: "Select and download media from Google Photos",
	}
	command.AddCommand(newPhotosDownloadCommand(out))
	return command
}

func newPhotosDownloadCommand(out io.Writer) *cobra.Command {
	var outputDir string
	var maxItems int
	var noBrowser bool
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "download",
		Short: "Select photos or videos and download them",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			ctx, cancel := contextWithTimeout(command.Context(), timeout)
			defer cancel()
			client, err := auth.HTTPClient(ctx)
			if err != nil {
				return err
			}
			var opener func(string) error
			if !noBrowser {
				opener = openPicker
			}
			return photos.Download(ctx, client, photos.Options{
				OutputDir: outputDir,
				MaxItems:  maxItems,
				Out:       out,
				OpenURL:   opener,
			})
		},
	}
	command.Flags().StringVarP(&outputDir, "output-dir", "o", "google-photos", "directory for downloaded media")
	command.Flags().IntVar(&maxItems, "max-items", 0, "maximum selectable items (0 uses Google's default of 2000)")
	command.Flags().BoolVar(&noBrowser, "no-browser", false, "print the picker URL without opening a browser")
	command.Flags().DurationVar(&timeout, "timeout", 15*time.Minute, "maximum time to wait for selection and downloads")
	return command
}

func openPicker(uri string) error {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{uri}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", uri}
	default:
		name, args = "xdg-open", []string{uri}
	}
	if err := exec.Command(name, args...).Start(); err != nil {
		return fmt.Errorf("open picker: %w", err)
	}
	return nil
}
