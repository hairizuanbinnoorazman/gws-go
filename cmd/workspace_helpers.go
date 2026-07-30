package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hairizuanbinnoorazman/gws-go/internal/api"
	"github.com/hairizuanbinnoorazman/gws-go/internal/clierr"
	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
	"github.com/spf13/cobra"
)

func newCalendarAgendaCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var calendarID, from, to string
	var days, maxResults int
	var opts api.Options
	command := &cobra.Command{
		Use:   "agenda",
		Short: "List upcoming events in chronological order",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if days <= 0 || maxResults <= 0 {
				return clierr.New("invalid_argument", "--days and --max-results must be positive", clierr.ExitInput, nil)
			}
			method, err := discoveredMethod(doc, "events", "list")
			if err != nil {
				return err
			}
			start := time.Now()
			if from != "" {
				start, err = time.Parse(time.RFC3339, from)
				if err != nil {
					return clierr.New("invalid_argument", "--from must use RFC3339", clierr.ExitInput, err)
				}
			}
			end := start.Add(time.Duration(days) * 24 * time.Hour)
			if to != "" {
				end, err = time.Parse(time.RFC3339, to)
				if err != nil {
					return clierr.New("invalid_argument", "--to must use RFC3339", clierr.ExitInput, err)
				}
			}
			if !end.After(start) {
				return clierr.New("invalid_argument", "--to must be after --from", clierr.ExitInput, nil)
			}
			params := map[string]any{
				"calendarId":   calendarID,
				"timeMin":      start.Format(time.RFC3339),
				"timeMax":      end.Format(time.RFC3339),
				"singleEvents": true,
				"orderBy":      "startTime",
				"maxResults":   maxResults,
			}
			opts.ParamsJSON, err = marshalOption(params)
			if err != nil {
				return err
			}
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&calendarID, "calendar", "primary", "calendar ID")
	command.Flags().StringVar(&from, "from", "", "start time in RFC3339 (defaults to now)")
	command.Flags().StringVar(&to, "to", "", "end time in RFC3339")
	command.Flags().IntVar(&days, "days", 7, "days after --from when --to is omitted")
	command.Flags().IntVar(&maxResults, "max-results", 100, "maximum events per page")
	addHelperExecutionFlags(command, &opts, out)
	addHelperPaginationFlags(command, &opts)
	return command
}

func newCalendarCreateEventCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var calendarID, summary, start, end, description, location, timezone, attendees, sendUpdates string
	var opts api.Options
	command := &cobra.Command{
		Use:   "create-event",
		Short: "Create a timed or all-day calendar event",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validateChoice(sendUpdates, "--send-updates", "all", "externalOnly", "none"); err != nil {
				return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
			}
			method, err := discoveredMethod(doc, "events", "insert")
			if err != nil {
				return err
			}
			startValue, allDay, err := calendarDateValue(start, timezone)
			if err != nil {
				return clierr.New("invalid_argument", "invalid --start", clierr.ExitInput, err)
			}
			endValue, endAllDay, err := calendarDateValue(end, timezone)
			if err != nil {
				return clierr.New("invalid_argument", "invalid --end", clierr.ExitInput, err)
			}
			if allDay != endAllDay {
				return clierr.New("invalid_argument", "--start and --end must both be dates or both be RFC3339 timestamps", clierr.ExitInput, nil)
			}
			body := map[string]any{"summary": summary, "start": startValue, "end": endValue}
			if description != "" {
				body["description"] = description
			}
			if location != "" {
				body["location"] = location
			}
			if attendees != "" {
				var people []map[string]string
				for _, email := range strings.Split(attendees, ",") {
					email = strings.TrimSpace(email)
					if email != "" {
						people = append(people, map[string]string{"email": email})
					}
				}
				body["attendees"] = people
			}
			opts.BodyJSON, err = marshalOption(body)
			if err != nil {
				return err
			}
			opts.ParamsJSON, err = marshalOption(map[string]any{"calendarId": calendarID, "sendUpdates": sendUpdates})
			if err != nil {
				return err
			}
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&calendarID, "calendar", "primary", "calendar ID")
	command.Flags().StringVar(&summary, "summary", "", "event summary")
	command.Flags().StringVar(&start, "start", "", "start date (YYYY-MM-DD) or RFC3339 timestamp")
	command.Flags().StringVar(&end, "end", "", "exclusive end date or RFC3339 timestamp")
	command.Flags().StringVar(&timezone, "timezone", "", "IANA timezone for timestamp events")
	command.Flags().StringVar(&description, "description", "", "event description")
	command.Flags().StringVar(&location, "location", "", "event location")
	command.Flags().StringVar(&attendees, "attendees", "", "comma-separated attendee email addresses")
	command.Flags().StringVar(&sendUpdates, "send-updates", "none", "notification behavior: all, externalOnly, or none")
	_ = command.MarkFlagRequired("summary")
	_ = command.MarkFlagRequired("start")
	_ = command.MarkFlagRequired("end")
	addHelperExecutionFlags(command, &opts, out)
	return command
}

