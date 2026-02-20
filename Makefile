run:
	CompileDaemon -command="./iris-api" -exclude-dir="vendor"

build:
	go build -o iris-api main.go

db-migration:
	go run migrate/migrate.go

run-build:
	./iris-api

.PHONY: run build run-build db-migration