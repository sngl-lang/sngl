// Registry maps test names to their callable functions. Codegen-
// emitted init blocks register each `test*` SNGL function here.
export const Registry = {
    _tests: new Map(),
    register(name, fn) { this._tests.set(name, fn); },
    get(name) { return this._tests.get(name); },
    list() { return Array.from(this._tests.keys()).sort(); },
    reset() { this._tests.clear(); },
};
