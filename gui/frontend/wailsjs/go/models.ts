export namespace content {
	
	export class Seg {
	    text: string;
	    class?: string;
	
	    static createFrom(source: any = {}) {
	        return new Seg(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.class = source["class"];
	    }
	}
	export class Line {
	    num: string;
	    segs?: Seg[];
	    mark?: string;
	
	    static createFrom(source: any = {}) {
	        return new Line(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.num = source["num"];
	        this.segs = this.convertValues(source["segs"], Seg);
	        this.mark = source["mark"];
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
	
	export class SplitBlock {
	    mark: string;
	    leftStart: number;
	    leftCount: number;
	    rightStart: number;
	    rightCount: number;
	
	    static createFrom(source: any = {}) {
	        return new SplitBlock(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mark = source["mark"];
	        this.leftStart = source["leftStart"];
	        this.leftCount = source["leftCount"];
	        this.rightStart = source["rightStart"];
	        this.rightCount = source["rightCount"];
	    }
	}

}

export namespace main {
	
	export class CompareContentOptions {
	    path: string;
	    chain?: string[];
	    charset: string;
	    rightCharset: string;
	    diffModel?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CompareContentOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.chain = source["chain"];
	        this.charset = source["charset"];
	        this.rightCharset = source["rightCharset"];
	        this.diffModel = source["diffModel"];
	    }
	}
	export class CompareDropFiles {
	    a?: string;
	    b?: string;
	
	    static createFrom(source: any = {}) {
	        return new CompareDropFiles(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.a = source["a"];
	        this.b = source["b"];
	    }
	}
	export class CompileOptions {
	    side: string;
	    path: string;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new CompileOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.side = source["side"];
	        this.path = source["path"];
	        this.text = source["text"];
	    }
	}
	export class ContentOptions {
	    side: string;
	    path: string;
	    charset: string;
	    rightCharset: string;
	    diffModel?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ContentOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.side = source["side"];
	        this.path = source["path"];
	        this.charset = source["charset"];
	        this.rightCharset = source["rightCharset"];
	        this.diffModel = source["diffModel"];
	    }
	}
	export class ContentPane {
	    path: string;
	    lines: content.Line[];
	
	    static createFrom(source: any = {}) {
	        return new ContentPane(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.lines = this.convertValues(source["lines"], content.Line);
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
	export class ContentView {
	    mode: string;
	    title: string;
	    diffMode: boolean;
	    diffModel: boolean;
	    lang: string;
	    ext: string;
	    leftCharset?: string;
	    rightCharset?: string;
	    message?: string;
	    left?: ContentPane;
	    right?: ContentPane;
	    blocks?: content.SplitBlock[];
	
	    static createFrom(source: any = {}) {
	        return new ContentView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.title = source["title"];
	        this.diffMode = source["diffMode"];
	        this.diffModel = source["diffModel"];
	        this.lang = source["lang"];
	        this.ext = source["ext"];
	        this.leftCharset = source["leftCharset"];
	        this.rightCharset = source["rightCharset"];
	        this.message = source["message"];
	        this.left = this.convertValues(source["left"], ContentPane);
	        this.right = this.convertValues(source["right"], ContentPane);
	        this.blocks = this.convertValues(source["blocks"], content.SplitBlock);
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
	export class MatchCandidate {
	    path: string;
	    name: string;
	    size: number;
	    md5?: string;
	
	    static createFrom(source: any = {}) {
	        return new MatchCandidate(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.size = source["size"];
	        this.md5 = source["md5"];
	    }
	}
	export class DropMatch {
	    fileName: string;
	    srcPath: string;
	    srcSize?: number;
	    srcMd5?: string;
	    candidates?: MatchCandidate[];
	    placeholders?: MatchCandidate[];
	    scanNote?: string;
	
	    static createFrom(source: any = {}) {
	        return new DropMatch(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.fileName = source["fileName"];
	        this.srcPath = source["srcPath"];
	        this.srcSize = source["srcSize"];
	        this.srcMd5 = source["srcMd5"];
	        this.candidates = this.convertValues(source["candidates"], MatchCandidate);
	        this.placeholders = this.convertValues(source["placeholders"], MatchCandidate);
	        this.scanNote = source["scanNote"];
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
	export class MappedLink {
	    appDom: string;
	    patchDom: string;
	
	    static createFrom(source: any = {}) {
	        return new MappedLink(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.appDom = source["appDom"];
	        this.patchDom = source["patchDom"];
	    }
	}
	export class ManualDelItem {
	    path: string;
	    name: string;
	    size: number;
	
	    static createFrom(source: any = {}) {
	        return new ManualDelItem(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.size = source["size"];
	    }
	}
	export class Settings {
	    withPath: boolean;
	    withName: boolean;
	    patchFileRegex: string;
	    writePatchSign: boolean;
	    patchFileDiff: boolean;
	    historyLimit: number;
	    username: string;
	    readmeFile: string;
	    showMappedLinks: boolean;
	
	    static createFrom(source: any = {}) {
	        return new Settings(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.withPath = source["withPath"];
	        this.withName = source["withName"];
	        this.patchFileRegex = source["patchFileRegex"];
	        this.writePatchSign = source["writePatchSign"];
	        this.patchFileDiff = source["patchFileDiff"];
	        this.historyLimit = source["historyLimit"];
	        this.username = source["username"];
	        this.readmeFile = source["readmeFile"];
	        this.showMappedLinks = source["showMappedLinks"];
	    }
	}
	export class PatchTotal {
	    add: number;
	    mod: number;
	    del: number;
	    manualDel: number;
	
	    static createFrom(source: any = {}) {
	        return new PatchTotal(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.add = source["add"];
	        this.mod = source["mod"];
	        this.del = source["del"];
	        this.manualDel = source["manualDel"];
	    }
	}
	export class PackStats {
	    files: number;
	    dirs: number;
	    classes: number;
	    jars: number;
	    totalSize: number;
	
	    static createFrom(source: any = {}) {
	        return new PackStats(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.files = source["files"];
	        this.dirs = source["dirs"];
	        this.classes = source["classes"];
	        this.jars = source["jars"];
	        this.totalSize = source["totalSize"];
	    }
	}
	export class MenuItemView {
	    id: string;
	    label: string;
	    radio?: boolean;
	    checked?: boolean;
	    disabled?: boolean;
	    children?: MenuItemView[];
	
	    static createFrom(source: any = {}) {
	        return new MenuItemView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.radio = source["radio"];
	        this.checked = source["checked"];
	        this.disabled = source["disabled"];
	        this.children = this.convertValues(source["children"], MenuItemView);
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
	export class NodeView {
	    name: string;
	    path: string;
	    dir: boolean;
	    size: number;
	    type: string;
	    modTime: string;
	    md5?: string;
	    children?: NodeView[];
	    compress?: boolean;
	    icon?: string;
	    mapped?: boolean;
	    edited?: boolean;
	    wholeReplace?: boolean;
	    menu?: MenuItemView[];
	
	    static createFrom(source: any = {}) {
	        return new NodeView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.dir = source["dir"];
	        this.size = source["size"];
	        this.type = source["type"];
	        this.modTime = source["modTime"];
	        this.md5 = source["md5"];
	        this.children = this.convertValues(source["children"], NodeView);
	        this.compress = source["compress"];
	        this.icon = source["icon"];
	        this.mapped = source["mapped"];
	        this.edited = source["edited"];
	        this.wholeReplace = source["wholeReplace"];
	        this.menu = this.convertValues(source["menu"], MenuItemView);
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
	export class State {
	    appPath: string;
	    patchPath: string;
	    appTree?: NodeView;
	    patchTree?: NodeView;
	    appStats: PackStats;
	    patchStats: PackStats;
	    total: PatchTotal;
	    settings: Settings;
	    diffInfo: string;
	    readmeText: string;
	    progress: string[];
	    matched: boolean;
	    focusTree: number;
	    manualDelList: ManualDelItem[];
	    manualDelPending?: boolean;
	    mappedLinks?: MappedLink[];
	
	    static createFrom(source: any = {}) {
	        return new State(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.appPath = source["appPath"];
	        this.patchPath = source["patchPath"];
	        this.appTree = this.convertValues(source["appTree"], NodeView);
	        this.patchTree = this.convertValues(source["patchTree"], NodeView);
	        this.appStats = this.convertValues(source["appStats"], PackStats);
	        this.patchStats = this.convertValues(source["patchStats"], PackStats);
	        this.total = this.convertValues(source["total"], PatchTotal);
	        this.settings = this.convertValues(source["settings"], Settings);
	        this.diffInfo = source["diffInfo"];
	        this.readmeText = source["readmeText"];
	        this.progress = source["progress"];
	        this.matched = source["matched"];
	        this.focusTree = source["focusTree"];
	        this.manualDelList = this.convertValues(source["manualDelList"], ManualDelItem);
	        this.manualDelPending = source["manualDelPending"];
	        this.mappedLinks = this.convertValues(source["mappedLinks"], MappedLink);
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
	export class DropResult {
	    action: string;
	    state?: State;
	    path?: string;
	    fileName?: string;
	    srcPath?: string;
	    srcSize?: number;
	    srcMd5?: string;
	    count?: number;
	    items?: DropMatch[];
	
	    static createFrom(source: any = {}) {
	        return new DropResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.action = source["action"];
	        this.state = this.convertValues(source["state"], State);
	        this.path = source["path"];
	        this.fileName = source["fileName"];
	        this.srcPath = source["srcPath"];
	        this.srcSize = source["srcSize"];
	        this.srcMd5 = source["srcMd5"];
	        this.count = source["count"];
	        this.items = this.convertValues(source["items"], DropMatch);
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
	export class EditorOptions {
	    side: string;
	    path: string;
	    charset: string;
	    asBytes: boolean;
	
	    static createFrom(source: any = {}) {
	        return new EditorOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.side = source["side"];
	        this.path = source["path"];
	        this.charset = source["charset"];
	        this.asBytes = source["asBytes"];
	    }
	}
	export class EditorView {
	    mode: string;
	    side: string;
	    path: string;
	    title: string;
	    lang: string;
	    charset: string;
	    text?: string;
	    base64?: string;
	    size: number;
	    edited: boolean;
	    canCompile?: boolean;
	    compileHint?: string;
	
	    static createFrom(source: any = {}) {
	        return new EditorView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.mode = source["mode"];
	        this.side = source["side"];
	        this.path = source["path"];
	        this.title = source["title"];
	        this.lang = source["lang"];
	        this.charset = source["charset"];
	        this.text = source["text"];
	        this.base64 = source["base64"];
	        this.size = source["size"];
	        this.edited = source["edited"];
	        this.canCompile = source["canCompile"];
	        this.compileHint = source["compileHint"];
	    }
	}
	export class FileCompareOptions {
	    left: string;
	    right: string;
	    charset: string;
	    rightCharset: string;
	    diffModel?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new FileCompareOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.left = source["left"];
	        this.right = source["right"];
	        this.charset = source["charset"];
	        this.rightCharset = source["rightCharset"];
	        this.diffModel = source["diffModel"];
	    }
	}
	export class LocateResult {
	    state?: State;
	    side: string;
	    ancestors: string[];
	    path: string;
	
	    static createFrom(source: any = {}) {
	        return new LocateResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.state = this.convertValues(source["state"], State);
	        this.side = source["side"];
	        this.ancestors = source["ancestors"];
	        this.path = source["path"];
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
	
	
	
	
	
	
	export class PatchResult {
	    output: string;
	    total: PatchTotal;
	    diffInfo: string;
	
	    static createFrom(source: any = {}) {
	        return new PatchResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.output = source["output"];
	        this.total = this.convertValues(source["total"], PatchTotal);
	        this.diffInfo = source["diffInfo"];
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
	export class PatchSignView {
	    name: string;
	    md5: string;
	    sha1: string;
	
	    static createFrom(source: any = {}) {
	        return new PatchSignView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.md5 = source["md5"];
	        this.sha1 = source["sha1"];
	    }
	}
	
	export class PathTarget {
	    path: string;
	    name?: string;
	    size?: number;
	    dir?: boolean;
	    placeholder?: boolean;
	    missing?: boolean;
	
	    static createFrom(source: any = {}) {
	        return new PathTarget(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.name = source["name"];
	        this.size = source["size"];
	        this.dir = source["dir"];
	        this.placeholder = source["placeholder"];
	        this.missing = source["missing"];
	    }
	}
	export class SaveOptions {
	    side: string;
	    path: string;
	    mode: string;
	    text: string;
	    base64: string;
	    charset: string;
	
	    static createFrom(source: any = {}) {
	        return new SaveOptions(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.side = source["side"];
	        this.path = source["path"];
	        this.mode = source["mode"];
	        this.text = source["text"];
	        this.base64 = source["base64"];
	        this.charset = source["charset"];
	    }
	}
	export class SearchItemView {
	    name: string;
	    path: string;
	    side?: string;
	    nodePath?: string;
	    ancestors?: string[];
	    icon?: string;
	    history?: boolean;
	    searchTime?: string;
	    target?: string;
	
	    static createFrom(source: any = {}) {
	        return new SearchItemView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.side = source["side"];
	        this.nodePath = source["nodePath"];
	        this.ancestors = source["ancestors"];
	        this.icon = source["icon"];
	        this.history = source["history"];
	        this.searchTime = source["searchTime"];
	        this.target = source["target"];
	    }
	}
	

}

export namespace service {
	
	export class CompareEntry {
	    path: string;
	    size: number;
	    md5: string;
	    modTime?: string;
	
	    static createFrom(source: any = {}) {
	        return new CompareEntry(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.size = source["size"];
	        this.md5 = source["md5"];
	        this.modTime = source["modTime"];
	    }
	}
	export class CompareChange {
	    path: string;
	    a: CompareEntry;
	    b: CompareEntry;
	
	    static createFrom(source: any = {}) {
	        return new CompareChange(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.path = source["path"];
	        this.a = this.convertValues(source["a"], CompareEntry);
	        this.b = this.convertValues(source["b"], CompareEntry);
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
	
	export class CompareFileResult {
	    kind: string;
	    pathA: string;
	    pathB: string;
	    nameA: string;
	    nameB: string;
	    sizeA: number;
	    sizeB: number;
	    md5A: string;
	    md5B: string;
	    same: boolean;
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new CompareFileResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.pathA = source["pathA"];
	        this.pathB = source["pathB"];
	        this.nameA = source["nameA"];
	        this.nameB = source["nameB"];
	        this.sizeA = source["sizeA"];
	        this.sizeB = source["sizeB"];
	        this.md5A = source["md5A"];
	        this.md5B = source["md5B"];
	        this.same = source["same"];
	        this.text = source["text"];
	    }
	}
	export class ComparePackResult {
	    kind: string;
	    pathA: string;
	    pathB: string;
	    nameA: string;
	    nameB: string;
	    totalA: number;
	    totalB: number;
	    same: number;
	    added: CompareEntry[];
	    removed: CompareEntry[];
	    changed: CompareChange[];
	    text: string;
	
	    static createFrom(source: any = {}) {
	        return new ComparePackResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.kind = source["kind"];
	        this.pathA = source["pathA"];
	        this.pathB = source["pathB"];
	        this.nameA = source["nameA"];
	        this.nameB = source["nameB"];
	        this.totalA = source["totalA"];
	        this.totalB = source["totalB"];
	        this.same = source["same"];
	        this.added = this.convertValues(source["added"], CompareEntry);
	        this.removed = this.convertValues(source["removed"], CompareEntry);
	        this.changed = this.convertValues(source["changed"], CompareChange);
	        this.text = source["text"];
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

