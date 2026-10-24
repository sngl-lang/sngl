package gtk4

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"duckfam.us/sngl/codegen/platform/gtk4/gir"
	"duckfam.us/sngl/internal/gencache"
)

// The introspection data reaches this platform as two stored SNGL files, each
// a producer's output (see internal/gencache):
//
//   - gtk4.registry is the parsed registry, as data: `const registry = …`, the
//     whole of what the code generator reads back about each class. Its
//     inputs are the GIR file it was parsed from and, when the file was found
//     by probing, the locations before it that were not there.
//   - gtk4.widgets is the widget declarations derived from that registry, the
//     generated half of sngl:platform/gtk4. Its one input is the registry
//     entry, so it is derived from the registry the generator will read and
//     not from a second parse that could disagree with it.
//
// The declarations are what every build reads, since the checker walks every
// platform's package; the registry only a build targeting gtk4 does. Kept
// apart, a build for another platform checks one small file's inputs and
// decodes nothing.
const (
	registryProducer = "gtk4.registry"
	widgetsProducer  = "gtk4.widgets"
)

func init() {
	gencache.Register(registryProducer, produceRegistry)
	gencache.Register(widgetsProducer, produceWidgets)
}

// girRequest names the GIR a "gir" option selects. A path is made absolute,
// so one option spelled from two directories is one request.
func girRequest(producer, opt string) (gencache.Request, error) {
	if opt != "" && opt != girBuiltin {
		abs, err := filepath.Abs(opt)
		if err != nil {
			return gencache.Request{}, err
		}
		opt = abs
	}
	return gencache.Request{Producer: producer, Params: []string{opt}}, nil
}

// widgetsRequest names the declarations for the GIR opt selects, written over
// C or over gtk4rt as cgo says.
func widgetsRequest(opt string, cgo bool) (gencache.Request, error) {
	req, err := girRequest(widgetsProducer, opt)
	req.Params = append(req.Params, strconv.FormatBool(cgo))
	return req, err
}

// parseGIR is gir.ParseGIR, a variable so a test can count the parses a
// build does.
var parseGIR = gir.ParseGIR

// produceRegistry parses the GIR the option selects, recording what it read
// before reading it.
func produceRegistry(_ *gencache.Store, params []string) (gencache.Output, error) {
	opt := params[0]
	var (
		inputs []gencache.Input
		src    gencache.Input
	)
	switch {
	case opt == girBuiltin:
		// Embedded in the compiler, so the compiler's identity in the key is
		// its input.
	case opt != "":
		p, err := resolveGIRPath(opt)
		if err != nil {
			return gencache.Output{}, err
		}
		if src, err = gencache.File(p); err != nil {
			return gencache.Output{}, err
		}
		inputs = append(inputs, src)
	default:
		// The probe takes the first location that stats cleanly, as
		// resolveGIRPath does, so the ones before it staying absent is part of
		// what the answer depends on -- and when none is present, all of them
		// are. A location that fails for any other reason (a dangling link, a
		// file where a directory should be) is skipped rather than failing the
		// registry; gencache.Absent is checked with the same Stat.
		for _, p := range girAutoPaths {
			if _, serr := os.Stat(p); serr != nil {
				inputs = append(inputs, gencache.Absent(p))
				continue
			}
			var err error
			if src, err = gencache.File(p); err != nil {
				return gencache.Output{}, err
			}
			inputs = append(inputs, src)
			break
		}
	}
	body, err := registryBody(src.Get("path"), src.Get("sha256"))
	if err != nil {
		return gencache.Output{}, err
	}
	return gencache.Output{Inputs: inputs, Body: body}, nil
}

// encoded holds the registry files this process has written, by the digest of
// the GIR each was parsed from ("" for the bundled subset). A store is one
// build's, and a fresh one would otherwise parse an unchanged GIR again.
var encoded = struct {
	sync.Mutex
	bodies map[string][]byte
}{bodies: map[string][]byte{}}

