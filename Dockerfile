FROM golang:1.23 as builder

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
COPY config/assets out/config/assets
COPY scripts/ scripts/
COPY Makefile Makefile
RUN make docker-prepare
#
# # Use distroless as minimal base image to package the manager binary
# # Refer to https://github.com/GoogleContainerTools/distroless for more details
FROM alpine:3.21
WORKDIR /
COPY --from=builder /workspace/out/ /

USER 65532:65532

ENTRYPOINT ["/manager"]
