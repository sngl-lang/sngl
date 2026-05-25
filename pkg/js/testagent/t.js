import { Snapshots } from './snapshot.js';

// AbortSentinel marks a control-flow unwind from failNow/skip/fatal.
// Caught by the per-test dispatcher in testagent.js; not user-facing.
export class AbortSentinel extends Error {
    constructor() { super('sngl test abort'); this.name = 'AbortSentinel'; }
}

// T is the per-test handle exposed to user test functions. API mirrors
// pkg/go/testagent.T and pkg/kotlin/testagent.T.
export class T {
    constructor(name, sink) {
        this.name = name;
        this.sink = sink;
        this.failed = false;
        this.skipped = false;
    }

    _notify(method, params) {
        this.sink.send(JSON.stringify({ jsonrpc: '2.0', method, params }));
    }

    log(msg) { this._notify('log', { test: this.name, msg }); }

    fail() {
        this.failed = true;
        this._notify('markFail', { test: this.name });
    }

    failNow() {
        this.fail();
        throw new AbortSentinel();
    }

    skip(reason) {
        this.skipped = true;
        this._notify('markSkip', { test: this.name, reason });
        throw new AbortSentinel();
    }

    error(msg) {
        this.log(msg);
        this.fail();
    }

    fatal(msg) {
        this.error(msg);
        throw new AbortSentinel();
    }

    assertTrue(b, msg) {
        if (!b) this.fatal(msg);
    }

    async wait(ms) {
        await new Promise(resolve => setTimeout(resolve, ms));
    }

    async snapshot(name) {
        const capture = Snapshots.capture();
        if (!capture) {
            this.error(`snapshot "${name}": no capture registered for this platform`);
            return;
        }
        let mime, bytes;
        try {
            [mime, bytes] = capture();
        } catch (e) {
            this.error(`snapshot "${name}": capture: ${e.message}`);
            return;
        }
        const prefixed = Snapshots.namePrefix + name;
        const b64 = btoa(String.fromCharCode(...new Uint8Array(bytes)));
        const id = this.sink.allocId();
        const promise = new Promise(resolve => {
            this.sink.pending.set(id, resolve);
        });
        this.sink.send(JSON.stringify({
            jsonrpc: '2.0',
            id,
            method: 'snapshotAssert',
            params: { test: this.name, name: prefixed, mime, bytes: b64 },
        }));
        const response = await promise;
        if (response.error) {
            this.error(`snapshot "${name}": driver: ${response.error.message}`);
            return;
        }
        if (!response.result?.pass) {
            this.error(`snapshot "${name}" mismatch:\n${response.result?.diff ?? ''}`);
        }
    }
}
