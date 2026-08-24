FROM registry.fedoraproject.org/fedora:42

RUN dnf install -y \
        findutils \
        gcc \
        git \
        golang \
        gzip \
        make \
        python3 \
        python3-devel \
        python3-pip \
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

ENV GOMODCACHE="/opt/elgatolight-go-mod"
COPY go.mod go.sum /tmp/elgatolight-module/
RUN cd /tmp/elgatolight-module \
    && go mod download \
    && chmod -R a+rwX /opt/elgatolight-go-mod \
    && rm -rf /tmp/elgatolight-module

ENV PATH="/opt/elgatolight-venv/bin:${PATH}"
WORKDIR /workspace
