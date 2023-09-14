/*
Package containerd implements the engine detector for containerd processes.

containerd is differs slightly from the usual in that it can return two
watchers, one for containerd's native API and workload, but optionally also
another one for the CRI API workload (when CRI is enabled).
*/
package containerd
