// JSON-RPC 2.0 message helpers. Wire is line-delimited JSON; this
// module handles individual message frames (no transport — that's
// in testagent.js).

export function encodeMessage(msg) {
    const obj = { jsonrpc: '2.0' };
    if (msg.id !== undefined) obj.id = msg.id;
    if (msg.method !== undefined) obj.method = msg.method;
    if (msg.params !== undefined) obj.params = msg.params;
    if (msg.result !== undefined) obj.result = msg.result;
    if (msg.error !== undefined) obj.error = msg.error;
    return JSON.stringify(obj);
}

export function decodeMessage(text) {
    const obj = JSON.parse(text);  // throws on bad JSON
    return {
        id: obj.id,
        method: obj.method,
        params: obj.params,
        result: obj.result,
        error: obj.error,
    };
}

// isNotification: request without id (no response expected).
export function isNotification(msg) {
    return msg.method !== undefined && msg.id === undefined;
}

// isResponse: id present, no method (matches a previously-sent request).
export function isResponse(msg) {
    return msg.id !== undefined && msg.method === undefined;
}
