// Package cmd builds and executes the Cobra command tree.
package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/hairizuanbinnoorazman/gws-go/internal/api"
	"github.com/hairizuanbinnoorazman/gws-go/internal/auth"
	"github.com/hairizuanbinnoorazman/gws-go/internal/clierr"
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
	{Name: "sheets", APIVersion: "v4", Description: "Read and write Google Sheets"},
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

// Run executes the CLI, renders any error, and returns its stable process exit
// code. Global --error-format is accepted anywhere in args.
func Run(args []string, out, errOut io.Writer) int {
	cleanArgs, errorFormat, err := extractErrorFormat(args)
	if err == nil {
		root := newRootCommand(dependencies{out: out, errOut: errOut})
		root.SetArgs(cleanArgs)
		err = root.Execute()
		if err != nil {
			var structured *clierr.Error
			if !errors.As(err, &structured) && isCommandInputError(err) {
				err = clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
			}
		}
	}
	if err == nil {
		return 0
	}
	_ = clierr.Render(errOut, err, errorFormat)
	return clierr.ExitCode(err)
}

func isCommandInputError(err error) bool {
	message := err.Error()
	for _, prefix := range []string{
		"unknown command ", "unknown flag:", "accepts ", "requires at least ",
		"requires at most ", "requires exactly ", "required flag(s) ",
	} {
		if strings.HasPrefix(message, prefix) {
			return true
		}
	}
	return false
}

func newRootCommand(deps dependencies) *cobra.Command {
	root := &cobra.Command{
		Use:           "gws-go",
		Short:         "Google Workspace CLI for Docs, Calendar, Slides, Sheets, Drive, Gmail, and Photos",
		Version:       version,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	root.SetOut(deps.out)
	root.SetErr(deps.errOut)
	root.PersistentFlags().String("error-format", "text", "error output format: text or json")
	root.AddCommand(newAuthCommand(deps.out))
	root.AddCommand(newPhotosCommand(deps.out))
	root.AddCommand(newSchemaCommand(deps))
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
			addServiceHelpers(item.Name, dynamic, doc, deps)
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
	return buildResourceCommandAtPath(doc, name, resource, out, []string{name})
}

func buildResourceCommandAtPath(doc *discovery.Document, name string, resource *discovery.Resource, out io.Writer, path []string) *cobra.Command {
	command := &cobra.Command{Use: name, Short: "Operations on the " + name + " resource"}
	methodNames := make([]string, 0, len(resource.Methods))
	for methodName := range resource.Methods {
		methodNames = append(methodNames, methodName)
	}
	sort.Strings(methodNames)
	for _, methodName := range methodNames {
		method := resource.Methods[methodName]
		command.AddCommand(buildMethodCommandAtPath(doc, methodName, method, out, path))
	}
	for _, childName := range sortedResourceNames(resource.Resources) {
		childPath := append(append([]string{}, path...), childName)
		command.AddCommand(buildResourceCommandAtPath(doc, childName, resource.Resources[childName], out, childPath))
	}
	return command
}

func buildMethodCommandAtPath(doc *discovery.Document, name string, method *discovery.Method, out io.Writer, resourcePath []string) *cobra.Command {
	var opts api.Options
	command := &cobra.Command{
		Use:     name,
		Short:   firstLine(method.Description),
		Long:    methodHelp(doc, method),
		Example: methodExample(doc, resourcePath, name, method),
		Args:    cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			var client *http.Client
			var err error
			if !opts.DryRun {
				client, err = auth.HTTPClient(command.Context())
				if err != nil {
					return clierr.New("authentication_error", "authentication failed", clierr.ExitAuth, err)
				}
			}
			opts.Out = out
			return (api.Executor{Client: client}).Execute(command.Context(), doc, method, opts)
		},
	}
	command.Flags().StringVar(&opts.ParamsJSON, "params", "", "JSON object containing path and query parameters")
	command.Flags().StringVar(&opts.ParamsFile, "params-file", "", "read path and query parameters from a JSON file (- for stdin)")
	if method.Request != nil {
		command.Flags().StringVar(&opts.BodyJSON, "json", "", "JSON request body (- reads from stdin)")
		command.Flags().StringVar(&opts.BodyFile, "json-file", "", "read the request body from a JSON file (- for stdin)")
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
	command.Flags().DurationVar(&opts.RequestTimeout, "timeout", 30*time.Second, "timeout for each HTTP request (0 disables)")
	command.Flags().IntVar(&opts.MaxRetries, "max-retries", 4, "maximum retries for HTTP 408, 429, and 5xx responses")
	command.Flags().DurationVar(&opts.RetryDelay, "retry-delay", 500*time.Millisecond, "initial exponential retry delay")
	command.Flags().StringVar(&opts.Format, "format", "json", "output format: json, jsonl, table, yaml, or csv")
	command.Flags().StringVar(&opts.Fields, "fields", "", "comma-separated dotted response fields to select")
	command.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "print only resource IDs or fields selected with --fields")
	return command
}

