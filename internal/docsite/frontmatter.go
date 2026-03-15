package docsite

import (
	"bytes"

	"gopkg.in/yaml.v3"
)

// FrontMatter holds YAML metadata parsed from the top of markdown files.
type FrontMatter struct {
	Title       string `yaml:"title"`
	Order       int    `yaml:"order"`
	Description string `yaml:"description"`
}

var separator = []byte("---")

// ParseFrontMatter splits a markdown file into front matter and body.
// If no front matter is present, an empty FrontMatter is returned with the full content as body.
func ParseFrontMatter(data []byte) (FrontMatter, []byte, error) {
	data = bytes.TrimLeft(data, "\n\r")
	if !bytes.HasPrefix(data, separator) {
		return FrontMatter{}, data, nil
	}

	rest := data[len(separator):]
	end := bytes.Index(rest, separator)
	if end < 0 {
		return FrontMatter{}, data, nil
	}

	var fm FrontMatter
	if err := yaml.Unmarshal(rest[:end], &fm); err != nil {
		return FrontMatter{}, nil, err
	}

	body := rest[end+len(separator):]
	body = bytes.TrimLeft(body, "\n\r")
	return fm, body, nil
}
