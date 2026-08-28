export namespace agents {
	
	export class Summary {
	    name: string;
	    description: string;
	    created_at?: string;
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.created_at = source["created_at"];
	    }
	}

}

export namespace config {
	
	export class MCPServer {
	    name: string;
	    transport: string;
	    command?: string;
	    args?: string[];
	    env?: Record<string, string>;
	    url?: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPServer(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.transport = source["transport"];
	        this.command = source["command"];
	        this.args = source["args"];
	        this.env = source["env"];
	        this.url = source["url"];
	    }
	}
	export class MemorySettings {
	    max_raw_messages: number;
	    preserve_recent: number;
	    max_summary_bytes: number;
	    replay_full_history: boolean;
	
	    static createFrom(source: any = {}) {
	        return new MemorySettings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.max_raw_messages = source["max_raw_messages"];
	        this.preserve_recent = source["preserve_recent"];
	        this.max_summary_bytes = source["max_summary_bytes"];
	        this.replay_full_history = source["replay_full_history"];
	    }
	}

}

export namespace desktop {
	
	export class GraphEdge {
	    from: string;
	    to: string;
	    condition?: string;
	
	    static createFrom(source: any = {}) {
	        return new GraphEdge(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.from = source["from"];
	        this.to = source["to"];
	        this.condition = source["condition"];
	    }
	}
	export class GraphNode {
	    id: string;
	    type: string;
	    config?: number[];
	
	    static createFrom(source: any = {}) {
	        return new GraphNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.type = source["type"];
	        this.config = source["config"];
	    }
	}
	export class Graph {
	    name: string;
	    entry: string;
	    nodes: GraphNode[];
	    edges: GraphEdge[];
	
