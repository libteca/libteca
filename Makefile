BIN := libteca
WEBDIST := internal/server/webdist
BENCH_SEED_DIR ?= data/bench-seed
SEED_KIND ?= audio
SEED_COUNT ?= 10000

.PHONY: build web test clean run smoke record corpus bench bench-idle bench-scan bench-transcode seed docker

docker:
	docker build -t libteca:dev .

build: web
	go build -o $(BIN) ./cmd/libteca

web:
	cd web && npm install --no-fund --no-audit && npx tsc --noEmit && npm run build
	rm -rf $(WEBDIST) && mkdir $(WEBDIST) && cp -r web/dist/* $(WEBDIST)/

run: build
	./$(BIN) --data ./data --port 8096

test:
	go vet ./...
	go test ./...

clean:
	rm -rf $(WEBDIST) web/dist $(BIN)

record:
	@echo "Point the client app's HTTP proxy at this machine, then run one of:"
	@echo "  LIBTECA_RECORD_FACE=abs LIBTECA_RECORD_HOST=<abs-host> mitmdump -s tools/record/record.py"
	@echo "  LIBTECA_RECORD_FACE=jellyfin LIBTECA_RECORD_HOST=<jf-host> mitmdump -s tools/record/record.py"
	@echo "Optional: LIBTECA_RECORD_DIR=<dir> (default testcorpus/<face>/raw)"
	@echo "Then: make corpus"

corpus:
	python3 tools/record/sanitize.py

# PLAN §10 benchmarks (tools/bench is standalone; cmd/libteca untouched).
# Results land in bench/results/<date>-<kind>.json (gitignored).
bench: bench-idle bench-scan bench-transcode

bench-idle:
	go run ./tools/bench idle

bench-scan:
	go run ./tools/bench scan

bench-transcode:
	go run ./tools/bench transcode

seed:
	go run ./tools/seed --kind $(SEED_KIND) --count $(SEED_COUNT) --out $(BENCH_SEED_DIR)/$(SEED_KIND)

# Release (PLAN §10): cross-compile CGo-free, tar.gz (linux) / zip (darwin),
# SHA256SUMS over all archives. Archives carry binary + systemd unit + README.
# Version stamps main.version; `libteca --version` prints it.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST := dist/release
PLATFORMS := linux-amd64 linux-arm64 darwin-amd64 darwin-arm64

.PHONY: release checksums

release: web $(PLATFORMS:%=release-%) checksums

checksums: $(PLATFORMS:%=release-%)
	mkdir -p $(DIST) && cd $(DIST) && shasum -a 256 libteca_$(VERSION)_*.tar.gz libteca_$(VERSION)_*.zip > SHA256SUMS

# Depends on web directly: as a sibling prerequisite of `release`, a -j run
# could embed webdist before the web build/copy finished.
release-%: web
	@set -e; os=$(word 1,$(subst -, ,$*)); arch=$(word 2,$(subst -, ,$*)); \
	name=libteca_$(VERSION)_$${os}_$${arch}; \
	rm -rf $(DIST)/$$name; mkdir -p $(DIST)/$$name; \
	CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/$$name/$(BIN) ./cmd/libteca; \
	cp deploy/systemd/libteca.service $(DIST)/$$name/; cp README.md $(DIST)/$$name/; \
	if [ $${os} = linux ]; then \
		(cd $(DIST) && COPYFILE_DISABLE=1 tar -czf $${name}.tar.gz $${name}); \
	else \
		(cd $(DIST) && zip -qr $${name}.zip $${name}); \
	fi; \
	rm -rf $(DIST)/$$name

