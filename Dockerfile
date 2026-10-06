FROM docker.io/library/golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452 AS dev

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        build-essential \
        ca-certificates \
        git \
        gzip \
        jq \
        make \
        openssl \
        tar \
        zip \
    && rm -rf /var/lib/apt/lists/*

ENV GOMODCACHE=/opt/elgatolight-go-mod
COPY go.mod go.sum /tmp/elgatolight-module/
RUN mkdir -p /opt/elgatolight-go-mod \
    && cd /tmp/elgatolight-module \
    && go mod download \
    && chmod -R a+rwX /opt/elgatolight-go-mod \
    && rm -rf /tmp/elgatolight-module

WORKDIR /workspace
COPY . .

FROM dev AS build
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -buildvcs=false -trimpath \
    -ldflags "-s -w -X git2.riper.fr/ztec/elgatocmd/internal/buildinfo.Version=${VERSION}" \
    -o /out/elgatolight ./cmd/elgatolight

FROM docker.io/library/debian:bookworm-slim@sha256:7c7b2c966bc9ee8cedfeef67e0e279108992c77681fa595db4a9d65c06ccc587 AS runtime
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/elgatolight /usr/local/bin/elgatolight
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/elgatolight"]
