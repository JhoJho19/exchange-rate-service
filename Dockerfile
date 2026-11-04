FROM golang:1.27.3 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY cmd/ ./cmd/
COPY internal/ ./internal/
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/exchange-rate-service ./cmd/exchange-rate-service

FROM scratch

# Go's HTTPS client uses this trust store to verify provider certificates.
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=builder /out/exchange-rate-service /exchange-rate-service

USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/exchange-rate-service"]
