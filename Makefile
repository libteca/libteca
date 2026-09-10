BIN := libteca
WEBDIST := internal/server/webdist

.PHONY: build web test clean run smoke record corpus

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
