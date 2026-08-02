package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/hairizuanbinnoorazman/gws-go/internal/auth"
	personalMaps "github.com/hairizuanbinnoorazman/gws-go/internal/maps"
	"github.com/spf13/cobra"
)

func newMapsCommand(out io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:   "maps",
		Short: "Export personal Google Maps data (Timeline is not available via API)",
		Long: "Export the personal Maps data exposed by Google's Data Portability API. " +
			"Google does not provide API access to Maps Timeline or location-history visits; " +
			"Timeline must be exported from the Google Maps mobile app.",
	}
	command.AddCommand(newMapsResourcesCommand(out), newMapsExportCommand(out))
	return command
}

func newMapsResourcesCommand(out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "resources",
		Short: "List the personal Maps data groups available for export",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			names := make([]string, 0, len(personalMaps.Resources))
			for name := range personalMaps.Resources {
				names = append(names, name)
			}
			sort.Strings(names)
			for _, name := range names {
				if _, err := fmt.Fprintf(out, "%s\t%s\n", name, personalMaps.Resources[name]); err != nil {
					return err
				}
			}
			return nil
		},
	}
}

func newMapsExportCommand(out io.Writer) *cobra.Command {
	var resources []string
	var date string
	var start string
	var end string
	var outputDir string
	var timeout time.Duration
	command := &cobra.Command{
		Use:   "export",
		Short: "Create and download a personal Maps data archive",
		Long: "Create and download a Maps archive through Google's Data Portability API. " +
			"The default resource is Maps activity (searches and directions), not Timeline visits.",
		Args: cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if date != "" && (start != "" || end != "") {
				return fmt.Errorf("--date cannot be combined with --start or --end")
			}
			if date != "" {
				day, err := time.ParseInLocation("2006-01-02", date, time.Local)
				if err != nil {
					return fmt.Errorf("--date must use YYYY-MM-DD: %w", err)
				}
				start = day.Format(time.RFC3339)
				end = day.AddDate(0, 0, 1).Format(time.RFC3339)
			}
			for flag, value := range map[string]string{"--start": start, "--end": end} {
				if value != "" {
					if _, err := time.Parse(time.RFC3339, value); err != nil {
						return fmt.Errorf("%s must be an RFC3339 timestamp: %w", flag, err)
					}
				}
			}
			ctx, cancel := contextWithTimeout(command.Context(), timeout)
			defer cancel()
			client, err := auth.MapsHTTPClient(ctx)
			if err != nil {
				return err
			}
			return personalMaps.Export(ctx, client, personalMaps.Options{
				Resources: resources,
				StartTime: start,
				EndTime:   end,
				OutputDir: outputDir,
				Out:       out,
			})
		},
	}
	command.Flags().StringSliceVar(&resources, "resources", []string{"myactivity.maps"}, "Maps resource groups to export (see maps resources)")
	command.Flags().StringVar(&date, "date", "", "export Maps activity for one local day (YYYY-MM-DD)")
	command.Flags().StringVar(&start, "start", "", "Maps activity start time (RFC3339)")
	command.Flags().StringVar(&end, "end", "", "Maps activity end time (RFC3339)")
	command.Flags().StringVarP(&outputDir, "output-dir", "o", "google-maps", "directory for downloaded archive files")
	command.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "maximum time to wait for archive creation and download")
	command.Example = strings.Join([]string{
		"  gws-go auth login --scope-preset maps",
		"  gws-go maps export --date 2026-08-02",
		"  gws-go maps export --resources maps.starred_places,maps.reviews",
	}, "\n")
	return command
}
