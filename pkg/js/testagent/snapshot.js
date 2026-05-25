// Snapshots stores the per-platform capture function and a name
// prefix that scopes goldens per testRunner. html platform uses ""
// (single runner); android-equivalent platforms use "robolectric/"
// or "device/".
export const Snapshots = {
    _capture: null,
    namePrefix: '',
    register(prefix, fn) { this._capture = fn; this.namePrefix = prefix; },
    capture() { return this._capture; },
    reset() { this._capture = null; this.namePrefix = ''; },
};
