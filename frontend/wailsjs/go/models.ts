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
	    proxyPort: number;
	    configPath: string;
	    firstRun: boolean;
	
	    static createFrom(source: any = {}) {
	        return new SettingsView(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.targetURL = source["targetURL"];
	        this.proxyPort = source["proxyPort"];
	        this.configPath = source["configPath"];
	        this.firstRun = source["firstRun"];
	    }
	}

}

