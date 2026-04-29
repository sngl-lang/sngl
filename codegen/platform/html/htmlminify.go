package html

import (
	"fmt"
	"strings"
)

// minifyHTML strips comments and collapses whitespace in markup regions while
// leaving <script> and <style> contents verbatim. esbuild's Go API has no
// HTML loader, so this is an in-process pass that works under both native
// and js/wasm builds.
func minifyHTML(src string) (string, error) {
	var out strings.Builder
	out.Grow(len(src))
	i := 0
	for i < len(src) {
		if end, ok := matchVerbatimTag(src, i); ok {
			out.WriteString(src[i:end])
			i = end
			continue
		}
		if strings.HasPrefix(src[i:], "<!--") {
			j := strings.Index(src[i+4:], "-->")
			if j < 0 {
				return "", fmt.Errorf("minifyHTML: unterminated comment")
			}
			i += 4 + j + 3
			continue
		}
		c := src[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			j := i
			for j < len(src) {
				cj := src[j]
				if cj != ' ' && cj != '\t' && cj != '\n' && cj != '\r' {
					break
				}
				j++
			}
			prevAngle := out.Len() > 0 && out.String()[out.Len()-1] == '>'
			nextAngle := j < len(src) && src[j] == '<'
			if !prevAngle && !nextAngle {
				out.WriteByte(' ')
			}
			i = j
			continue
		}
		out.WriteByte(c)
		i++
	}
	return out.String(), nil
}

// matchVerbatimTag returns the end index just past </script> or </style> if
// src[i:] opens with one of those tags. Bodies are emitted verbatim so
// already-minified JS / inline CSS isn't disturbed.
func matchVerbatimTag(src string, i int) (end int, ok bool) {
	for _, t := range [...]string{"script", "style"} {
		open := "<" + t
		if !strings.HasPrefix(strings.ToLower(src[i:min(i+len(open)+1, len(src))]), open) {
			continue
		}
		k := i + len(open)
		if k >= len(src) {
			continue
		}
		ch := src[k]
		if ch != ' ' && ch != '\t' && ch != '\n' && ch != '\r' && ch != '>' && ch != '/' {
			continue
		}
		closeTag := "</" + t
		idx := strings.Index(strings.ToLower(src[i:]), closeTag)
		if idx < 0 {
			return 0, false
		}
		gt := strings.IndexByte(src[i+idx:], '>')
		if gt < 0 {
			return 0, false
		}
		return i + idx + gt + 1, true
	}
	return 0, false
}
