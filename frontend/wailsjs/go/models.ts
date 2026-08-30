export namespace main {
	
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

