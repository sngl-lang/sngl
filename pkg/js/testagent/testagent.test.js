import { test } from 'node:test';
import assert from 'node:assert/strict';
import { encodeMessage, decodeMessage } from './rpc.js';
import { T, AbortSentinel } from './t.js';
import { Registry } from './registry.js';
import { Snapshots } from './snapshot.js';

test('encodes a notification', () => {
    const msg = encodeMessage({ method: 'log', params: { test: 'T1', msg: 'hi' } });
    const parsed = decodeMessage(msg);
    assert.equal(parsed.method, 'log');
    assert.equal(parsed.id, undefined);
    assert.deepEqual(parsed.params, { test: 'T1', msg: 'hi' });
});

test('encodes a request with id', () => {
    const msg = encodeMessage({ id: 7, method: 'list', params: {} });
    const parsed = decodeMessage(msg);
    assert.equal(parsed.id, 7);
    assert.equal(parsed.method, 'list');
});

test('decodes a response', () => {
    const msg = encodeMessage({ id: 7, result: { tests: ['a', 'b'] } });
    const parsed = decodeMessage(msg);
    assert.equal(parsed.method, undefined);
    assert.equal(parsed.id, 7);
    assert.deepEqual(parsed.result, { tests: ['a', 'b'] });
});

test('rejects malformed JSON', () => {
    assert.throws(() => decodeMessage('not json'));
});

// Mock WS sink — captures emitted messages instead of sending.
function makeSink() {
    const messages = [];
    const pending = new Map();
    return {
        messages,
        pending,
        send(text) {
            const m = JSON.parse(text);
            messages.push(m);
        },
        resolvePending(id, response) {
            const r = pending.get(id);
            if (r) r(response);
        },
    };
}

test('T.log emits a notification', () => {
    const sink = makeSink();
    const t = new T('myTest', sink);
    t.log('hello');
    assert.equal(sink.messages.length, 1);
    assert.equal(sink.messages[0].method, 'log');
    assert.deepEqual(sink.messages[0].params, { test: 'myTest', msg: 'hello' });
});

test('T.failNow throws AbortSentinel after marking failed', () => {
    const sink = makeSink();
    const t = new T('myTest', sink);
    assert.throws(() => t.failNow(), e => e instanceof AbortSentinel);
    assert.equal(t.failed, true);
});

test('T.error logs and marks failed without throwing', () => {
    const sink = makeSink();
    const t = new T('myTest', sink);
    t.error('oops');
    assert.equal(t.failed, true);
    const logMsg = sink.messages.find(m => m.method === 'log');
    assert.ok(logMsg);
    assert.equal(logMsg.params.msg, 'oops');
});

test('Registry enumerates registered tests', () => {
    Registry.reset();
    Registry.register('foo', t => t.log('ran'));
    Registry.register('bar', t => t.log('ran'));
    assert.deepEqual(Registry.list(), ['bar', 'foo']);
});

test('Snapshots stores prefix + capture fn', () => {
    Snapshots.reset();
    Snapshots.register('html/', () => ['text/html', new Uint8Array([1, 2, 3])]);
    assert.equal(Snapshots.namePrefix, 'html/');
    const [mime, bytes] = Snapshots.capture()();
    assert.equal(mime, 'text/html');
    assert.deepEqual(Array.from(bytes), [1, 2, 3]);
});
