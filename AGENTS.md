# Repository Guidelines

## Project Structure & Module Organization
Piccolo Storage is the service that maintains federated checkpoints and recovery bundles. Keep the executable entry point in `cmd/storage-service/main.go` and group shareable packages under `internal/`. Use `internal/federation` for peer membership + quorum logic, `internal/checkpoint` for capsule metadata + content addressing, `internal/vault` for TPM bootstrap handling, and `internal/api` for gRPC/HTTP handlers. Configuration samples live in `configs/`, durable fixtures in `testdata/`, and exploratory notebooks or scripts in `tools/` so the runtime tree stays lean.

## Build, Test, and Development Commands
- `go build ./cmd/storage-service` compiles the daemon against Go 1.24; run before every commit to catch interface drift.
- `go test ./...` runs unit and integration suites; add `-run TestName` for focused debugging.
- `golangci-lint run` enforces gofmt, gofumpt, staticcheck, and security linters; install via `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`.
- `make dev` (create if missing) should wrap lint + test so agents can execute a single pre-push check.

## Coding Style & Naming Conventions
Follow idiomatic Go: gofmt/goimports enforced through `golangci-lint`. Use `CamelCase` for exported types, `lowerCamel` for locals, and prefix interfaces with the capability (`FederationStore`, `CheckpointWriter`). Keep packages focused and single-word. Avoid global state; wire dependencies through constructors that accept interfaces for testability. Centralize feature flags and environment toggles in `internal/config`.

## Testing Guidelines
Unit tests belong next to the code as `*_test.go` using the standard library testing package plus `stretchr/testify` for assertions/mocks. Mark expensive scenarios with `//go:build integration` and run them via `go test -tags=integration ./...` before release branches. Maintain ≥85% coverage for core packages (`internal/federation`, `internal/checkpoint`); document gaps in `TESTING.md`. Store fixtures under mirrored directories in `testdata/` and keep them deterministic.

## Commit & Pull Request Guidelines
Use `area: summary` subject lines (e.g., `checkpoint: add bloom filter for replica lag`) capped at 72 characters, followed by a concise body explaining rationale and validation. Reference issues as `Refs #123` in the footer. Every PR must include: purpose paragraph, testing notes (commands run), federated risk callouts, and screenshots for CLI/API changes when applicable. Request at least one reviewer from the storage rotation and wait for lint/test CI to pass before merging.

## Security & Configuration Tips
Never commit real federation credentials or TPM artifacts; use `.envrc.example` with placeholder values. Ensure new code respects TPM availability checks—gated by capability probes—and logs without leaking secrets. When adding dependencies, prefer vetted libraries with AGPL-compatible licenses and record their purpose in `docs/dependency-catalog.md`.
