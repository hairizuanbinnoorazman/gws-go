# gws-go

A focused Go port of the Google Workspace CLI, built with Cobra. It exposes the
Google Docs, Google Calendar, Google Slides, Google Sheets, Google Drive, and
Gmail REST APIs from Google's Discovery documents and caches those documents
for 24 hours. It also supports selecting and downloading personal media through
the Google Photos Picker API. Gmail authorization is read-only.

## Build and development

Go 1.24 or newer is required.

```sh
go mod download
make install-tools
make test
make lint
make build
```

The binary is written to `bin/gws-go`. Dependencies and linter binaries are kept
inside the repository (`.gomodcache/` and `bin/`) and are ignored by Git.

## OAuth setup

1. In Google Cloud Console, enable the Google Docs API, Google Calendar API,
   Google Slides API, Google Sheets API, Google Drive API, Gmail API, and Google
   Photos Picker API.
2. Configure the OAuth consent screen and add your Google account as a test user
   if the app is in testing mode.
3. Create an OAuth client with application type **Desktop app**, then download
   its JSON file.
4. Log in:

```sh
bin/gws-go auth login --client-secret ~/Downloads/client_secret.json
```

The command opens Google's authorization page and starts a callback server on
`127.0.0.1`. Use `--no-browser` to print the URL for you to open yourself. The
flow uses PKCE, requests `access_type=offline`, and forces a consent prompt so
Google returns a refresh token. The client file and token are stored with mode
`0600` under `~/.config/gws-go/` (or `$GWS_GO_CONFIG_DIR`).

```sh
bin/gws-go auth status
bin/gws-go auth logout
```

For short-lived automation, `GWS_GO_TOKEN` can provide an access token directly.
Existing users must run `auth login` again to grant the Google Photos Picker
and Google Sheets scopes.

The default `standard` scope preset keeps Gmail read-only. To explicitly grant
Gmail send and modify access for discovered write methods, log in with:

```sh
bin/gws-go auth login \
  --client-secret ~/Downloads/client_secret.json \
  --scope-preset gmail-write
```

`--scope-preset` and the lower-level `--scopes` override cannot be combined.

## Usage

The command shape follows the Discovery API hierarchy:

```sh
# Explore live commands
bin/gws-go docs --help
bin/gws-go calendar --help
bin/gws-go slides --help
bin/gws-go sheets --help
bin/gws-go gmail --help
bin/gws-go drive --help
bin/gws-go photos --help

# Inspect accepted parameters and the resolved request schema
bin/gws-go schema calendar events insert

# Fetch a document
bin/gws-go docs documents get \
  --params '{"documentId":"DOCUMENT_ID"}'

# List the next ten primary-calendar events
bin/gws-go calendar events list \
  --params '{"calendarId":"primary","maxResults":10,"singleEvents":true}'

# Show an agenda and create an event through task-oriented helpers
bin/gws-go calendar agenda --days 7 --format table \
  --fields summary,start.dateTime,end.dateTime
bin/gws-go calendar create-event \
  --summary "Planning" \
  --start 2026-08-03T09:00:00+08:00 \
  --end 2026-08-03T10:00:00+08:00 \
  --attendees ada@example.com,lin@example.com

# Insert text at a document index
bin/gws-go docs write \
  --document DOCUMENT_ID --index 1 --text "Project update"

# Create a presentation
bin/gws-go slides presentations create \
  --json '{"title":"Quarterly review"}'

# Read and append spreadsheet values without raw API request JSON
bin/gws-go sheets read \
  --spreadsheet SPREADSHEET_ID --range 'Sheet1!A1:C20' \
  --format table
bin/gws-go sheets append \
  --spreadsheet SPREADSHEET_ID --range 'Sheet1!A:C' \
  --values '[["Ada","Complete",10]]'

# List the ten most recent messages (Gmail access is read-only)
bin/gws-go gmail users messages list \
  --params '{"userId":"me","maxResults":10}'

# Search, read, or export a message with the read-only Gmail grant
bin/gws-go gmail search --query 'from:ada newer_than:7d' \
  --format table --fields id,threadId
bin/gws-go gmail read --id MESSAGE_ID
bin/gws-go gmail export --id MESSAGE_ID --output message.eml

# Produce a script-friendly table from every page
bin/gws-go drive files list \
  --params '{"pageSize":100}' \
  --page-all --format table --fields id,name,mimeType

# Read parameters from a file and the request body from stdin
printf '%s\n' '{"summary":"Planning"}' | \
  bin/gws-go calendar events insert \
    --params-file ./calendar-params.json --json -

# Upload a file to Drive with metadata
bin/gws-go drive files create \
  --json '{"name":"report.pdf"}' \
  --upload ./report.pdf

# Upload a large file in resumable chunks
bin/gws-go drive upload --file ./archive.zip --resumable

# Task-oriented Drive transfer and sharing helpers
bin/gws-go drive upload --file ./report.pdf --folder FOLDER_ID
bin/gws-go drive download --file FILE_ID --output ./report.pdf
# Google Docs, Sheets, Slides, and Drawings are exported automatically
bin/gws-go drive download --file GOOGLE_DOC_ID --output ./report.docx
bin/gws-go drive share --file FILE_ID \
  --email ada@example.com --role writer

# Pick a day's photos or videos in Google Photos and download them
bin/gws-go photos download --output-dir ./day-photos

# Validate and preview a request without authenticating or sending it
bin/gws-go calendar events insert \
  --params '{"calendarId":"primary"}' \
  --json '{"summary":"Planning","start":{"date":"2026-07-20"},"end":{"date":"2026-07-21"}}' \
  --dry-run

# Emit a machine-readable error for scripts
bin/gws-go drive files get \
  --params '{"fileId":"missing"}' \
  --error-format json
```

