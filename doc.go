/*
Package turtlefinder provides a Containerizer that auto-detects container
engines and automatically creates workload watchers for them. It supports both
“permanent” daemons as well as socket-activated “don't-call-them-daemons”.
Additionally, it also detects the hierarchy of container engines, such as
containerd-in-Docker and podman-in-Docker.

# Supported Container Engines

The following container engines are supported:

  - [Docker/Moby]
  - [containerd]
  - [CRI-O]
  - [podman] (via Docker-compatible API only)

# Supported Socket Activators

The following socket activators are supported:

  - [systemd]

# Quick Start

That's all that is necessary:

	containerizer := turtlefinder.New()

Boringly simple, right?

The turtlefinder [containerizer.Containerizer] is safe to be used in concurrent
discoveries.

# Principles of “Turtle” Discovery

A turtlefinder supports two different container engine discovery mechanisms:
  - based on well-known engine process names; please note that this works for
    “always on” engine daemons, such as “dockerd” (even if this is initially
    socket-activated on systemd installations).
  - based on well-known socket activators, such as “systemd”, in combination
    with well-known API socket names (rather, suffixes). This method is much
    more involved when compared to the well-known always-on process name method,
    but allows discovering especially usually short-lived “podman” services.

The turtlefinder then spins up background watchers as required that synchronize
with the workload state of the detected container engines. Also, old engine
watchers get retired as their engine processes die. This workload state
information is then returned as the list of discovered containers, including the
hierarchy of container engines, based on which engine is placed inside a
container managed by another engine.

# Well-Known Process Name Discovery

Basically, upon a container query the turtlefinder containerizer first looks for
any newly seen container engines, based on container engine process names. The
engine discovery can be extended by pluging in new engine detectors (and
adaptors).

# Well-known Socket Activation Name Discovery

For “short-lived” container engine services that terminate themselves whenever
they go idle, we unfortunately need a more involved discovery mechanism. More
involved, as we don't want to slow down discovery by constantly looking for
something that isn't even installed in the system, so we need to do some
optimization.

The general idea is to look for well-known socket activators, namely “systemd”.
If found (even multiple times!), we scan such an activator for its listening
unix domain sockets and determine their file system paths. If we find a matching
path (rather, a matching suffix, such as “podman.sock”) we spin up a suitable
background watcher. Of course, this background watcher will keep the container
engine alive, but then we also need this service in constant monitoring.

The difficult part here is to avoid repeated unnecessary costly socket activator
discoveries. We thus keep some state information about a socket activator's
socket-related setup and only rediscover upon noticing changes in its socket
configuration (which rarely if ever occurs).

# Engines in Engines

A defining feature of the turtlefinder is that it additionally determines the
hierarchy of container engines, such as when a container engine is hosted inside
a container managed by a (parent) container engine. This hierarchy later gets
propagated to the individual containers in form of a so-called “prefix”,
attached in form of a special container label.

Such engine-in-engine configurations are actually not so unknown:

  - [Docker Desktop]
  - [Kubernetes in Docker] (KinD)
  - podman in Docker

# Decoration

Finally, the decoration of the discovered containers uses the usual (extensible)
lxkns [github.com/thediveo/lxkns/decorator.Decorator] mechanism as part of the
overall discovery.

[Docker/Moby]: https://docker.com
[containerd]: https://containerd.io
[CRI-O]: https://cri-o.io
[podman]: https://podman.io
[Docker Desktop]: https://www.docker.com/products/docker-desktop/
[Kubernetes in Docker]: https://kind.sigs.k8s.io/
[systemd]: https://0pointer.de/blog/projects/socket-activation.html
*/
package turtlefinder
