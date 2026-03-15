//go:build !js || !wasm

package playground

import "embed"

//go:embed assets/*
var Assets embed.FS
