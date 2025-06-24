FROM --platform=$BUILDPLATFORM golang:1.24 AS builder

ARG TARGETOS
ARG TARGETARCH
ARG VERSION
ARG COMMIT_HASH
ARG BIN_NAME=glesha

ENV CGO_ENABLED=0 \
    GOOS=$TARGETOS \
    GOARCH=$TARGETARCH

WORKDIR /app
COPY . .

RUN if [ "$TARGETOS" = "windows" ]; then SUFFIX=".exe"; else SUFFIX=""; fi && \
    OUTFILE="/app/${BIN_NAME}${SUFFIX}" && \
    go build -ldflags="-X 'glesha/cmd/version_cmd.version=${VERSION}' -X 'glesha/cmd/version_cmd.commitHash=${COMMIT_HASH}' -X 'glesha/logger/logger.printCallerLocation=false'" \
    -o "$OUTFILE" .

FROM scratch AS export
COPY --from=builder /app/glesha* /out/
