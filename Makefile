run: start-docker start

start:
	CompileDaemon -command="./iris-api" -exclude-dir="vendor"

build:
	go build -o iris-api main.go

start-docker:
	@$(MAKE) stop-docker
	docker-compose -f docker/docker-compose.yml up -d

stop-docker:
	docker-compose -f docker/docker-compose.yml down

db-migration:
	go run migrate/migrate.go

run-build:
	./iris-api

clean:
	docker stop iris-redis && docker rm iris-redis

.PHONY: run build run-build db-migration clean start-docker stop-docker start