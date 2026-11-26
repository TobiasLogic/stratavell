FROM golang:1.27.4-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -buildvcs=false \
      -ldflags "-s -w -buildid= -X github.com/TobiasLogic/stratavell/server.Version=${VERSION}" \
      -o /out/stratavell ./cmd/stratavell \
 && mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
ARG VERSION=dev
LABEL org.opencontainers.image.title="Stratavell" \
      org.opencontainers.image.description="Context management engine for long-running LLM agents" \
      org.opencontainers.image.source="https://github.com/TobiasLogic/stratavell" \
      org.opencontainers.image.licenses="Apache-2.0" \
      org.opencontainers.image.version="${VERSION}" \
      io.modelcontextprotocol.server.name="io.github.TobiasLogic/stratavell"
COPY --from=build /out/stratavell /usr/local/bin/stratavell
COPY --from=build --chown=65532:65532 /out/data /data
WORKDIR /data
VOLUME ["/data"]
EXPOSE 7077
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/stratavell"]
CMD ["mcp", "--db", "/data/memory.db"]
