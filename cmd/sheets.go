package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/hairizuanbinnoorazman/gws-go/internal/api"
	"github.com/hairizuanbinnoorazman/gws-go/internal/auth"
	"github.com/hairizuanbinnoorazman/gws-go/internal/clierr"
	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
	"github.com/spf13/cobra"
)

func addServiceHelpers(serviceName string, command *cobra.Command, doc *discovery.Document, deps dependencies) {
	switch serviceName {
	case "sheets":
		command.AddCommand(newSheetsReadCommand(doc, deps.out), newSheetsAppendCommand(doc, deps.out))
	case "calendar":
		command.AddCommand(newCalendarAgendaCommand(doc, deps.out), newCalendarCreateEventCommand(doc, deps.out))
	case "docs":
		command.AddCommand(newDocsWriteCommand(doc, deps.out))
	case "drive":
		command.AddCommand(newDriveUploadCommand(doc, deps.out), newDriveDownloadCommand(doc, deps.out), newDriveShareCommand(doc, deps.out))
	case "gmail":
		command.AddCommand(newGmailReadCommand(doc, deps.out), newGmailSearchCommand(doc, deps.out), newGmailExportCommand(doc, deps.out))
	}
}

func newSheetsReadCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var spreadsheetID string
	var cellRange string
	var majorDimension string
	var opts api.Options
	command := &cobra.Command{
		Use:   "read",
		Short: "Read values from a spreadsheet range",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if majorDimension != "" {
				if err := validateChoice(majorDimension, "--major-dimension", "ROWS", "COLUMNS"); err != nil {
					return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
				}
			}
			method, err := sheetsValuesMethod(doc, "get")
			if err != nil {
				return err
			}
			params := map[string]any{"spreadsheetId": spreadsheetID, "range": cellRange}
			if majorDimension != "" {
				params["majorDimension"] = majorDimension
			}
			opts.ParamsJSON, err = marshalOption(params)
			if err != nil {
				return err
			}
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&spreadsheetID, "spreadsheet", "", "spreadsheet ID")
	command.Flags().StringVar(&cellRange, "range", "", "A1 notation range, such as Sheet1!A1:C20")
	command.Flags().StringVar(&majorDimension, "major-dimension", "", "return values as ROWS or COLUMNS")
	_ = command.MarkFlagRequired("spreadsheet")
	_ = command.MarkFlagRequired("range")
	addHelperExecutionFlags(command, &opts, out)
	return command
}

func newSheetsAppendCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var spreadsheetID string
	var cellRange string
	var valuesJSON string
	var valuesFile string
	var inputOption string
	var insertOption string
	var majorDimension string
	var includeValues bool
	var opts api.Options
	command := &cobra.Command{
		Use:   "append",
		Short: "Append rows or columns to a spreadsheet range",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			for _, validation := range []struct {
				value   string
				flag    string
				choices []string
			}{
				{inputOption, "--input-option", []string{"RAW", "USER_ENTERED"}},
				{insertOption, "--insert-option", []string{"INSERT_ROWS", "OVERWRITE"}},
				{majorDimension, "--major-dimension", []string{"ROWS", "COLUMNS"}},
			} {
				if err := validateChoice(validation.value, validation.flag, validation.choices...); err != nil {
					return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
				}
			}
			method, err := sheetsValuesMethod(doc, "append")
			if err != nil {
				return err
			}
			values, err := readHelperJSON(valuesJSON, valuesFile, command.InOrStdin())
			if err != nil {
				return clierr.New("invalid_argument", "invalid values input", clierr.ExitInput, err)
			}
			var rows []any
			if err := json.Unmarshal(values, &rows); err != nil {
				return clierr.New("invalid_argument", "--values must be a JSON array of rows or columns", clierr.ExitInput, err)
			}
			body := map[string]any{"majorDimension": majorDimension, "values": rows}
			opts.BodyJSON, err = marshalOption(body)
			if err != nil {
				return err
			}
			params := map[string]any{
				"spreadsheetId":           spreadsheetID,
				"range":                   cellRange,
				"valueInputOption":        inputOption,
				"insertDataOption":        insertOption,
				"includeValuesInResponse": includeValues,
			}
			opts.ParamsJSON, err = marshalOption(params)
			if err != nil {
				return err
			}
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&spreadsheetID, "spreadsheet", "", "spreadsheet ID")
	command.Flags().StringVar(&cellRange, "range", "", "A1 notation range to append to")
	command.Flags().StringVar(&valuesJSON, "values", "", "JSON array of rows or columns (- reads stdin)")
	command.Flags().StringVar(&valuesFile, "values-file", "", "read values from a JSON file (- reads stdin)")
	command.Flags().StringVar(&inputOption, "input-option", "USER_ENTERED", "value interpretation: RAW or USER_ENTERED")
	command.Flags().StringVar(&insertOption, "insert-option", "INSERT_ROWS", "append behavior: INSERT_ROWS or OVERWRITE")
	command.Flags().StringVar(&majorDimension, "major-dimension", "ROWS", "input values are ROWS or COLUMNS")
	command.Flags().BoolVar(&includeValues, "include-values", false, "include appended values in the response")
	_ = command.MarkFlagRequired("spreadsheet")
	_ = command.MarkFlagRequired("range")
	addHelperExecutionFlags(command, &opts, out)
	return command
}