func calendarDateValue(value, timezone string) (map[string]any, bool, error) {
	if _, err := time.Parse("2006-01-02", value); err == nil {
		return map[string]any{"date": value}, true, nil
	}
	if _, err := time.Parse(time.RFC3339, value); err != nil {
		return nil, false, errors.New("use YYYY-MM-DD or RFC3339")
	}
	result := map[string]any{"dateTime": value}
	if timezone != "" {
		result["timeZone"] = timezone
	}
	return result, false, nil
}

func newDocsWriteCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var documentID, text, textFile string
	var index int
	var opts api.Options
	command := &cobra.Command{
		Use:   "write",
		Short: "Insert text into a Google document",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if index < 1 {
				return clierr.New("invalid_argument", "--index must be at least 1", clierr.ExitInput, nil)
			}
			method, err := discoveredMethod(doc, "documents", "batchUpdate")
			if err != nil {
				return err
			}
			contents, err := readTextInput(text, textFile, command.InOrStdin())
			if err != nil {
				return clierr.New("invalid_argument", "read document text", clierr.ExitInput, err)
			}
			body := map[string]any{"requests": []any{
				map[string]any{"insertText": map[string]any{
					"location": map[string]any{"index": index},
					"text":     contents,
				}},
			}}
			opts.ParamsJSON, _ = marshalOption(map[string]any{"documentId": documentID})
			opts.BodyJSON, _ = marshalOption(body)
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&documentID, "document", "", "document ID")
	command.Flags().StringVar(&text, "text", "", "text to insert (- reads stdin)")
	command.Flags().StringVar(&textFile, "text-file", "", "read text from a file (- reads stdin)")
	command.Flags().IntVar(&index, "index", 1, "UTF-16 document index at which to insert")
	_ = command.MarkFlagRequired("document")
	addHelperExecutionFlags(command, &opts, out)
	return command
}

func newDriveUploadCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var filePath, name, folderID, contentType string
	var opts api.Options
	command := &cobra.Command{
		Use:   "upload",
		Short: "Upload a file with optional Drive metadata",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			method, err := discoveredMethod(doc, "files", "create")
			if err != nil {
				return err
			}
			if name == "" {
				name = filepath.Base(filePath)
			}
			body := map[string]any{"name": name}
			if folderID != "" {
				body["parents"] = []string{folderID}
			}
			opts.BodyJSON, _ = marshalOption(body)
			opts.UploadPath = filePath
			opts.UploadContentType = contentType
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&filePath, "file", "", "local file to upload")
	command.Flags().StringVar(&name, "name", "", "Drive filename (defaults to the local filename)")
	command.Flags().StringVar(&folderID, "folder", "", "parent folder ID")
	command.Flags().StringVar(&contentType, "mime-type", "", "media MIME type (detected from the filename when omitted)")
	_ = command.MarkFlagRequired("file")
	addHelperExecutionFlags(command, &opts, out)
	return command
}

func newDriveDownloadCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var fileID, outputPath string
	var force bool
	var opts api.Options
	command := &cobra.Command{
		Use:   "download",
		Short: "Download the media content of a Drive file",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if !force {
				if _, err := os.Stat(outputPath); err == nil {
					return clierr.New("file_exists", "output file already exists; use --force to replace it", clierr.ExitFilesystem, nil)
				} else if !errors.Is(err, os.ErrNotExist) {
					return clierr.New("filesystem_error", "inspect output file", clierr.ExitFilesystem, err)
				}
			}
			method, err := discoveredMethod(doc, "files", "get")
			if err != nil {
				return err
			}
			opts.ParamsJSON, _ = marshalOption(map[string]any{"fileId": fileID, "alt": "media"})
			opts.OutputPath = outputPath
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&fileID, "file", "", "Drive file ID")
	command.Flags().StringVarP(&outputPath, "output", "o", "", "destination file")
	command.Flags().BoolVar(&force, "force", false, "replace an existing destination")
	_ = command.MarkFlagRequired("file")
	_ = command.MarkFlagRequired("output")
	addHelperExecutionFlagsWithoutOutput(command, &opts, out)
	return command
}

func newDriveShareCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var fileID, permissionType, role, email, domain string
	var notify bool
	var opts api.Options
	command := &cobra.Command{
		Use:   "share",
		Short: "Create a Drive permission",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validateChoice(permissionType, "--type", "user", "group", "domain", "anyone"); err != nil {
				return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
			}
			if err := validateChoice(role, "--role", "reader", "commenter", "writer"); err != nil {
				return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
			}
			body := map[string]any{"type": permissionType, "role": role}
			switch permissionType {
			case "user", "group":
				if email == "" {
					return clierr.New("invalid_argument", "--email is required for user and group permissions", clierr.ExitInput, nil)
				}
				body["emailAddress"] = email
			case "domain":
				if domain == "" {
					return clierr.New("invalid_argument", "--domain is required for domain permissions", clierr.ExitInput, nil)
				}
				body["domain"] = domain
			}
			method, err := discoveredMethod(doc, "permissions", "create")
			if err != nil {
				return err
			}
			opts.ParamsJSON, _ = marshalOption(map[string]any{"fileId": fileID, "sendNotificationEmail": notify})
			opts.BodyJSON, _ = marshalOption(body)
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&fileID, "file", "", "Drive file ID")
	command.Flags().StringVar(&permissionType, "type", "user", "permission type: user, group, domain, or anyone")
	command.Flags().StringVar(&role, "role", "reader", "permission role: reader, commenter, or writer")
	command.Flags().StringVar(&email, "email", "", "email for a user or group permission")
	command.Flags().StringVar(&domain, "domain", "", "domain for a domain permission")
	command.Flags().BoolVar(&notify, "notify", true, "send a notification email when supported")
	_ = command.MarkFlagRequired("file")
	addHelperExecutionFlags(command, &opts, out)
	return command
}

func newGmailReadCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var messageID, format string
	var opts api.Options
	command := &cobra.Command{
		Use:   "read",
		Short: "Read a Gmail message",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if err := validateChoice(format, "--message-format", "full", "metadata", "minimal", "raw"); err != nil {
				return clierr.New("invalid_argument", err.Error(), clierr.ExitInput, nil)
			}
			method, err := discoveredMethod(doc, "users.messages", "get")
			if err != nil {
				return err
			}
			opts.ParamsJSON, _ = marshalOption(map[string]any{"userId": "me", "id": messageID, "format": format})
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&messageID, "id", "", "Gmail message ID")
	command.Flags().StringVar(&format, "message-format", "full", "message representation: full, metadata, minimal, or raw")
	_ = command.MarkFlagRequired("id")
	addHelperExecutionFlags(command, &opts, out)
	return command
}

func newGmailSearchCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var query string
	var maxResults int
	var includeSpamTrash bool
	var opts api.Options
	command := &cobra.Command{
		Use:   "search",
		Short: "Search Gmail using the standard Gmail query syntax",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			method, err := discoveredMethod(doc, "users.messages", "list")
			if err != nil {
				return err
			}
			opts.ParamsJSON, _ = marshalOption(map[string]any{
				"userId":           "me",
				"q":                query,
				"maxResults":       maxResults,
				"includeSpamTrash": includeSpamTrash,
			})
			return executeHelper(command, doc, method, &opts)
		},
	}
	command.Flags().StringVar(&query, "query", "", "Gmail search query")
	command.Flags().IntVar(&maxResults, "max-results", 100, "maximum messages per page")
	command.Flags().BoolVar(&includeSpamTrash, "include-spam-trash", false, "include messages from Spam and Trash")
	addHelperExecutionFlags(command, &opts, out)
	addHelperPaginationFlags(command, &opts)
	return command
}

