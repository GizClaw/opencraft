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

}

export namespace desktop {
	
	export class ProviderInstance {
	    stable_id: string;
	    type: string;
	    name: string;
	    api: string;
	    key: string;
	    key_set: boolean;
	    key_env: boolean;
	    model: string;
	    endpoint: string;
	    vision: boolean;
	    reasoning: string;
	    web_search: boolean;
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
	        this.model = source["model"];
	        this.endpoint = source["endpoint"];
	        this.vision = source["vision"];
	        this.reasoning = source["reasoning"];
	        this.web_search = source["web_search"];
	        this.enabled = source["enabled"];
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
	        this.work_dir = source["work_dir"];
	        this.user_dir = source["user_dir"];
	        this.version = source["version"];
	        this.agents = source["agents"];
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
	        this.created_at = source["created_at"];
	        this.updated_at = source["updated_at"];
	    }
	}
	export class ModelOption {
	    id: string;
	    label: string;
	
	    static createFrom(source: any = {}) {
	        return new ModelOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
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

