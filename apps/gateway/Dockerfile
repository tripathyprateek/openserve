# Build stage
FROM golang:1.23-alpine AS builder

ARG VERSION=dev

WORKDIR /src

COPY apps/gateway/ apps/gateway/

RUN cd apps/gateway && go mod tidy && CGO_ENABLED=0 GOOS=linux go build \
	-trimpath \
	-ldflags="-s -w -X main.version=${VERSION}" \
	-o /bin/server ./cmd/...

# Final stage
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /bin/server /bin/server

EXPOSE 8080

USER nonroot:nonroot

ENTRYPOINT ["/bin/server"]
