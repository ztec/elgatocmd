FROM docker.io/library/golang:1.26.6-bookworm@sha256:116d58cbd88c1297624acc6e967a060012422bacf9930927e23fb719189c6f36 AS go-toolchain

FROM registry.fedoraproject.org/fedora:42@sha256:63773f454664cd77e239f8e0b13ae7f18effe9e3d6612a325b5646eb3bda11f1 AS dev

COPY --from=go-toolchain /usr/local/go /usr/local/go
RUN dnf install --assumeyes \
        ca-certificates \
        findutils \
        gcc \
        git \
        gzip \
        jq \
        make \
        openssl \
        python3 \
        python3-devel \
        tar \
        zip \
    && dnf clean all \
    && rm -rf /var/cache/dnf

COPY requirements-dev.txt /tmp/elgatolight-requirements-dev.txt
RUN python3 -m venv /opt/elgatolight-venv \
    && /opt/elgatolight-venv/bin/python -m pip install --no-cache-dir --upgrade pip \
    && /opt/elgatolight-venv/bin/python -m pip install --no-cache-dir \
        --requirement /tmp/elgatolight-requirements-dev.txt \
    && rm /tmp/elgatolight-requirements-dev.txt

ENV PATH=/opt/elgatolight-venv/bin:/usr/local/go/bin:${PATH}
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

FROM docker.io/library/debian:bookworm-slim@sha256:88200866dfff7ea7f5cbcb6ec7c8a701889efe6fe859fe64d6990e4b07ea4171 AS runtime
RUN apt-get update \
    && apt-get install --yes --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/elgatolight /usr/local/bin/elgatolight
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/elgatolight"]
