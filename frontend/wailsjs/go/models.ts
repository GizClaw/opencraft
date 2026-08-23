export namespace agents {
	
	export class Summary {
	    name: string;
	    description: string;
	    // Go type: time
	    created_at?: any;
	
	    static createFrom(source: any = {}) {
	        return new Summary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.created_at = this.convertValues(source["created_at"], null);
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

export namespace desktop {
	
	export class SetupProvider {
	    id: string;
	    key: string;
	    key_env: boolean;
	    model: string;
	    endpoint: string;
	    vision: boolean;
	    reasoning: string;
	    web_search: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SetupProvider(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.key = source["key"];
	        this.key_env = source["key_env"];
	        this.model = source["model"];
	        this.endpoint = source["endpoint"];
	        this.vision = source["vision"];
	        this.reasoning = source["reasoning"];
	        this.web_search = source["web_search"];
	    }
	}
	export class ConfigState {
	    providers: SetupProvider[];
	    model: string;
	
	    static createFrom(source: any = {}) {
	        return new ConfigState(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providers = this.convertValues(source["providers"], SetupProvider);
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
	export class HistoryMsg {
	    role: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new HistoryMsg(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.role = source["role"];
	        this.text = source["text"];
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
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    updated_at: any;
	
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
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.updated_at = this.convertValues(source["updated_at"], null);
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
	    // Go type: time
	    created_at: any;
	    // Go type: time
	    updated_at: any;
	    messages: number;
	    total_tokens: number;
	
	    static createFrom(source: any = {}) {
	        return new SessionMeta(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.created_at = this.convertValues(source["created_at"], null);
	        this.updated_at = this.convertValues(source["updated_at"], null);
	        this.messages = source["messages"];
	        this.total_tokens = source["total_tokens"];
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
	
	export class SetupRequest {
	    providers: SetupProvider[];
	
	    static createFrom(source: any = {}) {
	        return new SetupRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.providers = this.convertValues(source["providers"], SetupProvider);
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

}

