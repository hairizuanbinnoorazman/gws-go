# `gws-cli` feature comparison

This document compares this Go port with the sibling Rust implementation in
`../gws-cli`. The comparison reflects the current working trees as of July 2026.

## Summary

`gws-go` implements the core Discovery-driven command model for a focused set of
Google Workspace APIs. `gws-cli` adds broader API coverage, higher-level helper
commands, production credential options, richer validation and output, agent
tooling, and several distribution formats.

| Area | `gws-cli` | `gws-go` |
| --- | --- | --- |
| Registered services | 18 | Docs, Calendar, Slides, Sheets, Gmail, and Drive |
| Gmail access | Read and write | Read-only by default; explicit `gmail-write` scope preset |
| Helper commands | 25 service helpers and workflows | 11 task-oriented helpers across Calendar, Docs, Sheets, Drive, and Gmail |
| Authentication | OAuth setup/login/export, encrypted keyring, service accounts, ADC | Desktop OAuth login and access-token environment variable |
| Output | JSON, table, YAML, and CSV | JSON, JSONL, table, YAML, CSV, quiet field output, and raw file output |
| Media transfer | Multipart uploads and media downloads | Multipart uploads, stored Drive-file downloads, Gmail `.eml` exports, and Photos Picker downloads |
| Schema support | Introspection and request-body validation | Reference-resolved introspection, schema-aware help, and request-body validation |
| Reliability | Retries for rate limits and transient network failures | Exponential retries for HTTP 408, 429, and transient 5xx responses; honors `Retry-After`; configurable per-request timeout |
| Agent tooling | Generated skills, personas, recipes, Gemini extension | None |
| Errors | Structured JSON errors and distinct exit codes | Structured JSON errors and distinct exit codes |
| Response safety | Model Armor and terminal sanitization | Response-size limits |

## Additional services in `gws-cli`

Beyond the APIs currently registered by `gws-go`, `gws-cli` supports:

- Admin Reports
- Google Tasks
- People and Contacts
- Google Chat
- Google Classroom
- Google Forms
- Google Keep
- Google Meet
- Google Workspace Events
- Model Armor
- Google Apps Script
- Synthetic cross-service workflows

## Helper commands

The Rust CLI provides handwritten `+verb` commands when a task needs
orchestration, format translation, MIME construction, or multiple APIs:

- Gmail: `+send`, `+read`, `+reply`, `+reply-all`, `+forward`, `+triage`, `+watch`
  (`gws-go` provides read, search, and `.eml` export)
- Calendar: `+insert`, `+agenda` (`gws-go` provides create-event and agenda)
- Sheets: `+append`, `+read` (available as `sheets append` and `sheets read` in
  `gws-go`)
- Docs: `+write` (available as `docs write` in `gws-go`)
- Drive: `+upload` (`gws-go` provides upload, download, and share)
- Chat: `+send`
- Apps Script: `+push`
- Workspace Events: `+subscribe`, `+renew`
- Model Armor: `+sanitize-prompt`, `+sanitize-response`, `+create-template`
- Workflows: `+standup-report`, `+meeting-prep`, `+email-to-task`,
  `+weekly-digest`, `+file-announce`

## Authentication and credentials

Features present in `gws-cli` but not yet in this repository include:

- Automated `gcloud`-based project and OAuth setup
- Service-oriented scope selection (`gws-go` has `standard` and `gmail-write`
  presets)
- Credential export for headless environments
- AES-256-GCM encrypted credential files with OS-keyring support
- Service-account credentials
- Application Default Credentials
- `.env` loading
- Proxy-aware token refresh
- Quota-project attribution

`gws-go` currently supports a local Desktop OAuth flow with PKCE, persisted
owner-only client and token files, custom scope URLs, and `GWS_GO_TOKEN` for a
pre-obtained access token.

## Request, response, and operational features

The Rust CLI additionally provides:

- Automatic media-download handling
- Model Armor response sanitization
- Retries for connection failures (`gws-go` currently retries HTTP 408, 429,
  and transient 5xx responses but not transport failures)
- API-version overrides
- A fallback Discovery URL for newer Google APIs
- Account-timezone discovery for calendar and workflow helpers
- Structured diagnostic logging
- File-path, terminal-control-character, and Unicode-spoofing protections

## Agent and distribution support

`gws-cli` includes checked-in agent skills, persona definitions, workflow
recipes, a `generate-skills` command, and Gemini CLI extension metadata. It is
distributed as release binaries and through npm, Homebrew, and Nix. This Go port
currently uses its Makefile to build a local binary.

## Shared foundation

Both implementations provide:

- Runtime command generation from Google Discovery documents
- Schema introspection with reference resolution
- Discovery-schema validation of JSON request bodies
- Recursive resources and methods
- A 24-hour Discovery cache
- JSON path/query parameters through `--params`
- JSON request bodies through `--json`
- JSON, table, YAML, and CSV output
- Local request previews through `--dry-run`
- Automatic pagination with page limits and delays
- Raw response output to a file
- Browser-based OAuth with PKCE
- Pre-obtained OAuth access tokens
