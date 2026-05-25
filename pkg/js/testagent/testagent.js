import { T, AbortSentinel } from './t.js';
import { Registry } from './registry.js';

// Sink wraps a WebSocket plus a pending-id table for response routing.
function makeSink(ws) {
    let nextId = 0;
    const pending = new Map();
    const sink = {
        send(text) { ws.send(text); },
        allocId() { return ++nextId; },
        pending,
    };
    ws.addEventListener('message', e => {
        const msg = JSON.parse(e.data);
        if (msg.id !== undefined && msg.method === undefined) {
            const resolve = pending.get(msg.id);
            if (resolve) {
                pending.delete(msg.id);
                resolve(msg);
            }
            return;
        }
        if (msg.id !== undefined && msg.method !== undefined) {
            handleRequest(msg, sink);
        }
    });
    return sink;
}

async function handleRequest(msg, sink) {
    const reply = (result, error) => {
        const obj = { jsonrpc: '2.0', id: msg.id };
        if (error) obj.error = error;
        else obj.result = result;
        sink.send(JSON.stringify(obj));
    };

    switch (msg.method) {
        case 'list':
            reply({ tests: Registry.list() });
            return;
        case 'run':
            await runFiltered(sink, msg.params?.filter ?? '');
            reply({});
            return;
        case 'cancel':
            reply({});
            return;
        default:
            reply(null, { code: -32601, message: `method not found: ${msg.method}` });
    }
}

async function runFiltered(sink, filter) {
    let passed = 0, failed = 0, skipped = 0;
    for (const name of Registry.list()) {
        if (filter && !name.includes(filter)) continue;
        const status = await runOne(sink, name);
        if (status === 'pass') passed++;
        else if (status === 'fail') failed++;
        else if (status === 'skip') skipped++;
    }
    sink.send(JSON.stringify({
        jsonrpc: '2.0',
        method: 'runComplete',
        params: { passed, failed, skipped },
    }));
}

async function runOne(sink, name) {
    const fn = Registry.get(name);
    sink.send(JSON.stringify({
        jsonrpc: '2.0',
        method: 'testStart',
        params: { test: name },
    }));
    const t = new T(name, sink);
    const start = Date.now();
    try {
        await fn(t);
    } catch (e) {
        if (!(e instanceof AbortSentinel)) {
            t.failed = true;
            sink.send(JSON.stringify({
                jsonrpc: '2.0',
                method: 'log',
                params: { test: name, msg: `exception: ${e.message}` },
            }));
        }
    }
    const status = t.skipped ? 'skip' : (t.failed ? 'fail' : 'pass');
    sink.send(JSON.stringify({
        jsonrpc: '2.0',
        method: 'testEnd',
        params: { test: name, status, durationMs: Date.now() - start },
    }));
    return status;
}

// main is the entry point the emitted page's <script> calls on
// DOMContentLoaded. Reads sngl_port from the URL query string,
// opens a WebSocket, and waits for the dispatcher to drive it.
export async function main() {
    const port = new URLSearchParams(window.location.search).get('sngl_port');
    if (!port) {
        console.warn('sngl testagent: no sngl_port query param — agent will not connect');
        return;
    }
    const ws = new WebSocket(`ws://127.0.0.1:${port}/agent`);
    await new Promise(resolve => {
        ws.addEventListener('open', resolve, { once: true });
    });
    makeSink(ws);
}

// Re-export for codegen-emitted modules.
export { T, AbortSentinel } from './t.js';
export { Registry } from './registry.js';
export { Snapshots } from './snapshot.js';
