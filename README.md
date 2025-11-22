# Personal website

This is my personal website to host things, it's just a static page and it is written from scratch with basic HTML/CSS.

Currently hosted in GitHub pages, and probably will remain like that because more dynamic apps/sites would probably have a subdomain and actual server to run in.

## Principles

The website is built from scratch using plain HTML, CSS and JavaScript. No frameworks, no dependencies.

## Blog generator

Blog posts live under `blog/*.md` with a tiny front‑matter block:

```yaml
---
authors:
- mike
tags:
- cloud
date: 2025-08-23
---
```

Run the Go helper to turn every markdown file into a full HTML page and refresh `blog/index.html`:

```sh
go run ./cmd/blogtool
```

The CLI only uses the Go standard library. It parses the metadata, renders the markdown into the shared blog post layout, and rebuilds the listing page with the newest posts first. Pass `-blog some/other/path` if you ever move the blog directory.
