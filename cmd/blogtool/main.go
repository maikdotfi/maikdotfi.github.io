package main

import (
	"bytes"
	"embed"
	"errors"
	"flag"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
)

type post struct {
	Title      string
	Slug       string
	Authors    []string
	AuthorLine string
	Tags       []string
	Date       time.Time
	DateISO    string
	DateHuman  string
	Content    template.HTML
	Excerpt    string
	SourcePath string
	OutputPath string
	Href       string
}

//go:embed templates/*.tmpl
var templateFS embed.FS

var (
	templateFuncs = template.FuncMap{
		"safe": func(s string) template.HTML {
			return template.HTML(escapeText(s))
		},
	}
	postTpl  = template.Must(template.New("post.tmpl").Funcs(templateFuncs).ParseFS(templateFS, "templates/post.tmpl"))
	indexTpl = template.Must(template.New("index.tmpl").Funcs(templateFuncs).ParseFS(templateFS, "templates/index.tmpl"))
)

func main() {
	var blogDir string
	flag.StringVar(&blogDir, "blog", "blog", "directory containing blog markdown files")
	flag.Parse()

	posts, err := loadPosts(blogDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load posts: %v\n", err)
		os.Exit(1)
	}
	if len(posts) == 0 {
		fmt.Fprintln(os.Stderr, "no markdown posts found")
		return
	}

	if err := writePosts(posts); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write posts: %v\n", err)
		os.Exit(1)
	}
	if err := writeIndex(blogDir, posts); err != nil {
		fmt.Fprintf(os.Stderr, "failed to write blog index: %v\n", err)
		os.Exit(1)
	}
}

func loadPosts(blogDir string) ([]*post, error) {
	mdFiles, err := filepath.Glob(filepath.Join(blogDir, "*.md"))
	if err != nil {
		return nil, err
	}
	sort.Strings(mdFiles)

	var posts []*post
	for _, mdPath := range mdFiles {
		p, err := parsePost(blogDir, mdPath)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", mdPath, err)
		}
		posts = append(posts, p)
	}

	sort.SliceStable(posts, func(i, j int) bool {
		if posts[i].Date.Equal(posts[j].Date) {
			return posts[i].Slug > posts[j].Slug
		}
		return posts[i].Date.After(posts[j].Date)
	})
	return posts, nil
}

func parsePost(blogDir, path string) (*post, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	fm, body, err := extractFrontMatter(string(raw))
	if err != nil {
		return nil, err
	}

	content, firstParagraph, headingTitle := markdownToHTML(body)

	slug := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	title := fm.scalar("title")
	if title == "" {
		title = headingTitle
	}
	if title == "" {
		title = titleFromSlug(slug)
	}

	authors := fm.list("authors")
	tags := fm.list("tags")

	var date time.Time
	if ds := fm.scalar("date"); ds != "" {
		if parsed, err := time.Parse("2006-01-02", ds); err == nil {
			date = parsed
		} else {
			return nil, fmt.Errorf("invalid date %q: %w", ds, err)
		}
	} else {
		info, statErr := os.Stat(path)
		if statErr == nil {
			date = info.ModTime()
		} else {
			date = time.Now()
		}
	}

	excerpt := makeExcerpt(firstParagraph, 220)
	if excerpt == "" {
		excerpt = makeExcerpt(body, 220)
	}

	p := &post{
		Title:      title,
		Slug:       slug,
		Authors:    authors,
		AuthorLine: formatAuthors(authors),
		Tags:       tags,
		Date:       date,
		DateISO:    date.Format("2006-01-02"),
		DateHuman:  date.Format("January 2, 2006"),
		Content:    content,
		Excerpt:    excerpt,
		SourcePath: path,
		OutputPath: filepath.Join(blogDir, slug+".html"),
		Href:       slug + ".html",
	}
	return p, nil
}

func writePosts(posts []*post) error {
	for _, p := range posts {
		if err := renderTemplate(postTpl, p.OutputPath, p); err != nil {
			return err
		}
	}
	return nil
}

func writeIndex(blogDir string, posts []*post) error {
	target := filepath.Join(blogDir, "index.html")
	data := struct {
		Posts []*post
	}{
		Posts: posts,
	}
	return renderTemplate(indexTpl, target, data)
}

