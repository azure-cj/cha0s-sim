export namespace config {
	
	export class CreateRuleRequest {
	    name: string;
	    path: string;
	    methods: string[];
	    frequency: string;
	    effect: string;
	    slowMs?: number;
	    errorCode?: number;
	    targetKeys?: string[];
	    attack?: string;
	
	    static createFrom(source: any = {}) {
	        return new CreateRuleRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.methods = source["methods"];
	        this.frequency = source["frequency"];
	        this.effect = source["effect"];
	        this.slowMs = source["slowMs"];
	        this.errorCode = source["errorCode"];
	        this.targetKeys = source["targetKeys"];
	        this.attack = source["attack"];
	    }
	}

}

export namespace discovery {
	
	export class Endpoint {
	    method: string;
	    path: string;
	    seenCount: number;
	    // Go type: time
	    lastSeen: any;
	    sessionName: string;
	
	    static createFrom(source: any = {}) {
	        return new Endpoint(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.method = source["method"];
	        this.path = source["path"];
	        this.seenCount = source["seenCount"];
	        this.lastSeen = this.convertValues(source["lastSeen"], null);
	        this.sessionName = source["sessionName"];
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

export namespace main {
	
	export class RuleView {
	    name: string;
	    path?: string;
	    pathRegex?: string;
	    methods?: string[];
	    errorRate: number;
	    enabled: boolean;
	    hasLatency: boolean;
	    hasStatusOverride: boolean;
	    hasDropConnection: boolean;
	    hasMangle: boolean;
	    hasFuzz: boolean;
	
	    static createFrom(source: any = {}) {
	        return new RuleView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.path = source["path"];
	        this.pathRegex = source["pathRegex"];
	        this.methods = source["methods"];
	        this.errorRate = source["errorRate"];
	        this.enabled = source["enabled"];
	        this.hasLatency = source["hasLatency"];
	        this.hasStatusOverride = source["hasStatusOverride"];
	        this.hasDropConnection = source["hasDropConnection"];
	        this.hasMangle = source["hasMangle"];
	        this.hasFuzz = source["hasFuzz"];
	    }
	}
	export class SettingsView {
	    targetURL: string;
	    configPath: string;
	    firstRun: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.targetURL = source["targetURL"];
	        this.configPath = source["configPath"];
	        this.firstRun = source["firstRun"];
	    }
	}
	export class StressConfig {
	    targetURL: string;
	    targetRPS: number;
	    durationSec: number;
	    concurrency: number;
	    shape: string;
	    endRPS?: number;
	    spikeRPS?: number;
	
	    static createFrom(source: any = {}) {
	        return new StressConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.targetURL = source["targetURL"];
	        this.targetRPS = source["targetRPS"];
	        this.durationSec = source["durationSec"];
	        this.concurrency = source["concurrency"];
	        this.shape = source["shape"];
	        this.endRPS = source["endRPS"];
	        this.spikeRPS = source["spikeRPS"];
	    }
	}

}

