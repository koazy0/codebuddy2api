FROM golang:1.25-bookworm AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -buildvcs=false -o /out/codebuddy-gateway .

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates tzdata \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=builder /out/codebuddy-gateway /app/codebuddy-gateway
COPY config.yaml.example /app/config.yaml
RUN mkdir -p /app/data /app/log
EXPOSE 8088
CMD ["/app/codebuddy-gateway", "server"]