func renderTemplate(tpl *template.Template, path string, data any) error {
	var buf bytes.Buffer
	if err := tpl.Execute(&buf, data); err != nil {
		return err
	}
	return os.WriteFile(path, buf.Bytes(), fs.FileMode(0o644))
}

type frontMatter struct {
	scalars map[string]string
	lists   map[string][]string
}

func newFrontMatter() frontMatter {
	return frontMatter{
		scalars: make(map[string]string),
		lists:   make(map[string][]string),
	}
}

func (fm frontMatter) scalar(key string) string {
	return fm.scalars[strings.ToLower(key)]
}

func (fm frontMatter) list(key string) []string {
	return fm.lists[strings.ToLower(key)]
}

func extractFrontMatter(raw string) (frontMatter, string, error) {
	raw = strings.TrimLeft(raw, "\ufeff")
	if !strings.HasPrefix(raw, "---") {
		return newFrontMatter(), strings.TrimSpace(raw), nil
	}

	remainder := raw[3:]
	remainder = strings.TrimPrefix(remainder, "\r")
	remainder = strings.TrimPrefix(remainder, "\n")

	lines := strings.Split(remainder, "\n")
	boundary := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			boundary = i
			break
		}
	}
	if boundary == -1 {
		return newFrontMatter(), raw, errors.New("missing closing front matter delimiter")
	}

	metaLines := lines[:boundary]
	bodyLines := lines[boundary+1:]

	fm, err := parseFrontMatter(metaLines)
	if err != nil {
		return fm, "", err
	}

	body := strings.Join(bodyLines, "\n")
	return fm, strings.TrimSpace(body), nil
}

func parseFrontMatter(lines []string) (frontMatter, error) {
	fm := newFrontMatter()
	var currentListKey string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "- ") {
			if currentListKey == "" {
				return fm, errors.New("list item outside a key")
			}
			value := strings.TrimSpace(trimmed[2:])
			if value != "" {
				key := strings.ToLower(currentListKey)
				fm.lists[key] = append(fm.lists[key], value)
			}
			continue
		}

		currentListKey = ""
		if idx := strings.Index(trimmed, ":"); idx >= 0 {
			key := strings.ToLower(strings.TrimSpace(trimmed[:idx]))
			value := strings.TrimSpace(trimmed[idx+1:])
			if value == "" {
				currentListKey = key
				continue
			}
			fm.scalars[key] = value
		}
	}
	return fm, nil
}

func markdownToHTML(md string) (template.HTML, string, string) {
	lines := strings.Split(md, "\n")
	var builder strings.Builder
	var paragraph []string
	var firstParagraph string
	var title string
	inList := false
	const blockIndent = "      "

	flushParagraph := func() {
		if len(paragraph) == 0 {
			return
		}
		raw := strings.Join(paragraph, " ")
		if firstParagraph == "" {
			firstParagraph = stripInline(raw)
		}
		builder.WriteString(blockIndent)
		builder.WriteString("<p>")
		builder.WriteString(renderInline(raw))
		builder.WriteString("</p>\n")
		paragraph = nil
	}

	closeList := func() {
		if inList {
			builder.WriteString(blockIndent)
			builder.WriteString("</ul>\n")
			inList = false
		}
	}

	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			flushParagraph()
			closeList()
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			level := headingLevel(trimmed)
			if level > 0 {
				flushParagraph()
				closeList()
				text := strings.TrimSpace(trimmed[level:])
				if level == 1 && title == "" {
					title = stripInline(text)
					continue
				}
				builder.WriteString(fmt.Sprintf("%s<h%d>%s</h%d>\n", blockIndent, level, renderInline(text), level))
				continue
			}
		}

		if strings.HasPrefix(trimmed, "- ") {
			flushParagraph()
			if !inList {
				builder.WriteString(blockIndent)
				builder.WriteString("<ul>\n")
				inList = true
			}
			builder.WriteString(blockIndent)
			builder.WriteString("  <li>")
			builder.WriteString(renderInline(strings.TrimSpace(trimmed[2:])))
			builder.WriteString("</li>\n")
			continue
		}

		paragraph = append(paragraph, trimmed)
	}

	flushParagraph()
	closeList()

	return template.HTML(builder.String()), strings.TrimSpace(firstParagraph), strings.TrimSpace(title)
}

