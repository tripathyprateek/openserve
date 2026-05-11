# Build stage
# Build context must be the repo root so the operator replace directive resolves.
FROM golang:1.24-alpine AS builder

ARG VERSION=dev

WORKDIR /src

RUN apk add --no-cache git ca-certificates

# Copy both modules — control-api depends on operator via replace directive.
# The operator's zz_generated.deepcopy.go is pre-generated; no controller-gen needed.
COPY operator/ operator/
COPY apps/control-api/ apps/control-api/

# Tidy resolves the replace directive, generates go.sum, downloads all deps.
RUN cd apps/control-api && go mod tidy

# Build the server binary
RUN cd apps/control-api && CGO_ENABLED=0 GOOS=linux go build \
	-trimpath \
	-ldflags="-s -w -X main.version=${VERSION}" \
	-o /bin/server ./cmd/server/...

# Final stage
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/server /bin/server

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/bin/server"]
