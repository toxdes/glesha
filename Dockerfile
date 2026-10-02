FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS compile
ARG TARGETARCH
ARG VERSION
ARG GIT_SHA
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} go build -trimpath \
    -ldflags "-s -w -X main.VERSION=${VERSION} -X main.GIT_SHA=${GIT_SHA}" -o /build/glesha .

FROM debian:bookworm-slim AS package
RUN apt-get update && apt-get install -y --no-install-recommends python3 dpkg-dev rpm tar \
    && rm -rf /var/lib/apt/lists/*
ARG TARGETARCH
ARG VERSION
ARG GIT_SHA
ARG INCLUDE_APPIMAGE=0
ENV TARGETARCH=${TARGETARCH} VERSION=${VERSION} GIT_SHA=${GIT_SHA} INCLUDE_APPIMAGE=${INCLUDE_APPIMAGE}
WORKDIR /build
COPY release.toml version.txt LICENSE ./
COPY --from=compile /build/glesha ./glesha
COPY yesb/package.py /package.py
RUN python3 /package.py

FROM scratch
COPY --from=package /output/ /