func headingLevel(line string) int {
	count := 0
	for count < len(line) && line[count] == '#' {
		count++
	}
	if count == 0 || count > 6 {
		return 0
	}
	if len(line) <= count || line[count] != ' ' {
		return 0
	}
	return count
}

func renderInline(input string) string {
	var b strings.Builder
	type marker struct {
		tag string
	}
	var stack []marker

	emit := func(s string) {
		b.WriteString(s)
	}

	for i := 0; i < len(input); {
		switch {
		case strings.HasPrefix(input[i:], "**"):
			if len(stack) > 0 && stack[len(stack)-1].tag == "strong" {
				emit("</strong>")
				stack = stack[:len(stack)-1]
			} else {
				stack = append(stack, marker{tag: "strong"})
				emit("<strong>")
			}
			i += 2
		case input[i] == '*' || input[i] == '_':
			if len(stack) > 0 && stack[len(stack)-1].tag == "em" {
				emit("</em>")
				stack = stack[:len(stack)-1]
			} else {
				stack = append(stack, marker{tag: "em"})
				emit("<em>")
			}
			i++
		case input[i] == '`':
			if len(stack) > 0 && stack[len(stack)-1].tag == "code" {
				emit("</code>")
				stack = stack[:len(stack)-1]
			} else {
				stack = append(stack, marker{tag: "code"})
				emit("<code>")
			}
			i++
		case input[i] == '[':
			endText := strings.IndexByte(input[i:], ']')
			if endText > 0 && i+endText+1 < len(input) && input[i+endText+1] == '(' {
				endURL := strings.IndexByte(input[i+endText+2:], ')')
				if endURL >= 0 {
					text := input[i+1 : i+endText]
					url := input[i+endText+2 : i+endText+2+endURL]
					emit(`<a href="`)
					emit(html.EscapeString(url))
					emit(`">`)
					emit(renderInline(text))
					emit("</a>")
					i += endText + 2 + endURL + 1
					continue
				}
			}
			fallthrough
		default:
			emit(escapeText(string(input[i])))
			i++
		}
	}

	for len(stack) > 0 {
		switch stack[len(stack)-1].tag {
		case "strong":
			emit("</strong>")
		case "em":
			emit("</em>")
		case "code":
			emit("</code>")
		}
		stack = stack[:len(stack)-1]
	}

	return b.String()
}

func stripInline(s string) string {
	replacer := strings.NewReplacer("**", "", "__", "", "*", "", "_", "", "`", "")
	return replacer.Replace(s)
}

func escapeText(s string) string {
	if !strings.ContainsAny(s, "&<>\"") {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&#34;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func formatAuthors(authors []string) string {
	clean := make([]string, 0, len(authors))
	for _, a := range authors {
		name := strings.TrimSpace(a)
		if name == "" {
			continue
		}
		clean = append(clean, tidyName(name))
	}
	authors = clean
	switch len(authors) {
	case 0:
		return ""
	case 1:
		return authors[0]
	case 2:
		return authors[0] + " & " + authors[1]
	default:
		last := authors[len(authors)-1]
		return strings.Join(authors[:len(authors)-1], ", ") + ", & " + last
	}
}

func makeExcerpt(s string, limit int) string {
	words := strings.Fields(s)
	if len(words) == 0 {
		return ""
	}
	text := strings.Join(words, " ")
	if len(text) <= limit {
		return text
	}
	cut := strings.LastIndex(text[:limit], " ")
	if cut == -1 {
		cut = limit
	}
	return strings.TrimSpace(text[:cut]) + "…"
}

func titleFromSlug(slug string) string {
	parts := strings.FieldsFunc(slug, func(r rune) bool {
		return r == '-' || r == '_' || r == ' ' || r == '/'
	})
	for i, part := range parts {
		if part == "" {
			continue
		}
		runes := []rune(part)
		runes[0] = unicode.ToUpper(runes[0])
		parts[i] = string(runes)
	}
	if len(parts) == 0 {
		return strings.ToUpper(slug)
	}
	return strings.Join(parts, " ")
}

func tidyName(name string) string {
	parts := strings.Fields(name)
	for i, part := range parts {
		runes := []rune(part)
		if len(runes) == 0 {
			continue
		}
		runes[0] = unicode.ToUpper(runes[0])
		for j := 1; j < len(runes); j++ {
			runes[j] = unicode.ToLower(runes[j])
		}
		parts[i] = string(runes)
	}
	return strings.Join(parts, " ")
}