	    static createFrom(source: any = {}) {
	        return new Graph(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.entry = source["entry"];
	        this.nodes = this.convertValues(source["nodes"], GraphNode);
	        this.edges = this.convertValues(source["edges"], GraphEdge);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgentDetail {
	    name: string;
	    description: string;
	    graph: Graph;
	    created_at?: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentDetail(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.graph = this.convertValues(source["graph"], Graph);
	        this.created_at = source["created_at"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class AgentUpdateResult {
	    name: string;
	    description: string;
	    persisted_to: string;
	    created_at: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentUpdateResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.persisted_to = source["persisted_to"];
	        this.created_at = source["created_at"];
	    }
	}
	export class CacheClearResult {
	    dirs: string[];
	    bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new CacheClearResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dirs = source["dirs"];
	        this.bytes = source["bytes"];
	    }
	}
	export class ModelView {
	    name: string;
	    vision: boolean;
	    reasoning: string;
	    web_search: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.vision = source["vision"];
	        this.reasoning = source["reasoning"];
	        this.web_search = source["web_search"];
	    }
	}
	export class ProviderInstance {
	    stable_id: string;
	    type: string;
	    name: string;
	    api: string;
	    key: string;
	    key_set: boolean;
	    key_env: boolean;
	    key_keychain: boolean;
	    models: ModelView[];
	    endpoint: string;
	    enabled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProviderInstance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.stable_id = source["stable_id"];
	        this.type = source["type"];
	        this.name = source["name"];
	        this.api = source["api"];
	        this.key = source["key"];
	        this.key_set = source["key_set"];
	        this.key_env = source["key_env"];
	        this.key_keychain = source["key_keychain"];
	        this.models = this.convertValues(source["models"], ModelView);
	        this.endpoint = source["endpoint"];
	        this.enabled = source["enabled"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConfigState {
	    instances: ProviderInstance[];
	    model: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.instances = this.convertValues(source["instances"], ProviderInstance);
	        this.model = source["model"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ConfigStatus {
	    needed: boolean;
	    default_model: string;
	    default_reasoning: boolean;
	    work_dir: string;
	    user_dir: string;
	    version: string;
	    agents: number;
	
	    static createFrom(source: any = {}) {
	        return new ConfigStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.needed = source["needed"];
	        this.default_model = source["default_model"];
	        this.default_reasoning = source["default_reasoning"];
	        this.work_dir = source["work_dir"];
	        this.user_dir = source["user_dir"];
	        this.version = source["version"];
	        this.agents = source["agents"];
	    }
	}
	export class DiagnosticsReport {
	    version: string;
	    go_version: string;
	    node_version: string;
	    git_version: string;
	    platform: string;
	    arch: string;
	    work_dir: string;
	    user_dir: string;
	    config_valid: boolean;
	    config_error?: string;
	    inference_configured: boolean;
	    git_repo: boolean;
	    git_branch?: string;
	    session_count: number;
	    active_runs: number;
	    sandbox_backend: string;
	    sandbox_available: boolean;
	    usage_total_tokens: number;
	
	    static createFrom(source: any = {}) {
	        return new DiagnosticsReport(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.version = source["version"];
	        this.go_version = source["go_version"];
	        this.node_version = source["node_version"];
	        this.git_version = source["git_version"];
	        this.platform = source["platform"];
	        this.arch = source["arch"];
	        this.work_dir = source["work_dir"];
	        this.user_dir = source["user_dir"];
	        this.config_valid = source["config_valid"];
	        this.config_error = source["config_error"];
	        this.inference_configured = source["inference_configured"];
	        this.git_repo = source["git_repo"];
	        this.git_branch = source["git_branch"];
	        this.session_count = source["session_count"];
	        this.active_runs = source["active_runs"];
	        this.sandbox_backend = source["sandbox_backend"];
	        this.sandbox_available = source["sandbox_available"];
	        this.usage_total_tokens = source["usage_total_tokens"];
	    }
	}
	export class FileNode {
	    name: string;
	    path: string;
	    is_dir: boolean;
	    size?: number;
	    children?: FileNode[];
	
	    static createFrom(source: any = {}) {
	        return new FileNode(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.is_dir = source["is_dir"];
	        this.size = source["size"];
	        this.children = this.convertValues(source["children"], FileNode);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	
	
	export class InferenceRequest {
	    instances: ProviderInstance[];
	
	    static createFrom(source: any = {}) {
	        return new InferenceRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.instances = this.convertValues(source["instances"], ProviderInstance);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class KanbanCard {
	    id: string;
	    producer?: string;
	    consumer?: string;
	    status: string;
	    target: string;
	    input?: string;
	    output?: string;
	    caller?: string;
	    depth: number;
	    error?: string;
	    run_id?: string;
	    parent_run_id?: string;
	    call_id?: string;
	    created_at: string;
	    updated_at: string;
	
	    static createFrom(source: any = {}) {
	        return new KanbanCard(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.producer = source["producer"];
	        this.consumer = source["consumer"];
	        this.status = source["status"];
	        this.target = source["target"];
	        this.input = source["input"];
	        this.output = source["output"];
	        this.caller = source["caller"];
	        this.depth = source["depth"];
	        this.error = source["error"];
	        this.run_id = source["run_id"];
	        this.parent_run_id = source["parent_run_id"];
	        this.call_id = source["call_id"];
	        this.created_at = source["created_at"];
	        this.updated_at = source["updated_at"];
	    }
	}
	export class MCPStatusDTO {
	    name: string;
	    status: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new MCPStatusDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.error = source["error"];
	    }
	}
	export class ModelOption {
	    id: string;
	    label: string;
	    reasoning: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ModelOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.reasoning = source["reasoning"];
	    }
	}
	export class ModelUsageStat {
	    model: string;
	    input_tokens: number;
	    output_tokens: number;
	    cache_read_tokens: number;
	    reasoning_tokens: number;
	    latency_ms: number;
	    workspaces: number;
	    sessions: number;
	    updated_at: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelUsageStat(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.model = source["model"];
	        this.input_tokens = source["input_tokens"];
	        this.output_tokens = source["output_tokens"];
	        this.cache_read_tokens = source["cache_read_tokens"];
	        this.reasoning_tokens = source["reasoning_tokens"];
	        this.latency_ms = source["latency_ms"];
	        this.workspaces = source["workspaces"];
	        this.sessions = source["sessions"];
	        this.updated_at = source["updated_at"];
	    }
	}
	
	export class PatchLineDTO {
	    kind: string;
	    old_num?: number;
	    new_num?: number;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new PatchLineDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.old_num = source["old_num"];
	        this.new_num = source["new_num"];
	        this.text = source["text"];
	    }
	}
	export class PatchFileDTO {
	    path: string;
	    action: string;
	    added: number;
	    removed: number;
	    lines: PatchLineDTO[];
	
	    static createFrom(source: any = {}) {
	        return new PatchFileDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.action = source["action"];
	        this.added = source["added"];
	        this.removed = source["removed"];
	        this.lines = this.convertValues(source["lines"], PatchLineDTO);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class PluginKVEntry {
	    key: string;
	    value: string;
	
	    static createFrom(source: any = {}) {
	        return new PluginKVEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.key = source["key"];
	        this.value = source["value"];
	    }
	}
	export class PluginSummary {
	    id: string;
	    name: string;
	    version: string;
	    entry: string;
	    permissions: string[];
	    enabled: boolean;
	    error?: string;
	    panels?: string[];
	    entries?: string[];
	
	    static createFrom(source: any = {}) {
	        return new PluginSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.version = source["version"];
	        this.entry = source["entry"];
	        this.permissions = source["permissions"];
	        this.enabled = source["enabled"];
	        this.error = source["error"];
	        this.panels = source["panels"];
	        this.entries = source["entries"];
	    }
	}
	export class PolicyDecision {
	    command: string;
	    allowed: boolean;
	    rules: string[];
	
	    static createFrom(source: any = {}) {
	        return new PolicyDecision(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.command = source["command"];
	        this.allowed = source["allowed"];
	        this.rules = source["rules"];
	    }
	}
	export class ProjectConfigStatus {
	    present: boolean;
	    trusted: boolean;
	    path?: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectConfigStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.present = source["present"];
	        this.trusted = source["trusted"];
	        this.path = source["path"];
	    }
	}
	
	export class ProviderView {
	    id: string;
	    name: string;
	    default_model: string;
	    env_var: string;
	    api: string;
	    azure: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProviderView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.name = source["name"];
	        this.default_model = source["default_model"];
	        this.env_var = source["env_var"];
	        this.api = source["api"];
	        this.azure = source["azure"];
	    }
	}
	export class ReplyRequest {
	    text: string;
	    option?: string;
	    options: string[];
	    cancel: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ReplyRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.option = source["option"];
	        this.options = source["options"];
	        this.cancel = source["cancel"];
	    }
	}
	export class SandboxProbeResult {
	    ok: boolean;
	    output?: string;
	    error?: string;
	
	    static createFrom(source: any = {}) {
	        return new SandboxProbeResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.ok = source["ok"];
	        this.output = source["output"];
	        this.error = source["error"];
	    }
	}
	export class SearchFileHit {
	    path: string;
	    is_dir: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SearchFileHit(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.is_dir = source["is_dir"];
	    }
	}
	export class SessionMeta {
	    id: string;
	    title: string;
	    created_at: string;
	    updated_at: string;
	    messages: number;
	    total_tokens: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.created_at = source["created_at"];
	        this.updated_at = source["updated_at"];
	        this.messages = source["messages"];
	        this.total_tokens = source["total_tokens"];
	    }
	}
	export class SkillDTO {
	    name: string;
	    description: string;
	    scope: string;
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillDTO(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.scope = source["scope"];
	        this.path = source["path"];
	    }
	}
	export class TurnStart {
	    run_id: string;
	    context_id: string;
	
	    static createFrom(source: any = {}) {
	        return new TurnStart(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.run_id = source["run_id"];
	        this.context_id = source["context_id"];
	    }
	}
	export class UsagePoint {
	    time: string;
	    input_tokens: number;
	    output_tokens: number;
	    cache_read_tokens: number;
	    reasoning_tokens: number;
	
	    static createFrom(source: any = {}) {
	        return new UsagePoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.time = source["time"];
	        this.input_tokens = source["input_tokens"];
	        this.output_tokens = source["output_tokens"];
	        this.cache_read_tokens = source["cache_read_tokens"];
	        this.reasoning_tokens = source["reasoning_tokens"];
	    }
	}
	export class WorkspaceMeta {
	    id: string;
	    path: string;
	    title: string;
	    last_opened: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.path = source["path"];
	        this.title = source["title"];
	        this.last_opened = source["last_opened"];
	    }
	}

}

export namespace message {
	
	export class Content {
	    parts: any[];
	
	    static createFrom(source: any = {}) {
	        return new Content(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.parts = source["parts"];
	    }
	}
	export class Message {
	    role: string;
	    content: Content;
	
	    static createFrom(source: any = {}) {
	        return new Message(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.content = this.convertValues(source["content"], Content);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace undo {
	
	export class State {
	    can_undo: boolean;
	    can_redo: boolean;
	
	    static createFrom(source: any = {}) {
	        return new State(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.can_undo = source["can_undo"];
	        this.can_redo = source["can_redo"];
	    }
	}

}

