[![Siemens](https://img.shields.io/badge/github-siemens-009999?logo=github)](https://github.com/siemens)
[![Industrial Edge](https://img.shields.io/badge/github-industrial%20edge-e39537?logo=github)](https://github.com/industrial-edge)
[![Edgeshark](https://img.shields.io/badge/github-Edgeshark-003751?logo=github)](https://github.com/siemens/edgeshark)

# Turtlefinder Container Engine Discovery

![Coverage](https://img.shields.io/badge/Coverage-86.3%25-brightgreen)

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

The `turtlefinder` package originates from
[Ghostwire](https://github.com/siemens/ghostwire) (Edgeshark project) and has
been carved out in order to foster easy reuse in other projects without the need
for importing the full Ghostwire module.

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

## Mode of Operation

Finding container engines works in principle as follows:

1. scan the process tree for processes with known names (such as `dockerd`, et
   cetera). The well-known process names are supplied by a set of built-in
   "detectors" in form of sub-packages of the
   `github.com/siemens/turtlefinder/detector` package.
2. scan matching processes for file descriptors referencing listening unix
   domain sockets: we assume them to be potential container engine API
   endpoints.
3. try to talk sense to the endpoints found; this won't be always the case, such
   as when we trip on metrics endpoints, and other strange endpoints. Where we succeed, we add the engine found to our list of engines to watch.

Additionally, we also do engine pruning when we can't find a particular engine
process anymore after a later process tree scan.

Furthermore, we do some fancy things during workload discovery in order to
figure out how container engines might have been stuck into containers of
another container engine: that is, the hierarchy of container engines. This is
especially useful for such system configurations as KinD clusters and Docker
Desktop on WSL2.

## VSCode Tasks

The included `turtlefinder.code-workspace` defines the following tasks:

- **View Go module documentation** task: installs `pkgsite`, if not done already
  so, then starts `pkgsite` and opens VSCode's integrated ("simple") browser to
  show the csharg documentation.

#### Aux Tasks

- _pksite service_: auxilliary task to run `pkgsite` as a background service
  using `scripts/pkgsite.sh`. The script leverages browser-sync and nodemon to
  hot reload the Go module documentation on changes; many thanks to @mdaverde's
  [_Build your Golang package docs
  locally_](https://mdaverde.com/posts/golang-local-docs) for paving the way.
  `scripts/pkgsite.sh` adds automatic installation of `pkgsite`, as well as the
  `browser-sync` and `nodemon` npm packages for the local user.
- _view pkgsite_: auxilliary task to open the VSCode-integrated "simple" browser
  and pass it the local URL to open in order to show the module documentation
  rendered by `pkgsite`. This requires a detour via a task input with ID
  "_pkgsite_".

## Make Targets

- `make`: lists all targets.
- `make pkgsite`: installs [`x/pkgsite`](golang.org/x/pkgsite/cmd/pkgsite), as
  well as the [`browser-sync`](https://www.npmjs.com/package/browser-sync) and
  [`nodemon`](https://www.npmjs.com/package/nodemon) npm packages first, if not
  already done so. Then runs the `pkgsite` and hot reloads it whenever the
  documentation changes.
- `make report`: installs
  [`@gojp/goreportcard`](https://github.com/gojp/goreportcard) if not yet done
  so and then runs it on the code base.
- `make vuln`: install (or updates) govuln and then checks the Go sources.

# Contributing

Please see [CONTRIBUTING.md](CONTRIBUTING.md).

## License and Copyright

(c) Siemens AG 2023

[SPDX-License-Identifier: MIT](LICENSE)
