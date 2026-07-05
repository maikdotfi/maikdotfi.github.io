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
	"unicode/utf8"

	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
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

func parseFootnoteDef(trimmed string) (id, text string, ok bool) {
	if !strings.HasPrefix(trimmed, "[^") {
		return "", "", false
	}
	end := strings.Index(trimmed[2:], "]:")
	if end < 0 {
		return "", "", false
	}
	id = trimmed[2 : 2+end]
	text = strings.TrimSpace(trimmed[2+end+2:])
	return id, text, true
}

func markdownToHTML(md string) (template.HTML, string, string) {
	lines := strings.Split(md, "\n")
	var builder strings.Builder
	var paragraph []string
	var firstParagraph string
	var title string
	listType := "" // "", "ul", or "ol"
	inBlockquote := false
	var blockquoteLines []string
	inCodeBlock := false
	var codeLines []string
	var codeLang string
	codeFence := 0
	slugCounts := map[string]int{}
	const blockIndent = "      "

	// Pre-collect footnote definitions in order of appearance.
	footnotes := map[string]string{}
	var footnoteOrder []string
	for _, line := range lines {
		if id, text, ok := parseFootnoteDef(strings.TrimSpace(line)); ok {
			if _, exists := footnotes[id]; !exists {
				footnoteOrder = append(footnoteOrder, id)
			}
			footnotes[id] = text
		}
	}

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

	flushCodeBlock := func() {
		if !inCodeBlock {
			return
		}
		builder.WriteString(blockIndent)
		builder.WriteString("<pre><code")
		if codeLang != "" {
			builder.WriteString(` class="language-`)
			builder.WriteString(html.EscapeString(codeLang))
			builder.WriteString(`"`)
		}
		builder.WriteString(">")
		builder.WriteString(highlightCode(strings.Join(codeLines, "\n"), codeLang))
		builder.WriteString("</code></pre>\n")
		inCodeBlock = false
		codeLines = nil
		codeLang = ""
		codeFence = 0
	}

	closeList := func() {
		if listType != "" {
			builder.WriteString(blockIndent)
			builder.WriteString("</")
			builder.WriteString(listType)
			builder.WriteString(">\n")
			listType = ""
		}
	}

	writeListItem := func(kind, content string) {
		if listType != kind {
			closeList()
			builder.WriteString(blockIndent)
			builder.WriteString("<")
			builder.WriteString(kind)
			builder.WriteString(">\n")
			listType = kind
		}
		builder.WriteString(blockIndent)
		builder.WriteString("  <li>")
		builder.WriteString(renderInline(content))
		builder.WriteString("</li>\n")
	}

	flushBlockquote := func() {
		if !inBlockquote {
			return
		}
		raw := strings.Join(blockquoteLines, " ")
		builder.WriteString(blockIndent)
		builder.WriteString("<blockquote><p>")
		builder.WriteString(renderInline(raw))
		builder.WriteString("</p></blockquote>\n")
		inBlockquote = false
		blockquoteLines = nil
	}

	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		trimmed := strings.TrimSpace(line)

		if inCodeBlock {
			// A closing fence is a bare run of backticks (no info string)
			// at least as long as the opening fence. Anything else — including
			// shorter or info-bearing fences — is treated as block content,
			// which lets us nest fenced blocks (outer fence uses more backticks).
			if length, rest, ok := fenceInfo(trimmed); ok && rest == "" && length >= codeFence {
				flushCodeBlock()
			} else {
				codeLines = append(codeLines, line)
			}
			continue
		}

		if length, rest, ok := fenceInfo(trimmed); ok {
			flushParagraph()
			flushBlockquote()
			closeList()
			inCodeBlock = true
			codeFence = length
			codeLang = rest
			continue
		}

		if trimmed == "" {
			flushParagraph()
			flushBlockquote()
			closeList()
			continue
		}

		if strings.HasPrefix(trimmed, "#") {
			level := headingLevel(trimmed)
			if level > 0 {
				flushParagraph()
				flushBlockquote()
				closeList()
				text := strings.TrimSpace(trimmed[level:])
				if level == 1 && title == "" {
					title = stripInline(text)
					continue
				}
				slug := uniqueSlug(stripInline(text), slugCounts)
				builder.WriteString(fmt.Sprintf(
					"%s<h%d id=%q>%s<a class=\"heading-anchor\" href=\"#%s\" aria-label=\"Link to this section\">#</a></h%d>\n",
					blockIndent, level, slug, renderInline(text), slug, level))
				continue
			}
		}

		if strings.HasPrefix(trimmed, "> ") || trimmed == ">" {
			flushParagraph()
			closeList()
			inBlockquote = true
			blockquoteLines = append(blockquoteLines, strings.TrimSpace(strings.TrimPrefix(trimmed, ">")))
			continue
		}

		if strings.HasPrefix(trimmed, "- ") {
			flushParagraph()
			flushBlockquote()
			writeListItem("ul", strings.TrimSpace(trimmed[2:]))
			continue
		}

		if content, ok := orderedListItem(trimmed); ok {
			flushParagraph()
			flushBlockquote()
			writeListItem("ol", content)
			continue
		}

		if _, _, ok := parseFootnoteDef(trimmed); ok {
			flushParagraph()
			flushBlockquote()
			continue
		}

		flushBlockquote()
		paragraph = append(paragraph, trimmed)
	}

	flushParagraph()
	flushBlockquote()
	closeList()
	flushCodeBlock()

	if len(footnoteOrder) > 0 {
		builder.WriteString(blockIndent + "<section class=\"footnotes\">\n")
		builder.WriteString(blockIndent + "  <ol>\n")
		for _, id := range footnoteOrder {
			builder.WriteString(blockIndent + "    <li id=\"fn-" + html.EscapeString(id) + "\">")
			builder.WriteString(renderInline(footnotes[id]))
			builder.WriteString("</li>\n")
		}
		builder.WriteString(blockIndent + "  </ol>\n")
		builder.WriteString(blockIndent + "</section>\n")
	}

	return template.HTML(builder.String()), strings.TrimSpace(firstParagraph), strings.TrimSpace(title)
}

