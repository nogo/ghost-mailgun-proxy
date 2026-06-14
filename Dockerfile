FROM golang:1.26.4-alpine@sha256:7a3e50096189ad57c9f9f865e7e4aa8585ed1585248513dc5cda498e2f41812c AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /proxy .

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /proxy /proxy
USER 65532:65532
EXPOSE 8787
HEALTHCHECK --interval=30s --timeout=5s --retries=3 CMD ["/proxy", "-healthcheck"]
ENTRYPOINT ["/proxy"]
