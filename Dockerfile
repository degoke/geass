# The build stages run on the machine doing the build (BUILDPLATFORM) and cross-compile for the image's platform
# (TARGETARCH), so an arm64 image is built on an amd64 machine without emulation. The dashboard bundle is plain
# JavaScript and the same for every platform.
FROM --platform=$BUILDPLATFORM node:24 AS dashboard

WORKDIR /workspace/dashboard-ui
COPY dashboard-ui/package.json dashboard-ui/pnpm-lock.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile
COPY dashboard-ui/ ./
RUN pnpm build

# Build the manager binary
FROM --platform=$BUILDPLATFORM golang:1.26 AS builder
ARG TARGETOS
ARG TARGETARCH
# The release the operator is built as (pkg/release). It is how the cluster knows which operator it runs.
ARG VERSION=dev

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the Go source (relies on .dockerignore to filter)
COPY . .
COPY --from=dashboard /workspace/internal/dashboard/frontend/dist internal/dashboard/frontend/dist

# Build
# TARGETOS and TARGETARCH are the platform the image is built for (docker build --platform linux/arm64), which is the
# platform of the machine building it unless that is said. BuildKit, the default builder of Docker 23 and later and
# of Podman, provides them and BUILDPLATFORM.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} go build -a -ldflags "-X github.com/degoke/geass/pkg/release.Version=${VERSION}" -o manager ./cmd/main.go

# Use distroless as minimal base image to package the manager binary
# Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/manager .
USER 65532:65532

ENTRYPOINT ["/manager"]