// highlightCode tokenises a fenced code block with Chroma and emits token
// <span>s carrying our own semantic classes (see the .hl-* rules in the
// template). Colours live in CSS variables so highlighting follows the
// light/dark theme. Unknown languages (or the empty language) fall back to
// plainly-escaped text, so nothing is ever lost.
func highlightCode(code, lang string) string {
	lexer := lexers.Get(lang)
	if lexer == nil {
		return escapeText(code)
	}
	iterator, err := chroma.Coalesce(lexer).Tokenise(nil, code)
	if err != nil {
		return escapeText(code)
	}

	var b strings.Builder
	for _, tok := range iterator.Tokens() {
		text := escapeText(tok.Value)
		class := tokenClass(tok.Type)
		if class == "" {
			b.WriteString(text)
			continue
		}
		b.WriteString(`<span class="`)
		b.WriteString(class)
		b.WriteString(`">`)
		b.WriteString(text)
		b.WriteString(`</span>`)
	}
	return b.String()
}

// tokenClass maps a Chroma token type onto one of our restrained set of
// highlight classes. Anything unmapped (plain names, operators, punctuation,
// whitespace) renders in the default foreground colour.
func tokenClass(tt chroma.TokenType) string {
	switch {
	case tt.InCategory(chroma.Comment):
		return "hl-com"
	case tt.InSubCategory(chroma.String):
		return "hl-str"
	case tt.InSubCategory(chroma.Number):
		return "hl-num"
	case tt.InCategory(chroma.Keyword):
		return "hl-kw"
	case tt == chroma.NameTag:
		return "hl-kw"
	case tt == chroma.NameFunction || tt == chroma.NameFunctionMagic || tt == chroma.NameClass:
		return "hl-fn"
	case tt == chroma.NameAttribute || tt == chroma.NameDecorator:
		return "hl-fn"
	case tt == chroma.NameBuiltin || tt == chroma.NameBuiltinPseudo:
		return "hl-builtin"
	}
	return ""
}

// fenceInfo reports whether trimmed begins a code fence (a run of 3 or more
// backticks). It returns the fence length and the trailing info string (e.g. the
// language). For a closing fence the info string is empty.
func fenceInfo(trimmed string) (length int, rest string, ok bool) {
	n := 0
	for n < len(trimmed) && trimmed[n] == '`' {
		n++
	}
	if n < 3 {
		return 0, "", false
	}
	return n, strings.TrimSpace(trimmed[n:]), true
}