// registryBody is the gtk4.registry file for the GIR at path, whose content
// has digest sum, or for the bundled subset when path is "".
func registryBody(path, sum string) ([]byte, error) {
	encoded.Lock()
	body, ok := encoded.bodies[sum]
	encoded.Unlock()
	if ok {
		return body, nil
	}
	var (
		reg *gir.TypeRegistry
		err error
	)
	if path == "" {
		reg, err = gir.ParseMinimal()
	} else {
		reg, err = parseGIR(path)
	}
	if err != nil {
		return nil, err
	}
	reg.Strip()
	value, err := gencache.EncodeValue(reg)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s%v\n%s", minimalPrefix, path == "", registryPrefix)
	b.Write(value)
	b.WriteByte('\n')
	body = b.Bytes()
	encoded.Lock()
	defer encoded.Unlock()
	if len(encoded.bodies) >= maxRegistries {
		clear(encoded.bodies)
	}
	encoded.bodies[sum] = body
	return body, nil
}

const (
	minimalPrefix  = "const minimal = "
	registryPrefix = "const registry = "
)

type decodedRegistry struct {
	reg     *gir.TypeRegistry
	minimal bool
}

// decoded holds the registries this process has read back, by the digest of
// the stored file. A registry is never written once restored, so every store
// holding the same file -- each script's own store, a fresh one per build --
// shares one decode: the full GTK registry is megabytes of SNGL to parse.
var decoded = struct {
	sync.Mutex
	regs map[[sha256.Size]byte]decodedRegistry
}{regs: map[[sha256.Size]byte]decodedRegistry{}}

// maxRegistries bounds what a long-lived process keeps of registries a changed
// GIR has since replaced.
const maxRegistries = 4

// decodeRegistry reads a gtk4.registry file back into a registry, and whether
// it is the bundled subset.
func decodeRegistry(data []byte) (*gir.TypeRegistry, bool, error) {
	key := sha256.Sum256(data)
	decoded.Lock()
	d, ok := decoded.regs[key]
	decoded.Unlock()
	if ok {
		return d.reg, d.minimal, nil
	}
	reg, minimal, err := decodeRegistryText(data)
	if err != nil {
		return nil, false, err
	}
	decoded.Lock()
	defer decoded.Unlock()
	if len(decoded.regs) >= maxRegistries {
		clear(decoded.regs)
	}
	decoded.regs[key] = decodedRegistry{reg, minimal}
	return reg, minimal, nil
}

func decodeRegistryText(data []byte) (*gir.TypeRegistry, bool, error) {
	body := gencache.Body(data)
	first, rest, _ := bytes.Cut(body, []byte("\n"))
	flag, ok := bytes.CutPrefix(first, []byte(minimalPrefix))
	if !ok {
		return nil, false, errors.New("gtk4: stored registry has no minimal flag")
	}
	value, ok := bytes.CutPrefix(bytes.TrimSpace(rest), []byte(registryPrefix))
	if !ok {
		return nil, false, errors.New("gtk4: stored registry has no registry")
	}
	var reg gir.TypeRegistry
	if err := gencache.DecodeValue(value, &reg); err != nil {
		return nil, false, fmt.Errorf("gtk4: stored registry: %w", err)
	}
	reg.Restore()
	return &reg, string(flag) == "true", nil
}

// produceWidgets writes the widget declarations for the registry the option
// selects.
func produceWidgets(s *gencache.Store, params []string) (gencache.Output, error) {
	req, err := girRequest(registryProducer, params[0])
	if err != nil {
		return gencache.Output{}, err
	}
	in, data, err := s.Entry(req)
	if err != nil {
		return gencache.Output{}, err
	}
	reg, _, err := decodeRegistry(data)
	if err != nil {
		return gencache.Output{}, err
	}
	return gencache.Output{Inputs: []gencache.Input{in}, Body: widgetSource(reg, params[1] == "true")}, nil
}
