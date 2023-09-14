# Enforce that the kindest/base tag to build from must always be specified.
ARG KINDEST_BASE_TAG

FROM kindest/base:${KINDEST_BASE_TAG}

# add cri-o to the mix (that is, containerd)
RUN OS=Debian_11 && VERSION=1.28 \
    && apt-get update \
    && apt-get install -y --no-install-recommends gnupg \
    && echo "deb [signed-by=/usr/share/keyrings/libcontainers-archive-keyring.gpg] https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable/$OS/ /" \
        > /etc/apt/sources.list.d/devel:kubic:libcontainers:stable.list \
    && echo "deb [signed-by=/usr/share/keyrings/libcontainers-crio-archive-keyring.gpg] https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable:/cri-o:/$VERSION/$OS/ /" \
        > /etc/apt/sources.list.d/devel:kubic:libcontainers:stable:cri-o:$VERSION.list \
    && mkdir -p /usr/share/keyrings \
    && curl -L https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable/$OS/Release.key  \
        | gpg --dearmor -o /usr/share/keyrings/libcontainers-archive-keyring.gpg \
    && curl -L https://download.opensuse.org/repositories/devel:/kubic:/libcontainers:/stable:/cri-o:/$VERSION/$OS/Release.key \
        | gpg --dearmor -o /usr/share/keyrings/libcontainers-crio-archive-keyring.gpg \
    && apt-get update \
    && apt-get upgrade -y \
    && apt-get install -y -o Dpkg::Options::="--force-confold" cri-o cri-o-runc \
    && systemctl enable crio \
    && apt-get clean -y \
    && rm -rf \
        /var/cache/debconf/* \
        /var/lib/apt/lists/* \
        /var/log/* \
        /tmp/* \
        /var/tmp/* \
        /usr/share/doc/* \
        /usr/share/doc-base/* \
        /usr/share/man/* \
        /usr/share/local/*
        
# Complete and also modify the kindest/base configuration.
COPY files/ /
