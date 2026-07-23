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
| Registered services | 18 | Docs, Calendar, Slides, Gmail, and Drive |
| Gmail access | Read and write | Read-only OAuth grant |
| Helper commands | 25 service helpers and workflows | None |
| Authentication | OAuth setup/login/export, encrypted keyring, service accounts, ADC | Desktop OAuth login and access-token environment variable |
| Output | JSON, table, YAML, and CSV | JSON and raw file output |
| Media transfer | Multipart uploads and media downloads | Multipart uploads for Discovery methods that support them |
| Schema support | Introspection and request-body validation | Parameter validation and request construction |
| Reliability | Retries for rate limits and transient network failures | Standard HTTP client behavior |
| Agent tooling | Generated skills, personas, recipes, Gemini extension | None |
| Response safety | Model Armor and terminal sanitization | Response-size limits |

## Additional services in `gws-cli`

Beyond the APIs currently registered by `gws-go`, `gws-cli` supports:

- Google Sheets
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
- Calendar: `+insert`, `+agenda`
- Sheets: `+append`, `+read`
- Docs: `+write`
- Drive: `+upload`
- Chat: `+send`
- Apps Script: `+push`
- Workspace Events: `+subscribe`, `+renew`
- Model Armor: `+sanitize-prompt`, `+sanitize-response`, `+create-template`
- Workflows: `+standup-report`, `+meeting-prep`, `+email-to-task`,
  `+weekly-digest`, `+file-announce`

## Authentication and credentials

Features present in `gws-cli` but not yet in this repository include:

- Automated `gcloud`-based project and OAuth setup
- Service-oriented and preset scope selection
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

- `gws schema` introspection with reference resolution
- Discovery-schema validation of JSON request bodies
- Table, YAML, and CSV output formats
- Automatic media-download handling
- Model Armor response sanitization
- Retries for HTTP 429 responses, connection failures, and timeouts
- Structured JSON errors with distinct exit codes
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
- Recursive resources and methods
- A 24-hour Discovery cache
- JSON path/query parameters through `--params`
- JSON request bodies through `--json`
- Local request previews through `--dry-run`
- Automatic pagination with page limits and delays
- Raw response output to a file
- Browser-based OAuth with PKCE
- Pre-obtained OAuth access tokens

