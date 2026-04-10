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
	docker stop iris-redis && docker rm iris-redis && docker stop iris-postgres && docker rm iris-postgres

# Docker image (context: repo root). Run: make docker-build && make docker-run
docker-build:
	docker build -f docker/Dockerfile -t iris-api:local .

# REDIS_HOST points at host Redis by default. Temporal dials localhost:7233 in code; on Linux use
# make docker-run-host if Temporal is on the host, or run both in compose on one network.
docker-run:
	docker run --rm -p 4000:4000 \
		--add-host=host.docker.internal:host-gateway \
		-e RENDER=true \
		-e REDIS_HOST=$(or $(REDIS_HOST),host.docker.internal:6379) \
		iris-api:local

# Linux: Temporal/Redis on localhost. Docker Desktop on Mac does not support host networking.
docker-run-host:
	docker run --rm --network host \
		-e RENDER=true \
		-e REDIS_HOST=127.0.0.1:6379 \
		iris-api:local

.PHONY: run build run-build db-migration clean start-docker stop-docker start docker-build docker-run docker-run-host