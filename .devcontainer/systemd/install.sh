#!/bin/bash

set -Eeuo pipefail

export DEBIAN_FRONTEND=noninteractive

log() {
  echo "[setup-devcontainer] $*"
}

apt_install() {
  apt-get update
  apt-get install -y --no-install-recommends "$@"
}

setup_systemd_safeguards() {
  log "Configuring systemd/container safeguards"

  mkdir -p /etc/systemd/system.conf.d
  mkdir -p /etc/systemd/logind.conf.d
  mkdir -p /etc/systemd/system/getty.target.wants
  mkdir -p /etc/systemd/system/sysinit.target.wants
  mkdir -p /etc/systemd/system/multi-user.target.wants
  mkdir -p /etc/systemd/system/sockets.target.wants

  # Prevent logind from touching host sessions or VT handling.
  cat >/etc/systemd/logind.conf.d/00-devcontainer.conf <<'EOF'
[Login]
NAutoVTs=0
ReserveVT=0
KillUserProcesses=no
HandlePowerKey=ignore
HandleSuspendKey=ignore
HandleHibernateKey=ignore
HandleLidSwitch=ignore
HandleLidSwitchExternalPower=ignore
HandleLidSwitchDocked=ignore
RemoveIPC=no
EOF

  # Keep defaults conservative for containers.
  cat >/etc/systemd/system.conf.d/00-devcontainer.conf <<'EOF'
[Manager]
DefaultTimeoutStartSec=15s
DefaultTimeoutStopSec=15s
LogLevel=info
EOF

  # Mask units that are useless or potentially problematic in a container/devcontainer.
  local units=(
    "getty.target"
    "console-getty.service"
    "getty@.service"
    "serial-getty@.service"
    "systemd-vconsole-setup.service"
    "systemd-logind.service"
    "user@.service"
    "systemd-user-sessions.service"
    "systemd-remount-fs.service"
    "dev-hugepages.mount"
    "sys-fs-fuse-connections.mount"
  )

  for unit in "${units[@]}"; do
    systemctl mask "${unit}" >/dev/null 2>&1 || true
  done

  # Clean default wants that often create noise/failures in containers.
  rm -f /etc/systemd/system/sysinit.target.wants/systemd-tmpfiles-setup-dev.service || true
  rm -f /etc/systemd/system/multi-user.target.wants/getty.target || true
  rm -f /etc/systemd/system/sockets.target.wants/systemd-initctl.socket || true

  # Tell systemd it is in a container.
  echo "docker" >/etc/container
}

install_systemd() {
  log "Installing systemd"
  apt_install systemd systemd-sysv dbus
}

install_docker_ce() {
  log "Installing Docker CE"

  apt_install ca-certificates curl gnupg lsb-release

  install -m 0755 -d /etc/apt/keyrings
  curl -fsSL https://download.docker.com/linux/ubuntu/gpg \
    | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
  chmod a+r /etc/apt/keyrings/docker.gpg

  . /etc/os-release
  local arch
  arch="$(dpkg --print-architecture)"

  cat >/etc/apt/sources.list.d/docker.list <<EOF
deb [arch=${arch} signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu ${VERSION_CODENAME} stable
EOF

  apt-get update
  apt-get install -y --no-install-recommends \
    docker-ce \
    docker-ce-cli \
    containerd.io \
    docker-buildx-plugin \
    docker-compose-plugin

  # Make sure docker group exists and vscode can use it.
  groupadd -f docker

  if id -u vscode >/dev/null 2>&1; then
    usermod -aG docker vscode
  fi

  # Optional but usually wanted when systemd is PID 1 in the container.
  systemctl enable docker.service >/dev/null 2>&1 || true
  systemctl enable containerd.service >/dev/null 2>&1 || true
}

install_latest_go() {
  log "Installing latest Go release from upstream"

  apt_install jq tar

  local json version goarch url tmpdir dpkg_arch
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "${tmpdir}"' RETURN

  dpkg_arch="$(dpkg --print-architecture)"
  case "${dpkg_arch}" in
    amd64) goarch="amd64" ;;
    arm64) goarch="arm64" ;;
    *)
      echo "Unsupported architecture for Go auto-install: ${dpkg_arch}" >&2
      exit 1
      ;;
  esac

  json="$(curl -fsSL https://go.dev/dl/?mode=json)"
  version="$(printf '%s' "${json}" | jq -r '.[0].version')"
  url="$(printf '%s' "${json}" | jq -r --arg arch "${goarch}" '.[] | .version as $v | .files[] | select(.os=="linux" and .arch==$arch and .kind=="archive") | .filename' | head -n1)"

  if [[ -z "${version}" || -z "${url}" || "${url}" == "null" ]]; then
    echo "Failed to determine latest Go release." >&2
    exit 1
  fi

  url="https://go.dev/dl/${url}"

  curl -fsSL "${url}" -o "${tmpdir}/go.tar.gz"
  rm -rf /usr/local/go
  tar -C /usr/local -xzf "${tmpdir}/go.tar.gz"

  cat >/etc/profile.d/go.sh <<'EOF'
export GOROOT=/usr/local/go
export PATH=/usr/local/go/bin:$PATH
EOF
  chmod 0644 /etc/profile.d/go.sh

  if ! grep -q '/usr/local/go/bin' /etc/environment 2>/dev/null; then
    echo 'PATH="/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"' >/etc/environment
  fi

  log "Installed $(/usr/local/go/bin/go version)"
}

install_go_tools_for_vscode() {
  log "Installing Go tools for vscode user"
  
  # Check if vscode user exists
  if ! id -u vscode >/dev/null 2>&1; then
    log "Warning: vscode user does not exist, skipping Go tools installation"
    return
  fi

  # Set up GOPATH and GOBIN for vscode user
  local vscode_home
  vscode_home="$(getent passwd vscode | cut -d: -f6)"
  
  if [[ -z "${vscode_home}" || ! -d "${vscode_home}" ]]; then
    log "Warning: vscode home directory not found, skipping Go tools installation"
    return
  fi

  local gopath="${vscode_home}/go"
  local gobin="${gopath}/bin"

  # Create directories with proper ownership
  mkdir -p "${gopath}" "${gobin}"
  chown -R vscode:vscode "${gopath}"

  # Install Go tools as vscode user
  su - vscode -c "
    export GOROOT=/usr/local/go
    export GOPATH=${gopath}
    export GOBIN=${gobin}
    export PATH=/usr/local/go/bin:\${GOBIN}:\${PATH}
    
    echo 'Installing lazygit...'
    /usr/local/go/bin/go install github.com/jesseduffield/lazygit@latest
    
    echo 'Installing go-mod-upgrade...'
    /usr/local/go/bin/go install github.com/oligot/go-mod-upgrade@latest

    echo 'Installing gobadge...'
    /usr/local/go/bin/go install github.com/AlexBeauchemin/gobadge@latest
  "

  log "Go tools installed successfully for vscode user"
  log "Binaries location: ${gobin}"
}

cleanup() {
  log "Cleaning up apt cache"
  apt-get clean
  rm -rf /var/lib/apt/lists/*
}

install_systemd
setup_systemd_safeguards
install_docker_ce
install_latest_go
install_go_tools_for_vscode

cleanup
