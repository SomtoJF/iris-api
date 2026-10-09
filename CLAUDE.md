# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Migrations (migrate/migrate.go)

Migrations run via GORM AutoMigrate calls in `migrate/migrate.go`; most are kept commented out. Before running a migration: comment out every table migration unrelated to the recent DB change so ONLY the affected tables are uncommented and migrated. Leave the rest commented.

## Cross-repo model dependency

`iris-api/model` is the source of truth for database models shared with iris-worker. Keep each model in its own file; do not combine multiple model structs in one file. iris-worker must reference shared models through type aliases to `github.com/SomtoJF/iris-api/model`, not duplicate their definitions.

iris-worker imports the API module via a local `replace` and vendors it. After changing anything in `model/`, run `go mod vendor` in `../iris-worker` and commit its `vendor/` changes — worker deploy builds use `-mod=vendor` and won't pick up model changes otherwise.
