DATABASE_URL ?= postgres://bankcore:bankcore@localhost:5432/bankcore?sslmode=disable

.PHONY: run build test tidy compose-up compose-down migrate-up migrate-down

run:
	go run ./cmd/api

build:
	go build -o bin/api ./cmd/api

test:
	go test ./...

tidy:
	go mod tidy

compose-up:
	docker compose up -d

compose-down:
	docker compose down

migrate-up:
	docker run --rm -v $(CURDIR)/migrations:/migrations --network host migrate/migrate \
		-path=/migrations -database "$(DATABASE_URL)" up

migrate-down:
	docker run --rm -v $(CURDIR)/migrations:/migrations --network host migrate/migrate \
		-path=/migrations -database "$(DATABASE_URL)" down 1