All methods expose Discovery parameters as native kebab-case flags, such as
`--calendar-id`, `--max-results`, and repeatable `--label-ids` flags. Names that
conflict with gws-go output or execution flags receive an `--api-` prefix; for
example, Google's partial-response `fields` parameter is `--api-fields`, while
`--fields` continues to select fields locally. `--params` remains available for
JSON path/query parameters, and explicitly supplied native flags override its
values. Method help lists parameter types, required parameters, enum values, and
a generated example.
Methods with a request schema also accept `--json`; request bodies are validated
locally against resolved Discovery schemas before authentication or network
access. Use `gws-go schema <service> <resource> <method>` for full
introspection. Nested resources may use dots or slashes, such as
`users.messages`.

List methods can use `--page-all`, `--page-limit`, and `--page-delay`;
paginated JSON output is aggregated into one valid document. `--format` accepts
`json`, `jsonl`, `table`, `yaml`, or `csv`. JSONL emits one collection item per
line. `--fields id,name,owner.displayName` selects dotted response fields, while
`--quiet` prints resource IDs by default or tab-separated values selected by
`--fields`.

Use `--params-file` and `--json-file` to load JSON from files. A path of `-`, or
`--params -`/`--json -`, reads one input from stdin. `--output` writes the raw
response to a file. Methods whose Discovery metadata supports media upload
accept `--upload` and an optional `--upload-content-type`. Multipart uploads
stream from disk instead of being assembled in memory. Methods advertising
Google's resumable protocol also accept `--resumable` and
`--upload-chunk-size`; the Drive helper provides `--resumable` and
`--chunk-size`. `--progress` reports transfer progress to stderr. Saved-file
results include a SHA-256 checksum.

Each API request has a 30-second timeout and retries transport failures plus HTTP
408, 429, 500, 502, 503, and 504 responses up to four times with exponential
backoff. Google `Retry-After` responses are honored. Retries are automatic for
idempotent methods and requests carrying a `requestId`; potentially unsafe
writes require `--retry-unsafe`. Use `--timeout`, `--max-retries`, and
`--retry-delay` to change these values; `--timeout 0` disables the per-request
timeout.

`--error-format json` produces errors with stable `code`, `message`, and
`exit_code` fields plus HTTP and retry details when available. Exit codes are 1
for general failures, 2 for invalid input, 3 for authentication, 4 for Google
API errors, 5 for network failures, 6 for timeouts, and 7 for filesystem
failures.

The Gmail Discovery document includes write methods, but the default OAuth grant
uses only `https://www.googleapis.com/auth/gmail.readonly`; Gmail rejects send,
modify, and delete operations. The `gmail-write` scope preset replaces that
read-only scope with `gmail.modify` and `gmail.send`.

Raw `--output` responses and Drive downloads are streamed through an atomic
temporary file, avoiding the JSON response-size limit and partial destination
files. `drive download` inspects the file type and automatically uses Drive
export for Google Docs, Sheets, Slides, Drawings, and Apps Script projects. Its
`--export-format` flag can explicitly select formats such as PDF, DOCX, XLSX,
PPTX, CSV, or SVG.

Handwritten helpers provide shorter commands for common tasks: Calendar agenda
and event creation, Docs text insertion, Sheets reads and appends, Drive
uploads/downloads/sharing, and Gmail reads/searches/exports. These helpers reuse
Discovery request validation, dry runs, retries, timeouts, field selection, and
output formats.

Google no longer permits apps to search an existing personal Photos library by
date. The supported Picker flow opens Google Photos so you can select the media
for the day, waits for you to finish, downloads the selected media, and cleans
up the Picker session.

This is intentionally not a full port. It does not yet include the original
CLI's full set of workflows, encrypted keyring storage, service accounts, or
response sanitization.