// orderedListItem reports whether trimmed is an ordered list item ("1. text",
// "2. text", …) and returns the item content. The actual number is ignored —
// the browser renumbers <ol> items — so "1." on every line is fine.
func orderedListItem(trimmed string) (content string, ok bool) {
	n := 0
	for n < len(trimmed) && trimmed[n] >= '0' && trimmed[n] <= '9' {
		n++
	}
	if n == 0 || n+1 >= len(trimmed) || trimmed[n] != '.' || trimmed[n+1] != ' ' {
		return "", false
	}
	return strings.TrimSpace(trimmed[n+1:]), true
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
		case input[i] == '!' && i+1 < len(input) && input[i+1] == '[':
			// Image: ![alt](url) or ![alt|size](url)
			endAlt := strings.IndexByte(input[i+2:], ']')
			if endAlt >= 0 && i+2+endAlt+1 < len(input) && input[i+2+endAlt+1] == '(' {
				endURL := strings.IndexByte(input[i+2+endAlt+2:], ')')
				if endURL >= 0 {
					altRaw := input[i+2 : i+2+endAlt]
					url := input[i+2+endAlt+2 : i+2+endAlt+2+endURL]
					alt := altRaw
					var sizeClass string
					if pipe := strings.LastIndex(altRaw, "|"); pipe >= 0 {
						alt = altRaw[:pipe]
						sizeClass = strings.TrimSpace(altRaw[pipe+1:])
					}
					emit(`<img src="`)
					emit(html.EscapeString(url))
					emit(`" alt="`)
					emit(html.EscapeString(alt))
					emit(`"`)
					if sizeClass != "" {
						emit(` class="img-`)
						emit(html.EscapeString(sizeClass))
						emit(`"`)
					}
					emit(`>`)
					i += 2 + endAlt + 2 + endURL + 1
					continue
				}
			}
			fallthrough
		case input[i] == '[':
			// Footnote reference [^id]
			if i+1 < len(input) && input[i+1] == '^' {
				if end := strings.IndexByte(input[i+2:], ']'); end >= 0 {
					id := input[i+2 : i+2+end]
					emit(`<sup><a href="#fn-`)
					emit(html.EscapeString(id))
					emit(`">`)
					emit(escapeText(id))
					emit(`</a></sup>`)
					i += 2 + end + 1
					continue
				}
			}
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
			_, size := utf8.DecodeRuneInString(input[i:])
			if size < 1 {
				size = 1
			}
			emit(escapeText(input[i : i+size]))
			i += size
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
	// Strip markdown images ![alt](url) -> (removed entirely)
	for {
		start := strings.Index(s, "![")
		if start == -1 {
			break
		}
		end := strings.IndexByte(s[start+2:], ']')
		if end < 0 {
			break
		}
		end += start + 2
		if end+1 < len(s) && s[end+1] == '(' {
			urlEnd := strings.IndexByte(s[end+2:], ')')
			if urlEnd >= 0 {
				s = strings.TrimSpace(s[:start] + s[end+2+urlEnd+1:])
				continue
			}
		}
		break
	}
	// Strip markdown links [text](url) -> text
	for {
		start := strings.IndexByte(s, '[')
		if start == -1 {
			break
		}
		end := strings.IndexByte(s[start:], ']')
		if end < 0 {
			break
		}
		end += start
		if end+1 < len(s) && s[end+1] == '(' {
			urlEnd := strings.IndexByte(s[end+2:], ')')
			if urlEnd >= 0 {
				text := s[start+1 : end]
				s = s[:start] + text + s[end+2+urlEnd+1:]
				continue
			}
		}
		break
	}
	replacer := strings.NewReplacer("**", "", "__", "", "*", "", "_", "", "`", "")
	return replacer.Replace(s)
}

// escapeText escapes a string for use in HTML text content. Only &, < and >
// are special there; quotes are left as-is (they only need escaping inside
// attribute values — see html.EscapeString for that).
func escapeText(s string) string {
	if !strings.ContainsAny(s, "&<>") {
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

// slugify turns heading text into a URL-fragment-safe id: lowercased, with
// runs of non-alphanumeric characters collapsed to single hyphens.
func slugify(s string) string {
	var b strings.Builder
	lastHyphen := true // leading hyphens are suppressed
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastHyphen = false
		case r == '\'', r == '’':
			// Drop apostrophes so "let's" slugs to "lets", not "let-s".
		default:
			if !lastHyphen {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// uniqueSlug slugifies text and disambiguates collisions within a single
// document by suffixing -2, -3, … so every heading id is unique.
func uniqueSlug(text string, counts map[string]int) string {
	base := slugify(text)
	if base == "" {
		base = "section"
	}
	slug := base
	for counts[slug] > 0 {
		counts[base]++
		slug = fmt.Sprintf("%s-%d", base, counts[base])
	}
	counts[slug]++ // reserve the chosen slug so nothing else can take it
	return slug
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
