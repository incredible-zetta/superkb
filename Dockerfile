# ── build stage ──────────────────────────────────────────────────────────────
FROM golang:1.23-alpine AS build
WORKDIR /src

# Cache deps first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api

# ── runtime stage ────────────────────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates poppler-utils && adduser -D -u 10001 superkb
USER superkb
WORKDIR /app
COPY --from=build /out/api /app/api
EXPOSE 8080
ENTRYPOINT ["/app/api"]
