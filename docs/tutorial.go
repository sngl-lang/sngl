package docs

import (
	"regexp"
	"strings"

	"duckfam.us/sngl/internal/docsite"
	"duckfam.us/sngl/internal/parser"
)

// Section groups lessons under a top-level (`#`) heading.
type Section struct {
	Title   string
	Slug    string
	Lessons []Lesson
}

type Lesson struct {
	Title    string
	Slug     string // "<section-slug>/<lesson-slug>"
	BodyHTML string // rendered prose (code fences other than the final sngl block stay inline)
	Code     string // seed loaded into the playground editor
}

// Tutorial parses docs/learn/_tour.md and returns the lesson tree.
//
//sngl:pure
func Tutorial() []Section {
	data, err := content.ReadFile("learn/_tour.md")
	if err != nil {
		return nil
	}
	return parseWalkthrough(string(data))
}

var htmlCommentRE = regexp.MustCompile(`(?s)<!--.*?-->`)

func parseWalkthrough(src string) []Section {
	src = htmlCommentRE.ReplaceAllString(src, "")

	var (
		sections []Section
		cur      *Section
		lesson   *Lesson
		proseBuf strings.Builder
		codeBuf  strings.Builder
		inCode   bool
		codeLang string
		// Collected sngl fences for the current lesson, most recent last.
		fences []string
	)

	flushLesson := func() {
		if lesson == nil {
			return
		}
		prose := proseBuf.String()
		seed := ""
		// Drop the final sngl fence from the prose and use it as the seed.
		if len(fences) > 0 {
			seed = fences[len(fences)-1]
			marker := "\x00SNGL_FENCE_" + itoa(len(fences)-1) + "\x00"
			prose = strings.Replace(prose, marker, "", 1)
		}
		for i := 0; i < len(fences)-1; i++ {
			marker := "\x00SNGL_FENCE_" + itoa(i) + "\x00"
			prose = strings.Replace(prose, marker, "```sngl\n"+fences[i]+"```\n", 1)
		}
		if html, err := docsite.RenderMarkdown([]byte(prose)); err == nil {
			lesson.BodyHTML = string(html)
		}
		lesson.Code = strings.TrimSpace(seed)
		cur.Lessons = append(cur.Lessons, *lesson)
		lesson = nil
		proseBuf.Reset()
		fences = nil
	}

	flushSection := func() {
		flushLesson()
		if cur != nil {
			sections = append(sections, *cur)
			cur = nil
		}
	}

	for line := range strings.SplitSeq(src, "\n") {
		trim := strings.TrimSpace(line)

		if inCode {
			if strings.HasPrefix(trim, "```") {
				inCode = false
				if codeLang == "sngl" {
					fences = append(fences, codeBuf.String())
					proseBuf.WriteString("\x00SNGL_FENCE_")
					proseBuf.WriteString(itoa(len(fences) - 1))
					proseBuf.WriteString("\x00\n")
				} else {
					proseBuf.WriteString("```" + codeLang + "\n")
					proseBuf.WriteString(codeBuf.String())
					proseBuf.WriteString("```\n")
				}
				codeBuf.Reset()
				codeLang = ""
				continue
			}
			codeBuf.WriteString(line)
			codeBuf.WriteByte('\n')
			continue
		}

		if strings.HasPrefix(trim, "```") {
			inCode = true
			codeLang = strings.TrimPrefix(trim, "```")
			continue
		}

		if strings.HasPrefix(line, "# ") {
			flushSection()
			title := strings.TrimSpace(line[2:])
			cur = &Section{Title: title, Slug: slugify(title)}
			continue
		}
		if strings.HasPrefix(line, "## ") {
			if cur == nil {
				cur = &Section{Title: "Tutorial", Slug: "tutorial"}
			}
			flushLesson()
			title := strings.TrimSpace(line[3:])
			lesson = &Lesson{Title: title, Slug: cur.Slug + "/" + slugify(title)}
			continue
		}

		if lesson != nil {
			proseBuf.WriteString(line)
			proseBuf.WriteByte('\n')
		}
	}
	flushSection()

	// Drop lessons whose seed fails to parse — keeps a typo from breaking the whole tour.
	for si := range sections {
		kept := sections[si].Lessons[:0]
		for _, l := range sections[si].Lessons {
			if l.Code == "" {
				continue
			}
			if _, err := parser.Parse("lesson.sngl", []byte(l.Code)); err != nil {
				continue
			}
			kept = append(kept, l)
		}
		sections[si].Lessons = kept
	}
	out := sections[:0]
	for _, s := range sections {
		if len(s.Lessons) > 0 {
			out = append(out, s)
		}
	}
	return out
}

var slugRE = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(s)
	s = slugRE.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[pos:])
}
