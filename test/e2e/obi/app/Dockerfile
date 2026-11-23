# syntax=docker/dockerfile:1

FROM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
WORKDIR /src
COPY go.mod main.go ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /out/e2e-server .

FROM scratch
COPY --from=build /out/e2e-server /app/e2e-server
EXPOSE 8080
ENTRYPOINT ["/app/e2e-server"]
