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
VOLUME /data
EXPOSE 8096
ENTRYPOINT ["/libteca", "--data", "/data", "--port", "8096"]
