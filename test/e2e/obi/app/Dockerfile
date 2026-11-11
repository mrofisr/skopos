# syntax=docker/dockerfile:1

FROM golang:1.27.4@sha256:08bcb99a38f85a265f9a93d1d3323e2440eec333380f3ea26ee0dec9264bb41c AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/e2e-server .

FROM scratch
COPY --from=build /out/e2e-server /app/e2e-server
EXPOSE 8080
ENTRYPOINT ["/app/e2e-server"]
