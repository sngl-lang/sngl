package interp

// The interpreter runs a program on the repository's say-so, so it opens no
// platform store: sngl:settings keeps every value in its own memory var.
func init() {
	intrinsics["settings._persistent"] = func([]any) (any, error) { return false, nil }
	intrinsics["settings._read"] = func([]any) (any, error) { return "", nil }
	intrinsics["settings._write"] = func([]any) (any, error) { return nil, nil }
}