func sheetsValuesMethod(doc *discovery.Document, name string) (*discovery.Method, error) {
	resource, err := findResource(doc.Resources, "spreadsheets.values")
	if err != nil {
		return nil, err
	}
	method, ok := resource.Methods[name]
	if !ok {
		return nil, fmt.Errorf("sheets Discovery method spreadsheets.values.%s was not found", name)
	}
	return method, nil
}

func executeHelper(command *cobra.Command, doc *discovery.Document, method *discovery.Method, opts *api.Options) error {
	var clientErr error
	var executor api.Executor
	if !opts.DryRun {
		executor.Client, clientErr = auth.HTTPClient(command.Context())
		if clientErr != nil {
			return clierr.New("authentication_error", "authentication failed", clierr.ExitAuth, clientErr)
		}
	}
	return executor.Execute(command.Context(), doc, method, *opts)
}

func addHelperExecutionFlags(command *cobra.Command, opts *api.Options, out io.Writer) {
	opts.Out = out
	command.Flags().StringVarP(&opts.OutputPath, "output", "o", "", "write the raw response body to a file")
	command.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the request without sending it")
	command.Flags().DurationVar(&opts.RequestTimeout, "timeout", 30*time.Second, "timeout for the HTTP request (0 disables)")
	command.Flags().IntVar(&opts.MaxRetries, "max-retries", 4, "maximum retries for HTTP 408, 429, and transient 5xx responses")
	command.Flags().DurationVar(&opts.RetryDelay, "retry-delay", 500*time.Millisecond, "initial exponential retry delay")
	command.Flags().StringVar(&opts.Format, "format", "json", "output format: json, jsonl, table, yaml, or csv")
	command.Flags().StringVar(&opts.Fields, "fields", "", "comma-separated dotted response fields to select")
	command.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "print only resource IDs or fields selected with --fields")
}

func marshalOption(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

func readHelperJSON(inline, path string, input io.Reader) ([]byte, error) {
	if inline != "" && path != "" {
		return nil, errors.New("--values and --values-file cannot be combined")
	}
	if inline == "" && path == "" {
		return nil, errors.New("--values or --values-file is required")
	}
	if inline != "" && inline != "-" {
		return []byte(inline), nil
	}
	if inline == "-" || path == "-" {
		return io.ReadAll(io.LimitReader(input, 16<<20))
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return nil, errors.New("--values-file must be a regular file no larger than 16 MiB")
	}
	return os.ReadFile(path)
}

func validateChoice(value, flag string, choices ...string) error {
	for _, choice := range choices {
		if strings.EqualFold(value, choice) {
			return nil
		}
	}
	return fmt.Errorf("%s must be one of %s", flag, strings.Join(choices, ", "))
}
