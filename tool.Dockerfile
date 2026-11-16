FROM golang:1.27.4@sha256:fe517acaa580cc7915457080f850c37e618b3c1e750f98b58fc6429b605147a1 AS builder

WORKDIR /workspace
# ENV GOPROXY https://goproxy.cn
# Copy the Go Modules manifests
# # cache deps before building and copying source so that we don't need to re-download as much
# # and so that source changes don't invalidate our downloaded layer
#
# # Copy the go source
COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download
#
# # Build
COPY main.go main.go
COPY apis/ apis/
COPY pkg/ pkg/
COPY tool/ tool/
COPY config/assets/templates out/config/assets/templates
COPY scripts/ scripts/
COPY Makefile Makefile
RUN make docker-tool-prepare
#
# # Use distroless as minimal base image to package the manager binary
# # Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM gcr.io/distroless/static:nonroot
WORKDIR /
COPY --from=builder /workspace/out/tool /

USER 65532:65532
