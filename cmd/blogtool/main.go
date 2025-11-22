package main

import (
	"bytes"
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

var postTpl = template.Must(template.New("post").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} | maik.fi</title>
  <link rel="icon" href="../favicon.ico">
  <style>
    :root {
      color-scheme: light dark;
      --bg: #f6f7fb;
      --fg: #0f141e;
      --accent: #1c64f2;
      --muted: #6b7280;
      --border: color-mix(in srgb, var(--muted) 25%, transparent);
      font-family: "Inter", "Segoe UI", -apple-system, BlinkMacSystemFont, sans-serif;
    }

    @media (prefers-color-scheme: dark) {
      :root {
        --bg: #0f141e;
        --fg: #f8fafc;
        --muted: #94a3b8;
        --border: color-mix(in srgb, var(--muted) 40%, transparent);
      }
    }

    body {
      margin: 0;
      min-height: 100vh;
      background: var(--bg);
      color: var(--fg);
    }

    .wrap {
      max-width: 720px;
      margin: 0 auto;
      padding: 3rem 1.5rem 4rem;
    }

    nav a {
      color: inherit;
      text-decoration: none;
      font-weight: 600;
    }

    nav a:hover,
    nav a:focus {
      color: var(--accent);
      text-decoration: underline;
    }

    article {
      margin-top: 2.5rem;
    }

    h1 {
      font-size: clamp(2.3rem, 4.5vw, 3.2rem);
      margin: 0 0 0.5rem;
      letter-spacing: -0.035em;
    }

    .meta {
      margin: 0;
      color: var(--muted);
      font-size: 0.95rem;
    }

    .tags {
      display: inline-flex;
      gap: 0.35rem;
      flex-wrap: wrap;
      margin-top: 0.65rem;
    }

    .tag {
      background: color-mix(in srgb, var(--accent) 15%, transparent);
      color: var(--accent);
      border-radius: 999px;
      padding: 0.15rem 0.65rem;
      font-size: 0.85rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      font-weight: 600;
    }

    article p {
      line-height: 1.7;
      font-size: 1.05rem;
      margin: 1.3rem 0;
    }
  </style>
</head>
<body>
  <div class="wrap">
    <nav>
      <a href="index.html">← All posts</a> · <a href="../index.html">maik.fi home</a>
    </nav>
    <article>
      <header>
        <h1>{{.Title}}</h1>
        <p class="meta">
          <time datetime="{{.DateISO}}">{{.DateHuman}}</time>{{if .AuthorLine}} · by {{.AuthorLine}}{{end}}
        </p>
        {{- if .Tags}}
        <div class="tags">
          {{- range .Tags }}
          <span class="tag">{{.}}</span>
          {{- end }}
        </div>
        {{- end}}
      </header>
{{.Content}}
    </article>
  </div>
</body>
</html>
`))

var indexTpl = template.Must(template.New("index").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Blog | maik.fi</title>
  <link rel="icon" href="../favicon.ico">
  <style>
    :root {
      color-scheme: light dark;
      --bg: #f6f7fb;
      --fg: #0f141e;
      --accent: #1c64f2;
      --muted: #6b7280;
      font-family: "Inter", "Segoe UI", -apple-system, BlinkMacSystemFont, sans-serif;
    }

    @media (prefers-color-scheme: dark) {
      :root {
        --bg: #0f141e;
        --fg: #f8fafc;
        --muted: #94a3b8;
      }
    }

    :root[data-theme="light"] {
      --bg: #f6f7fb;
      --fg: #0f141e;
      --muted: #6b7280;
      color-scheme: light;
    }

    :root[data-theme="dark"] {
      --bg: #0f141e;
      --fg: #f8fafc;
      --muted: #94a3b8;
      color-scheme: dark;
    }

    body {
      margin: 0;
      min-height: 100vh;
      background: var(--bg);
      color: var(--fg);
    }

    .wrap {
      max-width: 720px;
      margin: 0 auto;
      padding: 3rem 1.5rem 4rem;
    }

    header {
      margin-bottom: 3rem;
      position: relative;
      padding-right: 3.5rem;
    }

    header a {
      color: inherit;
      text-decoration: none;
      font-weight: 600;
    }

    header a:hover,
    header a:focus {
      color: var(--accent);
      text-decoration: underline;
    }

    .theme-toggle {
      position: absolute;
      top: 0;
      right: 0;
      display: inline-flex;
      align-items: center;
      justify-content: center;
      width: 2.75rem;
      height: 2.75rem;
      border-radius: 999px;
      border: 1px solid color-mix(in srgb, var(--muted) 35%, transparent);
      background: none;
      color: inherit;
      cursor: pointer;
      transition: background 160ms ease, border-color 160ms ease, color 160ms ease;
    }

    .theme-toggle svg {
      width: 1.4rem;
      height: 1.4rem;
    }

    .theme-toggle:hover,
    .theme-toggle:focus-visible {
      background: color-mix(in srgb, var(--bg) 70%, var(--accent) 30%);
      border-color: color-mix(in srgb, var(--accent) 35%, transparent);
      color: var(--accent);
    }

    .theme-toggle:focus-visible {
      outline: 2px solid var(--accent);
      outline-offset: 2px;
    }

    h1 {
      font-size: clamp(2rem, 4vw, 3rem);
      margin: 0 0 0.5rem;
      letter-spacing: -0.03em;
    }

    p.lede {
      margin: 0;
      max-width: 32rem;
      color: var(--muted);
    }

    article {
      padding: 1.75rem 1.5rem;
      border: 1px solid color-mix(in srgb, var(--muted) 20%, transparent);
      border-radius: 18px;
      background: color-mix(in srgb, var(--bg) 85%, white 15%);
      margin-bottom: 1.5rem;
      transition: transform 160ms ease, box-shadow 160ms ease;
    }

    article:hover,
    article:focus-within {
      transform: translateY(-4px);
      box-shadow: 0 18px 36px -18px rgba(17, 24, 39, 0.35);
    }

    article h2 {
      margin: 0 0 0.35rem;
      font-size: 1.65rem;
      letter-spacing: -0.015em;
    }

    article h2 a {
      color: inherit;
      text-decoration: none;
    }

    article h2 a:hover,
    article h2 a:focus {
      color: var(--accent);
      text-decoration: underline;
    }

    .meta {
      font-size: 0.95rem;
      color: var(--muted);
      margin-bottom: 1rem;
    }

    .tags {
      display: inline-flex;
      gap: 0.35rem;
      flex-wrap: wrap;
    }

    .tag {
      background: color-mix(in srgb, var(--accent) 15%, transparent);
      color: var(--accent);
      border-radius: 999px;
      padding: 0.15rem 0.65rem;
      font-size: 0.85rem;
      text-transform: uppercase;
      letter-spacing: 0.08em;
      font-weight: 600;
    }

    .excerpt {
      margin: 0 0 1.25rem;
      line-height: 1.6;
    }

    .read-more {
      display: inline-flex;
      align-items: center;
      gap: 0.4rem;
      font-weight: 600;
      color: var(--accent);
      text-decoration: none;
    }

    .read-more svg {
      width: 18px;
      height: 18px;
    }
  </style>
</head>
<body>
  <div class="wrap">
    <header>
      <a href="../index.html">← Back to maik.fi</a>
      <h1>Blog</h1>
      <p class="lede">Occasional dispatches from the trenches. Expect rants, drunken mumblings, and opinions strong enough to give you a hangover.</p>
      <button type="button" class="theme-toggle" aria-pressed="false" aria-label="Toggle theme" title="Toggle theme">
        <svg viewBox="0 0 256 256" aria-hidden="true">
          <rect width="256" height="256" fill="none"/>
          <polygon points="64 40 192 40 240 152 16 152 64 40" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" stroke-width="16"/>
          <line x1="128" y1="152" x2="128" y2="216" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" stroke-width="16"/>
          <line x1="96" y1="216" x2="160" y2="216" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" stroke-width="16"/>
          <line x1="200" y1="152" x2="200" y2="192" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round" stroke-width="16"/>
        </svg>
      </button>
    </header>

    {{- range .Posts}}
    <article>
      <h2><a href="{{.Href}}">{{.Title}}</a></h2>
      <div class="meta">
        <time datetime="{{.DateISO}}">{{.DateHuman}}</time>{{if .AuthorLine}} · by {{.AuthorLine}}{{end}}{{if .Tags}} ·
        <span class="tags">
          {{- range .Tags }}
          <span class="tag">{{.}}</span>
          {{- end }}
        </span>{{end}}
      </div>
      {{if .Excerpt}}<p class="excerpt">{{.Excerpt}}</p>{{end}}
      <a class="read-more" href="{{.Href}}">
        Read the rant
        <svg viewBox="0 0 24 24" aria-hidden="true" focusable="false">
          <path fill="currentColor" d="M13.172 12 8.586 7.414 10 6l6 6-6 6-1.414-1.414z"/>
        </svg>
      </a>
    </article>
    {{- end}}
  </div>
  <script>
    (function () {
      const root = document.documentElement;
      const toggle = document.querySelector('.theme-toggle');
      if (!toggle) return;

      const storageKey = 'maikfi-theme';
      const mediaQuery = window.matchMedia('(prefers-color-scheme: dark)');

      const applyTheme = (theme) => {
        if (theme === 'light' || theme === 'dark') {
          root.setAttribute('data-theme', theme);
        } else {
          root.removeAttribute('data-theme');
        }

        const effective = root.getAttribute('data-theme') || (mediaQuery.matches ? 'dark' : 'light');
        const label = effective === 'dark' ? 'Switch to light mode' : 'Switch to dark mode';
        toggle.setAttribute('aria-pressed', effective === 'dark');
        toggle.setAttribute('aria-label', label);
        toggle.setAttribute('title', label);
      };

      const stored = localStorage.getItem(storageKey);
      const initial = stored === 'light' || stored === 'dark' ? stored : null;
      if (stored && !initial) {
        localStorage.removeItem(storageKey);
      }
      applyTheme(initial);

      toggle.addEventListener('click', () => {
        const current = root.getAttribute('data-theme') || (mediaQuery.matches ? 'dark' : 'light');
        const next = current === 'dark' ? 'light' : 'dark';
        localStorage.setItem(storageKey, next);
        applyTheme(next);
      });

      mediaQuery.addEventListener('change', (event) => {
        if (!localStorage.getItem(storageKey)) {
          applyTheme(event.matches ? 'dark' : 'light');
        }
      });
    }());
  </script>
</body>
</html>
`))
