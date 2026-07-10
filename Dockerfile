# build
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/alert-thread-proxy .

# runtime
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/alert-thread-proxy /alert-thread-proxy
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/alert-thread-proxy"]
