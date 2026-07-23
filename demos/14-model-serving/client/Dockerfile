# syntax=docker/dockerfile:1

FROM golang:1.24-bookworm AS build
WORKDIR /src

COPY go.mod ./
COPY *.go ./

RUN CGO_ENABLED=0 go test ./... \
	&& CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/client .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/client /client
USER nonroot:nonroot
ENTRYPOINT ["/client"]
