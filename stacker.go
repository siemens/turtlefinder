// (c) Siemens AG 2023
//
// SPDX-License-Identifier: MIT

package turtlefinder

import (
	"context"
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
	ID                 uint64           // arbitrary unique ID for this particular container engine, used later for decoration.
	EncloserName       string           // name derived from enclosing container, if any, otherwise "".
	EncloserEnginePID  model.PIDType    // PID of engine PID managing the enclosing container, if any, otherwise 0.
	EncloserEngineType string           // type of container engine, for multi-engine type processes.
	Prefix             string           // hierarchical prefix for this engine, or "".
	Parent             *stackedEngine   // parent container engine, if any.
	Children           []*stackedEngine // child container engines, if any.
}

type stackedEngineKey struct {
	PID  model.PIDType
	Type string
}

func stackedEngineKeyOf(e *Engine) stackedEngineKey {
	return stackedEngineKey{PID: model.PIDType(e.PID()), Type: e.Type()}
}

func stackedContainerEngineKey(e *model.ContainerEngine) stackedEngineKey {
	return stackedEngineKey{PID: e.PID, Type: e.Type}
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
//
// PREMIERE BIG OUCHIE: remember that the same process can be multiple engines,
// a containerd engine as well as a CRI API engine. **OUCH** **OUCH** **OUCH**
//
// DEUXIEME BIG OUCHIE: lxkns does the container PID translation from local
// engine PID namespace to root PID namespace only after the containerizer and
// thus the engine stacking has been processed. In other words: we here still
// have local engine container PIDs.
func stackEngines(enginesInclContainers []*model.ContainerEngine, engines []*Engine, proctable model.ProcessTable, pidmap model.PIDMapper) {
	if stackerDebug {
		slog.Debug("discovering container engine stacking", slog.Int("num", len(engines)))
	}
	// Let's start with building an index consisting only of container PIDs:
	// normally, this would be understood to refer to the initial (kind of
	// "root") PID inside the container ... but this can be quite misleading in
	// several real-life situations.
	//
	// When using development containers with the absolute essential
	// docker-in-docker feature the docker demon process isn't a child of the
	// container's initial/root process, but instead a *sibling*. Or in lxkns
	// parlance, the docker demon is an Ealdorman which is somehow fitting
	// *snicker*.
	//
	// Now as all currently supported OCI container engines (and that has always
	// excluded false marketing attributions from systemd and others) use shims
	// as a common architectural lynchpin, we are going to map the shim PIDs to
	// their containers instead -- this later makes searching more
	// straightforward.
	containersByShimPID := map[model.PIDType]*model.Container{}
	for _, engine := range enginesInclContainers {
		// Ohlala, DEUXIEME BIG OUCHIE: we need to translate container PIDs from
		// their (still) engine-local PID namespaces into the root namespace.
		var enginePIDns model.Namespace
		if engineProc, ok := proctable[engine.PID]; ok {
			enginePIDns = engineProc.Namespaces[model.PIDNS]
		} else if engine.PPIDHint != 0 {
			// This is a newly socket-activated engine that isn't yet
			// included in the process tree – that process tree that
			// ironically lead to the detection of the socket activator and
			// then activation of that container engine. As we cannot change
			// the past discovery some kind soul – a turtle, perchance? –
			// might have passed us a hint about the engine's parent process
			// PID. This parent process's PID namespace should be the same
			// as the container engine, so it should be good for container
			// PID translation.
			//
			// This deserves a badge: [COMMENTOR] ... rhymes with
			// "tormentor" *snicker*
			if parentProc, ok := proctable[engine.PPIDHint]; ok {
				enginePIDns = parentProc.Namespaces[model.PIDNS]
			}
		}
		// Index the containers of this engine, making sure to translate their
		// PIDs from engine-local PID namespace to root PID namespace. Oh,
		// well...
		for _, container := range engine.Containers {
			pid := container.PID
			if pidmap != nil && enginePIDns != nil {
				if pids := pidmap.NamespacedPIDs(container.PID, enginePIDns); len(pids) > 0 {
					pid = pids[0].PID
				}
			}
			if proc := proctable[pid]; proc != nil && proc.Parent != nil {
				pid = proc.Parent.PID // take the shim instead
			}
			containersByShimPID[pid] = container
			if stackerDebug {
				slog.Debug("turtlefinder stacker indexing containers by PID",
					slog.String("container-name", container.Name),
					slog.String("type", container.Type),
					slog.Int("container-pid", int(container.PID)),
					slog.Int("index-pid", int(pid)))
			}
		}
	}
	if stackerDebug {
		slog.Debug("container engine stacking discovery", slog.String("checkpoint", "A"))
	}

	// The hierarchy is formed by engines inside containers, and these
	// containers then again belong to engines, so its turtles all the way down.
	// Now, in case some of engines have only been socket-activated in this run,
	// we lack the process details for the newly activated engine process. So we
	// create a shallow clone of the discovered process tree and then fetch and
	// add in those missing pieces of engine process information. We have to do
	// as, we below will need this information for successfully climbing the
	// engine/process hierarchy.
	clonedProctable := false
	for _, engine := range engines {
		proc := proctable[model.PIDType(engine.PID())]
		if proc != nil {
			continue
		}
		proc = model.NewProcess(model.PIDType(engine.PID()), false)
		if proc == nil {
			slog.Debug("lost container engine", slog.Uint64("engine-pid", uint64(engine.PID())))
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
		slog.Debug("fetched missing process details for container engine", slog.Uint64("engine-pid", uint64(engine.PID())))
	}
	if stackerDebug {
		slog.Debug("container engine stacking discovery", slog.String("checkpoint", "B"))
	}

	// Index the list of engines we were told, in order to quickly look up the
	// additional information we need to associate with the engines during the
	// stacking process. The index key is an engine's PID, as this is the only
	// correct link between a model.ContainerEngine and a stackedEngine.
	// We also use this chance to see if an engine is inside a container and in
	// which one in particular.
	stackedEnginesByPIDType := map[stackedEngineKey]*stackedEngine{}
	for _, engine := range engines {
		if stackerDebug {
			slog.Debug("turtlefinder stacker indexing engine",
				slog.String("engine", engine.Type()),
				slog.Int("engine-pid", engine.PID()))
		}
		// Climb up the process tree until we either hit a container PID or we
		// fall off the ... root? Okay, another +1 on the eternal counter of
		// really bad metaphors.
		var (
			name            string
			outerEnginePID  model.PIDType
			outerEngineType string
			container       *model.Container
		)
		proc := proctable[model.PIDType(engine.PID())]
		for proc != nil && proc.PID != 1 {
			if stackerDebug {
				slog.Debug("turtlefinder stacker locating encloser step",
					slog.GroupAttrs("process",
						slog.String("name", proc.Name),
						slog.Int("pid", int(proc.PID)),
					))
			}
			var ok bool
			if container, ok = containersByShimPID[proc.PID]; ok {
				// we've found the container that is hosting our engine.
				name = container.Name
				outerEnginePID = container.Engine.PID
				outerEngineType = container.Engine.Type
				break
			}
			// rinse and repeat until container PID hit or falling off root.
			proc = proc.Parent
		}
		if stackerDebug {
			slog.Debug("turtlefinder stacker indexed located engine",
				slog.String("type", engine.Type()),
				slog.Int("engine-pid", engine.PID()),
				slog.GroupAttrs("outer",
					slog.String("name", name),
					slog.String("type", outerEngineType),
					slog.Int("engine-pid", int(outerEnginePID)),
				))
		}
		key := stackedEngineKeyOf(engine)
		if eng := stackedEnginesByPIDType[key]; eng != nil {
			slog.Error("aborting turtlefinder engine stacking due to duplicate engine PID",
				slog.Int("pid", engine.PID()))
			return // abort stacking immediately to avoid getting stuck below.
		}
		stackedEnginesByPIDType[key] = &stackedEngine{
			EncloserName:       name,
			EncloserEnginePID:  outerEnginePID,
			EncloserEngineType: outerEngineType,
		}
	}
	if stackerDebug {
		slog.Debug("container engine stacking discovery", slog.String("checkpoint", "C"),
			slog.Int("num", len(stackedEnginesByPIDType)))
	}

	// Now that we know which engines are containerized, set these engines to be
	// children of the container engines managing the engine-enclosing
	// containers. Hopefully, we end up with some hierarchy. While it is not
	// strictly necessary to explicitly build this engine hierarchy, it helps
	// with detecting sibling engines in the same context, such as side-by-side
	// engines in the host or in some container.
	var nullEngine = &stackedEngine{} // acts as "fake" root
	for _, engine := range stackedEnginesByPIDType {
		if pid := engine.EncloserEnginePID; pid != 0 {
			if parentEngine := stackedEnginesByPIDType[stackedEngineKey{
				PID:  pid,
				Type: engine.EncloserEngineType,
			}]; parentEngine != nil {
				parentEngine.Add(engine)
				continue
			}
		}
		nullEngine.Add(engine)
	}
	if stackerDebug {
		slog.Debug("container engine stacking discovery", slog.String("checkpoint", "D"))
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
		if stackedEng, ok := stackedEnginesByPIDType[stackedContainerEngineKey(engine)]; ok {
			stackedEng.ID = id
		}
	}
	for _, engine := range enginesInclContainers {
		id := engineIDs[engine]
		engine.Labels[TurtlefinderEngineIDLabelName] = strconv.FormatUint(id, 10)
		stackedEng, ok := stackedEnginesByPIDType[stackedContainerEngineKey(engine)]
		if !ok || stackedEng.Parent == nil || stackedEng.Parent.ID == 0 {
			continue
		}
		engine.Labels[TurtlefinderEngineParentIDLabelName] = strconv.FormatUint(stackedEng.Parent.ID, 10)
	}
	if stackerDebug {
		slog.Debug("container engine stacking discovery", slog.String("checkpoint", "E"))
	}
	// Next, we can now determine the engine prefixes ("turtle paths") based on
	// the discovered engine hierarchy. Looks like a recursion allergy ;)
	slog.Debug("stacked engines", slog.Int("num", len(stackedEnginesByPIDType)))
	for _, engine := range stackedEnginesByPIDType {
		attrs := []slog.Attr{
			slog.Int("id", int(engine.ID)),
		}
		if ep := engine.Parent; ep != nil {
			attrs = append(attrs, slog.Int("parent-id", int(ep.ID)),
				slog.String("encloser", ep.EncloserName))
		}
		slog.LogAttrs(context.Background(), slog.LevelDebug, "stacked engine", attrs...)
	}
	for _, engine := range stackedEnginesByPIDType {
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
	if stackerDebug {
		slog.Debug("container engine stacking discovery", slog.String("checkpoint", "F"))
	}

	// Finally distribute the per-engine prefixes to the individual containers;
	// the prefixes are attached as turtlefinder-specific container labels.
	for _, engine := range enginesInclContainers {
		cachedEnginePrefix := ""
		if stackedEng, ok := stackedEnginesByPIDType[stackedContainerEngineKey(engine)]; ok {
			cachedEnginePrefix = stackedEng.Prefix
		}
		for _, container := range engine.Containers {
			container.Labels[TurtlefinderContainerPrefixLabelName] = cachedEnginePrefix
		}
	}
	slog.Debug("container engine stacking discovery finished")
}
