// Package cmd builds and executes the Cobra command tree.
package cmd

import (
	"context"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hairizuanbinnoorazman/gws-go/internal/api"
	"github.com/hairizuanbinnoorazman/gws-go/internal/auth"
	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
	"github.com/spf13/cobra"
)

var version = "dev"

type service struct {
	Name        string
	APIVersion  string
	Description string
}

var services = []service{
	{Name: "docs", APIVersion: "v1", Description: "Read and write Google Docs"},
	{Name: "calendar", APIVersion: "v3", Description: "Manage calendars and events"},
	{Name: "slides", APIVersion: "v1", Description: "Read and write presentations"},
	{Name: "gmail", APIVersion: "v1", Description: "Read Gmail messages and metadata"},
	{Name: "drive", APIVersion: "v3", Description: "Manage files, folders, and shared drives"},
}

type dependencies struct {
	loader discovery.Loader
	out    io.Writer
	errOut io.Writer
}

// Execute runs the root CLI command using the process standard streams.
func Execute() error {
	return newRootCommand(dependencies{out: os.Stdout, errOut: os.Stderr}).Execute()
}

func newRootCommand(deps dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:           "gws-go",
		Short:         "Google Workspace CLI for Docs, Calendar, Slides, Drive, Gmail, and Photos",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetOut(deps.out)
	root.SetErr(deps.errOut)
	root.AddCommand(newAuthCommand(deps.out))
	root.AddCommand(newPhotosCommand(deps.out))
	for _, item := range services {
		root.AddCommand(newServiceCommand(item, deps))
	}
	return root
}

func newServiceCommand(item service, deps dependencies) *cobra.Command {
	command := &cobra.Command{
		Use:                item.Name + " [resource] [method] [flags]",
		Short:              item.Description,
		DisableFlagParsing: true,
		Args:               cobra.ArbitraryArgs,
		RunE: func(command *cobra.Command, args []string) error {
			doc, err := deps.loader.Load(command.Context(), item.Name, item.APIVersion)
			if err != nil {
				return err
			}
			dynamic := buildServiceTree(doc, deps.out, deps.errOut)
			dynamic.SetArgs(args)
			return dynamic.ExecuteContext(command.Context())
		},
	}
	return command
}

func buildServiceTree(doc *discovery.Document, out, errOut io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:           doc.Name,
		Short:         firstLine(doc.Description),
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	command.SetOut(out)
	command.SetErr(errOut)
	for _, name := range sortedResourceNames(doc.Resources) {
		command.AddCommand(buildResourceCommand(doc, name, doc.Resources[name], out))
	}
	return command
}

func buildResourceCommand(doc *discovery.Document, name string, resource *discovery.Resource, out io.Writer) *cobra.Command {
	command := &cobra.Command{Use: name, Short: "Operations on the " + name + " resource"}
	methodNames := make([]string, 0, len(resource.Methods))
	for methodName := range resource.Methods {
		methodNames = append(methodNames, methodName)
	}
	sort.Strings(methodNames)
	for _, methodName := range methodNames {
		method := resource.Methods[methodName]
		command.AddCommand(buildMethodCommand(doc, methodName, method, out))
	}
	for _, childName := range sortedResourceNames(resource.Resources) {
		command.AddCommand(buildResourceCommand(doc, childName, resource.Resources[childName], out))
	}
	return command
}

func buildMethodCommand(doc *discovery.Document, name string, method *discovery.Method, out io.Writer) *cobra.Command {
	var opts api.Options
	command := &cobra.Command{
		Use:   name,
		Short: firstLine(method.Description),
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var client *http.Client
			var err error
			if !opts.DryRun {
				client, err = auth.HTTPClient(command.Context())
				if err != nil {
					return err
				}
			}
			opts.Out = out
			return (api.Executor{Client: client}).Execute(command.Context(), doc, method, opts)
		},
	}
	command.Flags().StringVar(&opts.ParamsJSON, "params", "", "JSON object containing path and query parameters")
	if method.Request != nil {
		command.Flags().StringVar(&opts.BodyJSON, "json", "", "JSON request body")
	}
	command.Flags().StringVarP(&opts.OutputPath, "output", "o", "", "write the raw response body to a file")
	if method.SupportsMediaUpload && method.MediaUpload != nil && method.MediaUpload.Protocols.Simple != nil && method.MediaUpload.Protocols.Simple.Multipart {
		command.Flags().StringVar(&opts.UploadPath, "upload", "", "local file to upload as multipart media content")
		command.Flags().StringVar(&opts.UploadContentType, "upload-content-type", "", "MIME type of the uploaded file (detected automatically when omitted)")
	}
	command.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the request without sending it")
	command.Flags().BoolVar(&opts.PageAll, "page-all", false, "fetch all pages as sequential JSON values")
	command.Flags().IntVar(&opts.PageLimit, "page-limit", 10, "maximum pages fetched with --page-all")
	command.Flags().DurationVar(&opts.PageDelay, "page-delay", 100*time.Millisecond, "delay between pages")
	return command
}

func sortedResourceNames(resources map[string]*discovery.Resource) []string {
	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Google Workspace API operation"
	}
	if newline := strings.IndexByte(value, '\n'); newline >= 0 {
		value = value[:newline]
	}
	if len(value) > 160 {
		value = value[:157] + "..."
	}
	return value
}

func contextWithTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	if timeout <= 0 {
		return context.WithCancel(parent)
	}
	return context.WithTimeout(parent, timeout)
}
