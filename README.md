[![Siemens](https://img.shields.io/badge/github-siemens-009999?logo=github)](https://github.com/siemens)
[![Industrial Edge](https://img.shields.io/badge/github-industrial%20edge-e39537?logo=github)](https://github.com/industrial-edge)
[![Edgeshark](https://img.shields.io/badge/github-Edgeshark-003751?logo=github)](https://github.com/siemens/edgeshark)

# Turtlefinder Container Engine Discovery

[![PkgGoDev](https://pkg.go.dev/badge/github.com/siemens/turtlefinder)](https://pkg.go.dev/github.com/siemens/turtlefinder)
[![GitHub](https://img.shields.io/github/license/siemens/turtlefinder)](https://img.shields.io/github/license/siemens/turtlefinder)
![build and test](https://github.com/siemens/turtlefinder/actions/workflows/buildandtest.yaml/badge.svg?branch=main)
![goroutines](https://img.shields.io/badge/go%20routines-not%20leaking-success)
![file descriptors](https://img.shields.io/badge/file%20descriptors-not%20leaking-success)
[![Go Report Card](https://goreportcard.com/badge/github.com/siemens/turtlefinder)](https://goreportcard.com/report/github.com/siemens/turtlefinder)
![Coverage](https://img.shields.io/badge/Coverage-87.7%25-brightgreen)

> 🐢🐘 ["Turtles all the way down"
> (Wikipedia)](https://en.wikipedia.org/wiki/Turtles_all_the_way_down)

`turtlefinder` is a Go module that discovers various container engines in a
Linux host, including container engines that have been put into containers. If
you consider such configurations to be rarer than rare, then please take a look
at KinD ([Kubernetes-in-Docker](https://kind.sigs.k8s.io/)) as well as Docker
Desktop on WSL2 ([Windows Subsystem for
Linux](https://en.wikipedia.org/wiki/Windows_Subsystem_for_Linux)).

It supports the following container engines:
- Docker
- containerd (both native API as well as CRI Event PLEG API)
- CRI-O (CRI Event PLEG API)
- podman (only when socket-activated and via Docker-compatible API)

The `turtlefinder` package originates from
[Ghostwire](https://github.com/siemens/ghostwire) (part of the Edgeshark
project) and has been carved out in order to foster easy reuse in other projects
without the need for importing the full Ghostwire module.

## Usage: One to ~~Bind~~ Find Them All

Simply create a _single_ turtlefinder. No need to create individual
“containerizers” ([lxkns](https://github.com/thediveo/lxkns)' word for things
that  discover the current container workload of an container engine) and then
stitching them together, such as when dealing with Docker and containerd
simultaneously.

```go
enginectx, cancel := context.WithCancel(context.Background())
containerizer := turtlefinder.New(
  func() context.Context { return enginectx },
  /* options... */
)
```

Whenever a turtlefinder finds a new container engine process, it tries to talk
sense to it and discover and track its container workload. In order to shut down
such engine workload background tracking (watching) a turtlefinder expects us to
supply it with a suitable “background” context; one we preferably have control
over. So this is, what the first parameter to `New` is.

For further options, please refer to the module documentation.

## Project Structure

The "Edgeshark" project consist of several repositories:
- [Edgeshark Hub repository](https://github.com/siemens/edgeshark)
- [G(h)ostwire discovery service](https://github.com/siemens/ghostwire)
- [Packetflix packet streaming service](https://github.com/siemens/packetflix)
- [Containershark Extcap plugin for
  Wireshark](https://github.com/siemens/cshargextcap)
- support modules:
  - 🖝 **Turtlefinder** 🖜
  - [csharg (CLI)](https://github.com/siemens/csharg)
  - [mobydig](https://github.com/siemens/mobydig)
  - [ieddata](https://github.com/siemens/ieddata)

## Mode of Operation

Finding container engines works in principle as follows:

1. detect long-running engines (also commonly refered to as "demons"):
   1. scan the process tree for processes with known names (such as `dockerd`,
      `containerd`, `cri-o`, et cetera). The well-known process names are
      supplied by a set of built-in "detectors" in form of sub-packages of the
      `github.com/siemens/turtlefinder/detector` package.
   2. scan matching processes for file descriptors referencing listening unix
      domain sockets: we assume them to be potential container engine API
      endpoints.
2. detect
   [socket-activated](https://0pointer.de/blog/projects/socket-activation.html)
   engines:
   1. scan the process tree for socket-activating processes with known names,
      especially `systemd`. Again, the well-known process names are supplied by
      a set of build-in socket-activator detectors in form of sub-packages of
      the `github.com/siemens/turtlefinder/activator` package.
   2. scan matching processes for file descriptors referencing listening unix
      domain sockets with well-known suffixes, such as `podman.sock`. While this
      is slightly less efficient compared with directly using well-known
      absolute socket API paths, our approach is much more powerful as it finds
      suffix-matching API endpoints even in containers. This scanning happens
      only for a newly found socket activator or when we detect a change in the
      socket activator's socket configuration (such as after a configuration
      reload).
   3. activate the services ("don't call them _demons_") behind the API sockets
      and wait for the ~~demons~~service processes to appear, before proceeding
      with talking to these endpoints. (The rationale is that we need the engine
      PIDs for the turtlefinder hierarchy detection to work.)
3. try to talk sense to the API endpoints found; this won't be always the case,
   such as when we trip on metrics endpoints, and other strange endpoints. Where
   we succeed, we add the engine found to our list of engines to watch.

Additionally, we also do engine pruning when we can't find a particular engine
process anymore after a more recent process tree scan.

Furthermore, we do some fancy things during workload discovery in order to
figure out how container engines might have been stuck into containers of
another container engine: that is, the hierarchy of container engines. This is
especially useful for such system configurations as KinD clusters and
Development Containers with Docker-in-Docker.

## DevContainer

Do yourself a favor, tinker with this Go module in a devcontainer; this gives
you a controlled and somewhat isolated environment.

> [!CAUTION]
>
> Do **not** use VSCode's "~~Dev Containers: Clone Repository in Container
> Volume~~" command, as it is utterly broken by design, ignoring
> `.devcontainer/devcontainer.json`.

1. `git clone https://github.com/siemens/turtlefinder`
2. in VSCode: Ctrl+Shift+P, "Dev Containers: Open Workspace in Container..."
3. select `turtlefinder.code-workspace`,
4. then select the (default) "turtlefinder (docker-in-docker)" configuration,
   and off you go...

# Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md).

## License and Copyright

(c) Siemens AG 2023‒26

[SPDX-License-Identifier: MIT](LICENSE)
