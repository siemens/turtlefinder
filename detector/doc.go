/*
Package detector defines the plugin interface between the TurtleFinder and its
container engine detector plugins.

The sub-package “all” pulls in all supported engine detector plugins that are
supported out-of-the-box of this module. The “all” package in turn is imported
by the toplevel “turtlefinder” package to ensure that the full support is always
included.

The individual engine-specific detector plugins are then implemented in the
other sub-packages: for instance, the “containerd” and “moby” sub-packages.
*/
package detector
