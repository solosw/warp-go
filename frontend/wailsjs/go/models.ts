export namespace config {
	
	export class ClaudeCodeSlots {
	    opusIndex: number;
	    sonnetIndex: number;
	    haikuIndex: number;
	
	    static createFrom(source: any = {}) {
	        return new ClaudeCodeSlots(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.opusIndex = source["opusIndex"];
	        this.sonnetIndex = source["sonnetIndex"];
	        this.haikuIndex = source["haikuIndex"];
	    }
	}
	export class AIConfigGroup {
	    name: string;
	    apiKey: string;
	    baseURL: string;
	    models: string[];
	    claudeCode: ClaudeCodeSlots;
	
	    static createFrom(source: any = {}) {
	        return new AIConfigGroup(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.apiKey = source["apiKey"];
	        this.baseURL = source["baseURL"];
	        this.models = source["models"];
	        this.claudeCode = this.convertValues(source["claudeCode"], ClaudeCodeSlots);
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
	export class Appearance {
	    backgroundImage: string;
	    backgroundOpacity: number;
	    panelOpacity: number;
	
	    static createFrom(source: any = {}) {
	        return new Appearance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.backgroundImage = source["backgroundImage"];
	        this.backgroundOpacity = source["backgroundOpacity"];
	        this.panelOpacity = source["panelOpacity"];
	    }
	}
	
	export class ProjectRunCommand {
	    name: string;
	    command: string;
	
	    static createFrom(source: any = {}) {
	        return new ProjectRunCommand(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.command = source["command"];
	    }
	}
	export class RemoteWorkspaceEntry {
	    name: string;
	    host: string;
	    port: number;
	    user: string;
	    remotePath: string;
	    cachePath: string;
	
	    static createFrom(source: any = {}) {
	        return new RemoteWorkspaceEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.user = source["user"];
	        this.remotePath = source["remotePath"];
	        this.cachePath = source["cachePath"];
	    }
	}
	export class SSHConfig {
	    name: string;
	    host: string;
	    port: number;
	    user: string;
	    password: string;
	    keyPath: string;
	
	    static createFrom(source: any = {}) {
	        return new SSHConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.user = source["user"];
	        this.password = source["password"];
	        this.keyPath = source["keyPath"];
	    }
	}
	export class StartupCommand {
	    name: string;
	    command: string;
	
	    static createFrom(source: any = {}) {
	        return new StartupCommand(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.command = source["command"];
	    }
	}
	export class TerminalSnapshot {
	    id: string;
	    title: string;
	    type: string;
	    workspace: string;
	    cwd: string;
	    sshName?: string;
	    output: string;
	    restored: boolean;
	    active?: boolean;
	    updatedAt: string;
	
	    static createFrom(source: any = {}) {
	        return new TerminalSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.title = source["title"];
	        this.type = source["type"];
	        this.workspace = source["workspace"];
	        this.cwd = source["cwd"];
	        this.sshName = source["sshName"];
	        this.output = source["output"];
	        this.restored = source["restored"];
	        this.active = source["active"];
	        this.updatedAt = source["updatedAt"];
	    }
	}
	export class WorkspaceEntry {
	    path: string;
	    name: string;
	    lastOpened: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.lastOpened = source["lastOpened"];
	    }
	}

}

export namespace lsp {
	
	export class ServerInfo {
	    language: string;
	    available: boolean;
	    command: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new ServerInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.language = source["language"];
	        this.available = source["available"];
	        this.command = source["command"];
	        this.message = source["message"];
	    }
	}

}

export namespace main {
	
	export class AIToolPaths {
	    claudeCode: string;
	    codex: string;
	    openCode: string;
	
	    static createFrom(source: any = {}) {
	        return new AIToolPaths(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.claudeCode = source["claudeCode"];
	        this.codex = source["codex"];
	        this.openCode = source["openCode"];
	    }
	}
	export class RemoteDirEntry {
	    name: string;
	    path: string;
	    isDir: boolean;
	    size: number;
	    modTime: number;
	    isBinary: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RemoteDirEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.isDir = source["isDir"];
	        this.size = source["size"];
	        this.modTime = source["modTime"];
	        this.isBinary = source["isBinary"];
	    }
	}
	export class SSHConfig {
	    name: string;
	    host: string;
	    port: number;
	    user: string;
	    password: string;
	    keyPath: string;
	
	    static createFrom(source: any = {}) {
	        return new SSHConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.user = source["user"];
	        this.password = source["password"];
	        this.keyPath = source["keyPath"];
	    }
	}
	export class WorkspaceInfo {
	    path: string;
	    name: string;
	    fileCount: number;
	    files: string[];
	    otherFiles: string[];
	    directories: string[];
	    isRemote: boolean;
	    changedFiles: snapshot.FileChange[];
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.fileCount = source["fileCount"];
	        this.files = source["files"];
	        this.otherFiles = source["otherFiles"];
	        this.directories = source["directories"];
	        this.isRemote = source["isRemote"];
	        this.changedFiles = this.convertValues(source["changedFiles"], snapshot.FileChange);
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
	export class WorkspaceSearchMatch {
	    line: number;
	    column: number;
	    text: string;
	    match: string;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceSearchMatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.line = source["line"];
	        this.column = source["column"];
	        this.text = source["text"];
	        this.match = source["match"];
	    }
	}
	export class WorkspaceSearchResult {
	    path: string;
	    matches: WorkspaceSearchMatch[];
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceSearchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.matches = this.convertValues(source["matches"], WorkspaceSearchMatch);
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

export namespace snapshot {
	
	export class FileChange {
	    path: string;
	    status: string;
	    additions: number;
	    deletions: number;
	
	    static createFrom(source: any = {}) {
	        return new FileChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.status = source["status"];
	        this.additions = source["additions"];
	        this.deletions = source["deletions"];
	    }
	}

}

