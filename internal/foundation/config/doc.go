// Package config owns opencraft's configuration: the user-facing layer
// (~/.opencraft/config/opencraft.yaml) with its discovery, seeding,
// layered loading and per-domain settings, the embedded deploy assets
// the layer is merged on top of, and the app-level execution document.
// The deploy layering itself is flowcraft core's deploy.LoadLayers —
// this package decides what opencraft writes into it.
//
// File map (W3b of docs/architecture-plan.md §W3): a domain is found by
// name, not by reading the package. The shape is <domain>_<role>.go
// where a domain has more than one file (inference_* is the example);
// a single-file domain keeps the plain name, because renaming
// delegation.go to delegation_load.go would cost blame and the
// references §W4 and AGENTS.md make to config/atomic.go without
// answering a question nobody asked.
//
//	core        manager.go           discovery, seeding, layered load/save
//	            registry.go          previously opened workspaces
//	            paths.go             user data dir, config dir, workspace root
//	            launch.go            the launch resolver (content vs state root)
//	            userlayer.go         deep-merge a generated resource into the document
//	            render.go            render one resource row from its template
//	            seed.go              first-run config dir + sandbox cache
//	            atomic.go            writeFileAtomic (same-dir temp + rename)
//	            model.go             the router policy's default model
//	            retiredrefs.go       live references to retired assembly variables
//	            graph_contract.go    board channels shared with the graph asset
//	assets      assets.go            the embedded layer files, graph and prompts
//	inference   inference.go         the domain entry point and its documentation
//	            inference_spec.go    InstanceSpec: the one JSON shape of a row
//	            inference_load.go    reading the user layer (incl. legacy shapes)
//	            inference_write.go   rendering and persisting the document
//	            inference_apply.go   submitted row -> stored row, and what survives
//	            inference_query.go   "is inference wired?" queries
//	            inference_state.go   the state lock and the plugin-ownership sidecar
//	            inference_templates.go built-in provider templates
//	tools       tooloptions.go      provider-specific tool options
//	            websearch.go         hosted web search board extensions
//	            websearchtool.go     tool.websearch settings
//	            mcp.go               external MCP servers
//	memory      memory.go            resources.mem.settings
//	            usermemory.go        user-level memory settings
//	review      review.go            post-turn review settings
//	skills      skilllifecycle.go    skill lifecycle settings
//	delegation  delegation.go        delegation policy settings
//	workspace   workspace.go         workspace id derivation
package config
