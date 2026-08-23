FROM golang:1.25-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o briefly .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
RUN mkdir -p data/posts data/thumbs
COPY --from=builder /build/briefly .
COPY --from=builder /build/static ./static
EXPOSE 3000
VOLUME ["/app/data"]
CMD ["./briefly"]
