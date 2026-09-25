FROM node:24-alpine AS frontend
WORKDIR /frontend
COPY package.json package-lock.json ./
RUN npm ci
COPY static ./static
COPY scripts ./scripts
RUN npm run build

FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=frontend /frontend/static ./static
# Frontend must be built before the Go binary; go:embed consumes this tree.
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o briefly .

FROM alpine:3.22
# Сертификаты для исходящих запросов к провайдерам, wget — для HEALTHCHECK,
# tini — init-процесс: зомби от дочерних процессов не копятся.
RUN apk add --no-cache ca-certificates tzdata tini \
 && addgroup -S briefly && adduser -S -G briefly -H briefly
WORKDIR /app
COPY --from=builder /build/briefly ./briefly
# Frontend is embedded in the binary; no static directory is copied into the runtime image.
# data/ — volume: владелец задаётся заранее, чтобы bind-mount с chown на хосте не требовался.
RUN mkdir -p data/posts data/thumbs && chown -R briefly:briefly /app
USER briefly
HEALTHCHECK --interval=30s --timeout=5s --start-period=15s --retries=3 \
  CMD wget -qO- "http://127.0.0.1:${BRIEFLY_PORT:-3000}/api/ready" >/dev/null 2>&1 || exit 1
EXPOSE 3000
VOLUME ["/app/data"]
# tini как PID 1: форвардит сигналы и пожинает зомби-процессы.
ENTRYPOINT ["/sbin/tini", "--"]
CMD ["./briefly"]
