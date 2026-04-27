FROM mcr.microsoft.com/devcontainers/base:ubuntu-24.04

# As we cannot use devcontainer features here, we're running the installation
# scripts directly from the build context without copying them first into the
# layer.
RUN --mount=type=bind,target=/context /context/install.sh \
    && TAGS=matchers ROOT=true NUM_PROGRAMS=1 RACE=true VERBOSE=true HTML=true \
        /context/gocover-install.sh

# Note: do not install any devcontainer features as this will break the entry
# point.
ENTRYPOINT [ "/usr/lib/systemd/systemd" ]
