# Build stage
FROM golang:1.22-alpine AS builder

ARG VERSION=dev

WORKDIR /src

# Copy source (go mod tidy will fetch deps and generate go.sum)
COPY operator/ operator/

# Tidy, download deps, and build
RUN cd operator && go mod tidy && CGO_ENABLED=0 GOOS=linux go build \
	-trimpath \
	-ldflags="-s -w" \
	-o /bin/manager ./cmd/...

# Final stage
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/manager /bin/manager

EXPOSE 8080 8081

USER nonroot:nonroot

ENTRYPOINT ["/bin/manager"]
