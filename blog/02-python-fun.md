---
authors:
- mike
tags:
- python
- fun
date: 2025-11-22
---

# Python is great although it sucks

I've been writing Python again and to my surprise, I've been enjoying it a lot! For the most part.

What I really enjoy in Python is the actual writing and how concise, yet expressive, the language can be at the same time. Of course this does depend on how you write it, and one of the weaknesses I've seen with Python is that people write really shit code in Python. That's not Python's fault directly, but Python sure does allow/encourage some stuff I think should be straight up illegal, like awful one-liner syntax that some people consider _elegant_. Fuck me.

However, my main issue with Python is that it sucks ass the second you want to actually run it someplace else than your laptop. Coming back to Python from working mostly with Go, it's easy to see the appeal in the static binaries and cross-compilation that Go has. (of course Go isn't perfect either).

## B-b-but uv!

I know somebody reading this already thought to themselves "man, uv solves that, stop bitching". Yeaah, I mean it helps. If you are in an environment of heterogeneous hardware, no network limitations and fast connectivity too, then `uv` probably does fix Python packaging and runtime challenges quite neatly. I agree. But when you are dealing with different architectures (x86, arm64) and with different operating systems (Linux, macOS, even fucking Windows) AND you cannot just download bunch of shit from the internet... Python sucks.

## What does not suck

All things have pros and cons, so all things suck, at least a bit. But Python can be pretty fun, and I had some fun writing it after a long break of not writing more than a few lines here and there for years.