func newGmailExportCommand(doc *discovery.Document, out io.Writer) *cobra.Command {
	var messageID, outputPath string
	var force bool
	var opts api.Options
	command := &cobra.Command{
		Use:   "export",
		Short: "Export one Gmail message as an RFC 2822 .eml file",
		Args:  cobra.NoArgs,
		RunE: func(command *cobra.Command, _ []string) error {
			if !force {
				if _, err := os.Stat(outputPath); err == nil {
					return clierr.New("file_exists", "output file already exists; use --force to replace it", clierr.ExitFilesystem, nil)
				} else if !errors.Is(err, os.ErrNotExist) {
					return clierr.New("filesystem_error", "inspect output file", clierr.ExitFilesystem, err)
				}
			}
			method, err := discoveredMethod(doc, "users.messages", "get")
			if err != nil {
				return err
			}
			opts.ParamsJSON, _ = marshalOption(map[string]any{"userId": "me", "id": messageID, "format": "raw"})
			if opts.DryRun {
				return executeHelper(command, doc, method, &opts)
			}
			var response bytes.Buffer
			requestOpts := opts
			requestOpts.Out = &response
			requestOpts.Format = "json"
			requestOpts.Fields = ""
			requestOpts.Quiet = false
			if err := executeHelper(command, doc, method, &requestOpts); err != nil {
				return err
			}
			var message struct {
				Raw string `json:"raw"`
			}
			if err := json.Unmarshal(response.Bytes(), &message); err != nil || message.Raw == "" {
				return clierr.New("invalid_response", "Gmail response did not contain raw message data", clierr.ExitAPI, err)
			}
			data, err := base64.RawURLEncoding.DecodeString(message.Raw)
			if err != nil {
				data, err = base64.URLEncoding.DecodeString(message.Raw)
			}
			if err != nil {
				return clierr.New("invalid_response", "decode Gmail raw message", clierr.ExitAPI, err)
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
			if force {
				flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			}
			file, err := os.OpenFile(outputPath, flags, 0o600)
			if err != nil {
				return clierr.New("filesystem_error", "create export file", clierr.ExitFilesystem, err)
			}
			if _, err := file.Write(data); err != nil {
				_ = file.Close()
				return clierr.New("filesystem_error", "write export file", clierr.ExitFilesystem, err)
			}
			if err := file.Close(); err != nil {
				return clierr.New("filesystem_error", "close export file", clierr.ExitFilesystem, err)
			}
			_, err = fmt.Fprintf(out, "Exported %s\n", outputPath)
			return err
		},
	}
	command.Flags().StringVar(&messageID, "id", "", "Gmail message ID")
	command.Flags().StringVarP(&outputPath, "output", "o", "", "destination .eml file")
	command.Flags().BoolVar(&force, "force", false, "replace an existing destination")
	_ = command.MarkFlagRequired("id")
	_ = command.MarkFlagRequired("output")
	addHelperExecutionFlagsWithoutOutput(command, &opts, out)
	return command
}

func discoveredMethod(doc *discovery.Document, resourcePath, methodName string) (*discovery.Method, error) {
	resource, err := findResource(doc.Resources, resourcePath)
	if err != nil {
		return nil, err
	}
	method, ok := resource.Methods[methodName]
	if !ok {
		return nil, fmt.Errorf("discovery method %s.%s was not found", resourcePath, methodName)
	}
	return method, nil
}

func addHelperPaginationFlags(command *cobra.Command, opts *api.Options) {
	command.Flags().BoolVar(&opts.PageAll, "page-all", false, "fetch all pages")
	command.Flags().IntVar(&opts.PageLimit, "page-limit", 10, "maximum pages fetched with --page-all")
	command.Flags().DurationVar(&opts.PageDelay, "page-delay", 100*time.Millisecond, "delay between pages")
}

func addHelperExecutionFlagsWithoutOutput(command *cobra.Command, opts *api.Options, out io.Writer) {
	opts.Out = out
	command.Flags().BoolVar(&opts.DryRun, "dry-run", false, "print the request without sending it")
	command.Flags().DurationVar(&opts.RequestTimeout, "timeout", 30*time.Second, "timeout for the HTTP request (0 disables)")
	command.Flags().IntVar(&opts.MaxRetries, "max-retries", 4, "maximum retries for HTTP 408, 429, and transient 5xx responses")
	command.Flags().DurationVar(&opts.RetryDelay, "retry-delay", 500*time.Millisecond, "initial exponential retry delay")
	command.Flags().StringVar(&opts.Format, "format", "json", "output format: json, jsonl, table, yaml, or csv")
	command.Flags().StringVar(&opts.Fields, "fields", "", "comma-separated dotted response fields to select")
	command.Flags().BoolVarP(&opts.Quiet, "quiet", "q", false, "print only resource IDs or fields selected with --fields")
}

func readTextInput(inline, path string, input io.Reader) (string, error) {
	if inline != "" && path != "" {
		return "", errors.New("--text and --text-file cannot be combined")
	}
	if inline == "" && path == "" {
		return "", errors.New("--text or --text-file is required")
	}
	if inline != "" && inline != "-" {
		return inline, nil
	}
	if inline == "-" || path == "-" {
		data, err := io.ReadAll(io.LimitReader(input, 16<<20))
		return string(data), err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > 16<<20 {
		return "", errors.New("--text-file must be a regular file no larger than 16 MiB")
	}
	data, err := os.ReadFile(path)
	return string(data), err
}
