// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"log/slog"
	"maps"
	"strconv"

	"github.com/thediveo/lxkns/model"
)

// TurtlefinderContainerPrefixLabelName defines the label name for attaching
// prefix information about the engine hierarchy to containers. Discovery
// clients can use these container labels to find out the hierarchy of
// containers. For instance, if container "A" is managed by a container engine
// hosted inside container "B", then container "A" is labelled with prefix "B".
const TurtlefinderContainerPrefixLabelName = "turtlefinder/container/prefix"

// PrefixSeparator is the separator used in hierarchical prefixes. See also:
// [TurtlefinderContainerPrefixLabelName].
const PrefixSeparator = "/"

const TurtlefinderEngineIDLabelName = "turtlefinder/engine/id"
const TurtlefinderEngineParentIDLabelName = "turtlefinder/engine/parent-id"

const stackerDebug = false

// stackedEngine temporarily stores additional details about a container engine
// while we figure out if and how engines have been stacked, or rather, put into
// each other.
type stackedEngine struct {
	ID                uint64           // arbitrary unique ID for this particular container engine, used later for decoration.
	EncloserName      string           // name derived from enclosing container, if any, otherwise "".
	EncloserEnginePID model.PIDType    // PID of engine PID managing the enclosing container, if any, otherwise 0.
	Prefix            string           // hierarchical prefix for this engine, or "".
	Parent            *stackedEngine   // parent container engine, if any.
	Children          []*stackedEngine // child container engines, if any.
}

// Add a stackedEngine as the child of this stackedEngine, at the same time also
// setting this stackedEngine to be the parent of the added stackedEngine.
func (e *stackedEngine) Add(child *stackedEngine) {
	child.Parent = e
	e.Children = append(e.Children, child)
}

