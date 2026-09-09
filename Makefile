BIN := libteca
WEBDIST := internal/server/webdist

.PHONY: build web test clean run smoke

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
