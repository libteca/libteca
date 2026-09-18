FROM node:22-alpine AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm install --no-fund --no-audit
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist internal/server/webdist
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/libteca ./cmd/libteca

FROM alpine:3.22
RUN apk add --no-cache ffmpeg
COPY --from=build /out/libteca /libteca

# Audit 8 F32: run as a dedicated unprivileged identity instead of the image's
# default root. Only /data (database, covers, backups, transcode/subtitle
# caches) must be writable; media library mounts can be read-only. Existing
# bind mounts chowned by an earlier root-mode deployment need a one-time
# `chown -R 10001:10001 <data>` on the host — the application deliberately
# does not chown host paths itself. Named volumes initialize from the image
# and need nothing.
RUN addgroup -S -g 10001 libteca \
 && adduser -S -D -H -u 10001 -G libteca libteca \
 && mkdir -p /data \
 && chown 10001:10001 /data

VOLUME /data
EXPOSE 8096
USER 10001:10001
ENTRYPOINT ["/libteca", "--data", "/data", "--port", "8096"]
