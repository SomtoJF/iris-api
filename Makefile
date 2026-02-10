run:
	CompileDaemon -command="go run main.go" -build="go build -o iris-api main.go" -exclude-dir="vendor"

build:
	go build -o iris-api main.go

run-build:
	./iris-api

.PHONY: run build run-build