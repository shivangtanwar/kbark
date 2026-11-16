# syntax=docker/dockerfile:1
# Image for running `kbark serve --in-cluster`. See deploy/kbark.yaml.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X github.com/shivangtanwar/kbark/internal/version.Version=${VERSION}" \
    -o /out/kbark ./cmd/kbark

# distroless/static carries CA certificates (needed to reach an AI
# provider) and a non-root user, and nothing else.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/kbark /kbark
USER 65532:65532
ENTRYPOINT ["/kbark"]
CMD ["serve", "--in-cluster", "--addr", "0.0.0.0:8765", "--insecure-no-auth", "--audit-log", "/var/lib/kbark/audit.jsonl", "--state", "/var/lib/kbark/state.json"]
