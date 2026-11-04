FROM mcr.microsoft.com/devcontainers/base:ubuntu

# Installs systemd, Docker CE and Go, replacing the go and docker-in-docker
# devcontainer features that are incompatible with systemd as PID 1.
RUN --mount=type=bind,target=/context /context/install.sh

# Feature install scripts run "go install" and don't source /etc/profile.d.
ENV GOROOT=/usr/local/go \
    GOPATH=/go \
    PATH=/usr/local/go/bin:/go/bin:${PATH}

ENTRYPOINT [ "/usr/lib/systemd/systemd" ]
