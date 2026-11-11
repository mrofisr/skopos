FROM golang:1.27.4@sha256:08bcb99a38f85a265f9a93d1d3323e2440eec333380f3ea26ee0dec9264bb41c AS build

WORKDIR /src
COPY . .

ARG VERSION=0.1.0-alpha.2
RUN CGO_ENABLED=0 GOOS=linux go build \
    -mod=vendor \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/gateway ./cmd/gateway && \
    CGO_ENABLED=0 GOOS=linux go build \
    -mod=vendor \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/skopos-checkpoint ./cmd/skopos-checkpoint

FROM scratch

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/gateway /gateway
COPY --from=build /out/skopos-checkpoint /skopos-checkpoint

USER 65532:65532
EXPOSE 8080 4319
ENTRYPOINT ["/gateway"]
