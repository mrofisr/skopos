FROM golang:1.27.4@sha256:08bcb99a38f85a265f9a93d1d3323e2440eec333380f3ea26ee0dec9264bb41c AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=0.1.0-alpha.2
RUN CGO_ENABLED=0 GOOS=linux go build \
    -mod=readonly \
    -trimpath \
    -ldflags "-s -w -X github.com/jawaracloud/skopos/internal/version.Version=${VERSION}" \
    -o /out/skopos ./cmd/skopos

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/skopos /skopos

USER 65532:65532
EXPOSE 8080 4319
ENTRYPOINT ["/skopos"]
