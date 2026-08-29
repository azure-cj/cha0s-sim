import {useEffect, useState} from 'react';
import {GetStatus, GetRuleCount, StartProxy, StopProxy} from "../wailsjs/go/main/App";

function App() {
    const [status, setStatus] = useState("");
    const [ruleCount, setRuleCount] = useState(0);

    const refresh = () => {
        GetStatus().then(setStatus);
        GetRuleCount().then(setRuleCount);
    };

    useEffect(() => {
        refresh();
    }, []);

    const start = () => {
        StartProxy().then((err) => {
            setStatus(err !== "" ? err : "starting...");
            refresh();
        });
    };

    const stop = () => {
        StopProxy().then((err) => {
            setStatus(err !== "" ? err : "stopping...");
            refresh();
        });
    };

    return (
        <div id="App">
            <div>status: {status}</div>
            <div>rules loaded: {ruleCount}</div>
            <button onClick={start}>Start Proxy</button>
            <button onClick={stop}>Stop Proxy</button>
        </div>
    )
}

export default App