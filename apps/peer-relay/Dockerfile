# syntax=docker/dockerfile:1
FROM golang:1.22-alpine AS builder
WORKDIR /workspace

# Copy module files for caching
COPY apps/peer-relay/go.mod apps/peer-relay/go.sum ./apps/peer-relay/
RUN cd apps/peer-relay && go mod download

# Copy source
COPY apps/peer-relay/ ./apps/peer-relay/

RUN cd apps/peer-relay && CGO_ENABLED=0 GOOS=linux go build -o /peer-relay ./cmd/

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /peer-relay /peer-relay
EXPOSE 8080 8081
ENTRYPOINT ["/peer-relay"]
