BIN := libteca
WEBDIST := internal/server/webdist
BENCH_SEED_DIR ?= data/bench-seed
SEED_KIND ?= audio
SEED_COUNT ?= 10000

.PHONY: build web test clean run smoke record corpus bench bench-idle bench-scan bench-transcode seed

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