func newSchemaCommand(deps dependencies) *cobra.Command {
	return &cobra.Command{
		Use:   "schema <service> <resource> <method>",
		Short: "Inspect a Discovery method and its resolved request schema",
		Args:  cobra.ExactArgs(3),
		RunE: func(command *cobra.Command, args []string) error {
			item, ok := findService(args[0])
			if !ok {
				return clierr.New("invalid_argument", fmt.Sprintf("unsupported service %q", args[0]), clierr.ExitInput, nil)
			}
			doc, err := deps.loader.Load(command.Context(), item.Name, item.APIVersion)
			if err != nil {
				return err
			}
			resource, err := findResource(doc.Resources, args[1])
			if err != nil {
				return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
			}
			method, ok := resource.Methods[args[2]]
			if !ok {
				return clierr.New("invalid_argument", fmt.Sprintf("method %q was not found on resource %q", args[2], args[1]), clierr.ExitInput, nil)
			}
			result := map[string]any{
				"service":     doc.Name,
				"version":     doc.Version,
				"resource":    args[1],
				"method":      args[2],
				"http_method": method.HTTPMethod,
				"path":        method.Path,
				"parameters":  describeParameters(doc, method),
			}
			if method.Request != nil {
				resolved, resolveErr := doc.DescribeSchema(method.Request.Ref)
				if resolveErr != nil {
					return resolveErr
				}
				result["request"] = map[string]any{"$ref": method.Request.Ref, "schema": resolved}
			}
			return writeCommandJSON(deps.out, result)
		},
	}
}

func findService(name string) (service, bool) {
	for _, item := range services {
		if item.Name == name {
			return item, true
		}
	}
	return service{}, false
}

func findResource(resources map[string]*discovery.Resource, path string) (*discovery.Resource, error) {
	parts := strings.FieldsFunc(path, func(r rune) bool { return r == '.' || r == '/' })
	if len(parts) == 0 {
		return nil, fmt.Errorf("resource path is required")
	}
	current := resources
	var resource *discovery.Resource
	for _, part := range parts {
		var ok bool
		resource, ok = current[part]
		if !ok {
			return nil, fmt.Errorf("resource %q was not found", path)
		}
		current = resource.Resources
	}
	return resource, nil
}

func describeParameters(doc *discovery.Document, method *discovery.Method) []map[string]any {
	combined := make(map[string]*discovery.Parameter, len(doc.Parameters)+len(method.Parameters))
	for name, parameter := range doc.Parameters {
		combined[name] = parameter
	}
	for name, parameter := range method.Parameters {
		combined[name] = parameter
	}
	names := make([]string, 0, len(combined))
	for name := range combined {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]map[string]any, 0, len(names))
	for _, name := range names {
		parameter := combined[name]
		item := map[string]any{"name": name}
		if parameter.Location != "" {
			item["location"] = parameter.Location
		}
		if parameter.Type != "" {
			item["type"] = parameter.Type
		}
		if parameter.Format != "" {
			item["format"] = parameter.Format
		}
		if parameter.Description != "" {
			item["description"] = parameter.Description
		}
		if parameter.Required {
			item["required"] = true
		}
		if parameter.Repeated {
			item["repeated"] = true
		}
		if len(parameter.Enum) > 0 {
			item["enum"] = parameter.Enum
		}
		if parameter.Default != nil {
			item["default"] = parameter.Default
		}
		result = append(result, item)
	}
	return result
}

func methodHelp(doc *discovery.Document, method *discovery.Method) string {
	var builder strings.Builder
	builder.WriteString(strings.TrimSpace(method.Description))
	parameters := describeParameters(doc, method)
	if len(parameters) > 0 {
		builder.WriteString("\n\nParameters (provide as JSON with --params):")
		for _, parameter := range parameters {
			name, _ := parameter["name"].(string)
			kind, _ := parameter["type"].(string)
			if kind == "" {
				kind = "any"
			}
			builder.WriteString("\n  " + name + " (" + kind)
			if parameter["required"] == true {
				builder.WriteString(", required")
			}
			if location, ok := parameter["location"].(string); ok && location != "" {
				builder.WriteString(", " + location)
			}
			builder.WriteString(")")
			if values, ok := parameter["enum"].([]string); ok && len(values) > 0 {
				builder.WriteString(" values: " + strings.Join(values, ", "))
			}
			if description, ok := parameter["description"].(string); ok && description != "" {
				builder.WriteString("\n    " + firstLine(description))
			}
		}
	}
	if method.Request != nil {
		builder.WriteString("\n\nRequest body schema: " + method.Request.Ref)
	}
	return builder.String()
}

func methodExample(doc *discovery.Document, resourcePath []string, methodName string, method *discovery.Method) string {
	if len(resourcePath) == 0 {
		return ""
	}
	parts := []string{"gws-go", doc.Name}
	parts = append(parts, resourcePath...)
	parts = append(parts, methodName)
	params := make(map[string]any)
	for name, parameter := range method.Parameters {
		if !parameter.Required {
			continue
		}
		switch {
		case len(parameter.Enum) > 0:
			params[name] = parameter.Enum[0]
		case parameter.Type == "integer" || parameter.Type == "number":
			params[name] = 0
		default:
			params[name] = strings.ToUpper(name)
		}
	}
	if len(params) > 0 {
		encoded, _ := json.Marshal(params)
		parts = append(parts, "--params", "'"+string(encoded)+"'")
	}
	if method.Request != nil {
		if example, err := doc.ExampleForSchema(method.Request.Ref); err == nil {
			encoded, _ := json.Marshal(example)
			parts = append(parts, "--json", "'"+string(encoded)+"'")
		}
	}
	return strings.Join(parts, " ")
}

func writeCommandJSON(out io.Writer, value any) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func extractErrorFormat(args []string) ([]string, string, error) {
	format := "text"
	clean := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		arg := args[index]
		switch {
		case arg == "--error-format":
			if index+1 >= len(args) {
				return nil, format, clierr.New("invalid_argument", "--error-format requires a value", clierr.ExitInput, nil)
			}
			index++
			format = args[index]
		case strings.HasPrefix(arg, "--error-format="):
			format = strings.TrimPrefix(arg, "--error-format=")
		default:
			clean = append(clean, arg)
		}
	}
	if format != "text" && format != "json" {
		return nil, "text", clierr.New("invalid_argument", "--error-format must be text or json", clierr.ExitInput, nil)
	}
	return clean, format, nil
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
