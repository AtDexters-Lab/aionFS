CONTAINER_TOOL ?= podman
IMAGE ?= aionfs-devd:dev
DATA_DIR ?= $(PWD)/data
TOKEN_FILE ?= $(PWD)/tokens.json
CERT_DIR ?= $(PWD)/certs

.PHONY: build test run container run-container clean gen-dev-certs gen-token fmt lint

build:
	go build ./...

test:
	go test ./...

run:
	go run ./cmd/aionfs-devd -listen 127.0.0.1:7081 -data-dir $(DATA_DIR)

container:
	$(CONTAINER_TOOL) build -t $(IMAGE) -f Dockerfile .

run-container: container
	$(CONTAINER_TOOL) run --rm -p 7080:7080 -v $(DATA_DIR):/srv/aionfs/data:Z \
		$(if $(wildcard $(TOKEN_FILE)),-v $(TOKEN_FILE):/etc/aionfs/tokens.json:ro,) \
		$(if $(wildcard $(CERT_DIR)),-v $(CERT_DIR):/etc/aionfs/certs:ro,) \
		$(IMAGE) \
		-data-dir /srv/aionfs/data \
		$(if $(TOKEN_FILE),-token-file /etc/aionfs/tokens.json,) \
		$(if $(and $(wildcard $(CERT_DIR)/server.pem),$(wildcard $(CERT_DIR)/server-key.pem)),-tls-cert /etc/aionfs/certs/server.pem -tls-key /etc/aionfs/certs/server-key.pem,) \
		$(if $(wildcard $(CERT_DIR)/client-ca.pem),-tls-client-ca /etc/aionfs/certs/client-ca.pem,)

fmt:
	gofmt -w ./cmd ./internal

clean:
	rm -rf $(DATA_DIR) $(CERT_DIR) tokens.json