// stackEngines discovers the hierarchical relationships (if any) between
// container engines, that is, when one engine is running inside a container
// managed by another container engine.
func stackEngines(enginesInclContainers []*model.ContainerEngine, engines []*Engine, proctable model.ProcessTable) {
	// Let's build an index for mapping the PIDs of the containers' initial
	// (shim) processes to their containers. The reason we don't use the PIDs of
	// the container's initial processes is that in some configurations, such as
	// with the devcontainer Docker-in-Docker feature, we end up with the demon
	// process being a direct child of the container shim, not of the process.
	containersByPID := map[model.PIDType]*model.Container{}
	for _, engine := range enginesInclContainers {
		for _, container := range engine.Containers {
			pid := container.PID
			if proc := proctable[pid]; proc != nil && proc.Parent != nil {
				pid = proc.Parent.PID // take the shim instead
			}
			containersByPID[pid] = container
			if stackerDebug {
				slog.Debug("turtlefinder stacker",
					slog.String("container-name", container.Name),
					slog.Int("container-pid", int(container.PID)),
					slog.Int("pid", int(pid)))
			}
		}
	}
	// Index the list of engines we were told, in order to quickly look up the
	// additional information we need to associate with the engines during the
	// stacking process. The index key is an engine's PID, as this is the only
	// correct link between a model.ContainerEngine and a stackedEngine.
	// We also use this chance to see if an engine is inside a container and in
	// which one in particular.
	stackedEnginesByPID := map[model.PIDType]*stackedEngine{}
	// The hierarchy is formed by engines inside containers, and these
	// containers then again belonging to engines, and so on. Now in case of
	// engines that have been socket-activated in this run, we lack the
	// process details of the newly activated engine process. So we just
	// fetch those pieces of engine process information we next need for
	// climbing the hierarchy.
	clonedProctable := false
	for _, engine := range engines {
		proc := proctable[model.PIDType(engine.PID())]
		if proc != nil {
			continue
		}
		proc = model.NewProcess(model.PIDType(engine.PID()), false)
		if proc == nil {
			continue // we've lost this engine already, anyway.
		}
		if !clonedProctable {
			clonedProctable = true
			proctable = maps.Clone(proctable) // a shallow clone is enough.
		}
		// Below, we'll only need the child->parent relationship, but not
		// parent->children: we thus only need to modify the newly created
		// process object and the shallow clone of the map, but we neither touch
		// the original process map nor the original process objects.
		proctable[proc.PID] = proc
		proc.Parent = proctable[proc.PPID]
	}
	for _, engine := range engines {
		if stackerDebug {
			slog.Debug("turtlefinder stacker checking engine",
				slog.String("engine", engine.Type()),
				slog.Int("engine-pid", engine.PID()))
		}
		// Climb up the process tree until we either hit a container PID or we
		// fall off the ... root? Okay, another +1 on the eternal counter of
		// really bad metaphors.
		var (
			name           string
			outerEnginePID model.PIDType
			container      *model.Container
		)
		proc := proctable[model.PIDType(engine.PID())]
		for proc != nil {
			if stackerDebug {
				slog.Debug("turtlefinder stacker checking process",
					slog.String("name", proc.Name),
					slog.Int("pid", int(proc.PID)))
			}
			var ok bool
			if container, ok = containersByPID[proc.PID]; ok {
				name = container.Name
				outerEnginePID = container.Engine.PID
				break
			}
			// rinse and repeat until container PID hit or falling off root.
			proc = proc.Parent
		}
		if stackerDebug {
			slog.Debug("turtlefinder stacked engine",
				slog.String("engine", engine.Type()),
				slog.Int("engine-pid", engine.PID()),
				slog.String("parent-engine", engine.Type()),
				slog.Int("parent-engine-pid", int(outerEnginePID)))
		}
		stackedEnginesByPID[model.PIDType(engine.PID())] = &stackedEngine{
			EncloserName:      name,
			EncloserEnginePID: outerEnginePID,
		}
	}
	// Now that we know which engines are containerized, set these engines to be
	// children of the container engines managing the engine-enclosing
	// containers. Hopefully, we end up with some hierarchy. While it is not
	// strictly necessary to explicitly build this engine hierarchy, it helps
	// with detecting sibling engines in the same context, such as side-by-side
	// engines in the host or in some container.
	var nullEngine = &stackedEngine{} // acts as "fake" root
	for _, engine := range stackedEnginesByPID {
		if pid := engine.EncloserEnginePID; pid != 0 {
			if parentEngine := stackedEnginesByPID[pid]; parentEngine != nil {
				parentEngine.Add(engine)
				continue
			}
		}
		nullEngine.Add(engine)
	}
	// Write the discovered engine hierarchy into engine labels where tools
	// might be able to make use of them. We first assign arbitrary unique IDs
	// to the stacked engines regardless of whether the engine tree is correct
	// or has fallen-off branches due to transient effects or strong digital
	// winds.
	engineIDs := map[*model.ContainerEngine]uint64{}
	for idx, engine := range enginesInclContainers {
		id := uint64(idx) + 1 // IDs are never zero unless you have THAT MANY engines...
		engineIDs[engine] = id
		if stackedEng, ok := stackedEnginesByPID[engine.PID]; ok {
			stackedEng.ID = id
		}
	}
	for _, engine := range enginesInclContainers {
		id := engineIDs[engine]
		engine.Labels[TurtlefinderEngineIDLabelName] = strconv.FormatUint(id, 10)
		stackedEng, ok := stackedEnginesByPID[engine.PID]
		if !ok || stackedEng.Parent == nil || stackedEng.Parent.ID == 0 {
			continue
		}
		engine.Labels[TurtlefinderEngineParentIDLabelName] = strconv.FormatUint(stackedEng.Parent.ID, 10)
	}
	// Next, we can now determine the engine prefixes ("turtle paths") based on
	// the discovered engine hierarchy. Looks like a recursion allergy ;)
	for _, engine := range stackedEnginesByPID {
		prefix := ""
		eng := engine
		for eng != nil {
			if eng.EncloserName != "" {
				if prefix == "" {
					prefix = eng.EncloserName
				} else {
					prefix = eng.EncloserName + PrefixSeparator + prefix
				}
			}
			eng = eng.Parent
		}
		engine.Prefix = prefix
	}
	// Finally distribute the per-engine prefixes to the individual containers;
	// the prefixes are attached as turtlefinder-specific container labels.
	for _, engine := range enginesInclContainers {
		cachedEnginePrefix := ""
		if stackedEng, ok := stackedEnginesByPID[engine.PID]; ok {
			cachedEnginePrefix = stackedEng.Prefix
		}
		for _, container := range engine.Containers {
			container.Labels[TurtlefinderContainerPrefixLabelName] = cachedEnginePrefix
		}
	}
}
