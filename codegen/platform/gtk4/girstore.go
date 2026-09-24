package gtk4

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"git.duckfam.us/jonathan/sngl/codegen/platform/gtk4/gir"
	"git.duckfam.us/jonathan/sngl/internal/gencache"
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

// parseGIR is gir.ParseGIR, a variable so a test can count the parses a
// build does.
var parseGIR = gir.ParseGIR

// produceRegistry parses the GIR the option selects, recording what it read
// before reading it.
func produceRegistry(_ *gencache.Store, params []string) (gencache.Output, error) {
	opt := params[0]
	var (
		inputs  []gencache.Input
		reg     *gir.TypeRegistry
		minimal bool
		err     error
	)
	switch {
	case opt == girBuiltin:
		// Embedded in the compiler, so the compiler's identity in the key is
		// its input.
		reg, err = gir.ParseMinimal()
		minimal = true
	case opt != "":
		p, perr := resolveGIRPath(opt)
		if perr != nil {
			return gencache.Output{}, perr
		}
		in, ferr := gencache.File(p)
		if ferr != nil {
			return gencache.Output{}, ferr
		}
		inputs = append(inputs, in)
		reg, err = parseGIR(p)
	default:
		// The probe takes the first location present, so the ones before it
		// staying absent is part of what the answer depends on -- and when
		// none is present, all of them are.
		for _, p := range girAutoPaths {
			if _, serr := os.Stat(p); errors.Is(serr, os.ErrNotExist) {
				inputs = append(inputs, gencache.Absent(p))
				continue
			}
			in, ferr := gencache.File(p)
			if ferr != nil {
				return gencache.Output{}, ferr
			}
			inputs = append(inputs, in)
			reg, err = parseGIR(p)
			break
		}
		if reg == nil && err == nil {
			reg, err = gir.ParseMinimal()
			minimal = true
		}
	}
	if err != nil {
		return gencache.Output{}, err
	}
	reg.Strip()
	value, err := gencache.EncodeValue(reg)
	if err != nil {
		return gencache.Output{}, err
	}
	var body bytes.Buffer
	fmt.Fprintf(&body, "%s%v\n%s", minimalPrefix, minimal, registryPrefix)
	body.Write(value)
	body.WriteByte('\n')
	return gencache.Output{Inputs: inputs, Body: body.Bytes()}, nil
}

const (
	minimalPrefix  = "const minimal = "
	registryPrefix = "const registry = "
)

// decodeRegistry reads a gtk4.registry file back into a registry, and whether
// it is the bundled subset.
func decodeRegistry(data []byte) (*gir.TypeRegistry, bool, error) {
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
	return gencache.Output{Inputs: []gencache.Input{in}, Body: widgetSource(reg)}, nil
}
