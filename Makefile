run:
	CompileDaemon -command="go run main.go" -build="go build -o iris-api main.go" -exclude-dir="vendor"

build:
	go build -o iris-api main.go

db-migration:
	go run migrate/migrate.go

run-build:
	./iris-api

.PHONY: run build run-build db-migration