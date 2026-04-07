# Stage 1: Build
FROM golang:1.26-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o chargeghost ./cmd/chargeghost

# Stage 2: Runtime (distroless for minimal attack surface)
FROM gcr.io/distroless/static:nonroot

COPY --from=builder /build/chargeghost /chargeghost

EXPOSE 8080

ENTRYPOINT ["/chargeghost"]
