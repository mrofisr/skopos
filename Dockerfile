# syntax=docker/dockerfile:1

FROM --platform=$BUILDPLATFORM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build

ARG TARGETOS
ARG TARGETARCH

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=0.0.1-alpha
RUN CGO_ENABLED=0 GOOS="${TARGETOS:-linux}" GOARCH="${TARGETARCH:-$(go env GOARCH)}" go build \
    -mod=readonly \
    -trimpath \
    -ldflags "-s -w -X github.com/mrofisr/skopos/internal/version.Version=${VERSION}" \
    -o /out/skopos ./cmd/skopos

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/skopos /skopos

USER 65532:65532
EXPOSE 8080 4319
ENTRYPOINT ["/skopos"]
