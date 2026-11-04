# Enforce that the kindest/base tag to build from must always be specified.
ARG KINDEST_BASE_TAG

FROM kindest/base:${KINDEST_BASE_TAG}

# Stable CRI-O release stream, see https://github.com/cri-o/packaging.
ARG CRIO_VERSION=v1.37

# Add CRI-O to the mix (that is, containerd), as well as skopeo for loading
# images into CRI-O's image storage without registry access.
RUN apt-get update \
    && apt-get install -y --no-install-recommends gnupg skopeo \
    && mkdir -p /etc/apt/keyrings \
    && curl -fsSL https://download.opensuse.org/repositories/isv:/cri-o:/stable:/${CRIO_VERSION}/deb/Release.key \
        | gpg --batch --yes --dearmor -o /etc/apt/keyrings/cri-o-apt-keyring.gpg \
    && echo "deb [signed-by=/etc/apt/keyrings/cri-o-apt-keyring.gpg] https://download.opensuse.org/repositories/isv:/cri-o:/stable:/${CRIO_VERSION}/deb/ /" \
        > /etc/apt/sources.list.d/cri-o.list \
    && apt-get update \
    && apt-get install -y -o Dpkg::Options::="--force-confold" cri-o \
    && systemctl enable crio \
    && systemctl mask systemd-logind.service getty.service getty.target \
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
