# gws-go

A focused Go port of the Google Workspace CLI, built with Cobra. It exposes the
Google Docs, Google Calendar, Google Slides, Google Drive, and Gmail REST APIs
from Google's Discovery documents and caches those documents for 24 hours. It
also supports selecting and downloading personal media through the Google
Photos Picker API. Gmail authorization is read-only.

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
   Google Slides API, Google Drive API, Gmail API, and Google Photos Picker API.
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
scope.

## Usage

The command shape follows the Discovery API hierarchy:

```sh
# Explore live commands
bin/gws-go docs --help
bin/gws-go calendar --help
bin/gws-go slides --help
bin/gws-go gmail --help
bin/gws-go drive --help
bin/gws-go photos --help

# Fetch a document
bin/gws-go docs documents get \
  --params '{"documentId":"DOCUMENT_ID"}'

# List the next ten primary-calendar events
bin/gws-go calendar events list \
  --params '{"calendarId":"primary","maxResults":10,"singleEvents":true}'

# Create a presentation
bin/gws-go slides presentations create \
  --json '{"title":"Quarterly review"}'

# List the ten most recent messages (Gmail access is read-only)
bin/gws-go gmail users messages list \
  --params '{"userId":"me","maxResults":10}'

# Upload a file to Drive with metadata
bin/gws-go drive files create \
  --json '{"name":"report.pdf"}' \
  --upload ./report.pdf

# Pick a day's photos or videos in Google Photos and download them
bin/gws-go photos download --output-dir ./day-photos

# Validate and preview a request without authenticating or sending it
bin/gws-go calendar events insert \
  --params '{"calendarId":"primary"}' \
  --json '{"summary":"Planning","start":{"date":"2026-07-20"},"end":{"date":"2026-07-21"}}' \
  --dry-run
```

All methods accept `--params` for path/query parameters. Methods with a request
schema also accept `--json`. List methods can use `--page-all`, `--page-limit`,
and `--page-delay`; `--output` writes the raw response to a file. Methods whose
Discovery metadata supports multipart media upload accept `--upload` and an
optional `--upload-content-type`.

The Gmail Discovery document includes write methods, but the default OAuth grant
uses only `https://www.googleapis.com/auth/gmail.readonly`; Gmail rejects send,
modify, and delete operations.

Google no longer permits apps to search an existing personal Photos library by
date. The supported Picker flow opens Google Photos so you can select the media
for the day, waits for you to finish, downloads the selected media, and cleans
up the Picker session.

This is intentionally not a full port. It does not yet include the original
CLI's handwritten helper commands, schema introspection, alternate output
formats, encrypted keyring storage, service accounts, or response
sanitization.
