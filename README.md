# iris-api

HTTP API for Iris. Auth, profiles, resumes, job search, applications, cover letters, and the Chrome extension. Starts Temporal workflows that [iris-worker](https://github.com/SomtoJF/iris-worker) executes.

## Run

Needs Docker (Postgres + Redis) and Go. Hot reload uses [CompileDaemon](https://github.com/githubnemo/CompileDaemon):

```bash
go install github.com/githubnemo/CompileDaemon@latest
```

```bash
cp sample.env .env
make run
```

That starts Postgres and Redis, then the API with hot reload on [http://localhost:4000](http://localhost:4000).

Without hot reload:

```bash
make start-docker
go run main.go
```

Migrate after schema changes (uncomment only the tables you need in `migrate/migrate.go` first):

```bash
make db-migration
```

Job applications also need Temporal (`make start-temporal-server` in iris-worker) and a running iris-worker.
