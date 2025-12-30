---
authors:
- mike
tags:
- ci
- llms
date: 2025-12-30
---

# LLMs in your CI can be awesome

Like most people, I'm not convinced that LLMs(/thE Ai) will be replacing software professionals. But, I do find that it's very difficult to know when LLM is a good fit for a task and when you should just do it manually. One place where it feels like a quick and easy, continuous, win is the CI pipeline. Even if that fucker makes some mistakes, it's still sort of "a pair of eyes" to look at your commit.

## Ollama, let's fucking go

To prove my point I've added a simple blog reviewer to my blog repository. It just uses Codex CLI in the pipeline and I've pointed it to Ollama Cloud (becuz free).

Technically, Codex is a coding agent and not a general purpose "agent CLI" for which you can overwrite the system prompt. It is a way to allow the LLM to use tools (mainly the gh CLI), and do the agent loop. It does inject a system prompt that starts like this:

```text
You are a coding agent running in the Codex CLI, a terminal-based coding assistant
```

I found Codex to work fine for the task although it's not exactly coding, but for many other "agents" you might use in your CI coding is probably useful!

Here's a simplified example of how you would do this in general:

```yaml
name: Codex PR blog reviewer

on:
  pull_request:
    types: [opened, synchronize, reopened]

# Strict permissions for the GitHub token (GITHUB_TOKEN)
permissions:
  contents: read
  pull-requests: write

jobs:
  codex_review:
    runs-on: ubuntu-latest

    steps:
      - name: Checkout PR merge commit
        uses: actions/checkout@v5
        with:
          fetch-depth: 1

      - name: Install Codex CLI
        run: npm i -g @openai/codex

      - name: Run Codex
        env:
          OLLAMA_API_KEY: ${{ secrets.CODEX_API_KEY }}

          PR_NUMBER: ${{ github.event.pull_request.number }}
          REPO: ${{ github.repository }}
          GH_TOKEN: ${{ github.token }}
        run: |
          set -euo pipefail

          export CODEX_HOME="$GITHUB_WORKSPACE/.codex-ci"
          mkdir -p "$CODEX_HOME"

          cat > "$CODEX_HOME/config.toml" <<'EOF'
          model ="gpt-oss:120b-cloud"
          model_provider = "ollamacloud"

          [model_providers.ollamacloud]
          name = "Ollama"
          base_url = "https://ollama.com/v1"
          env_key = "OLLAMA_API_KEY"
          EOF

          cat > prompt.txt <<'PROMPT'
          You are a blog post reviewer. Check the blog post for typos and other problems with the language.

          Blogs live under the ./blog folder, if there are no changes there then you don't have to do anything.
          
          You can use gh CLI to post comments `gh pr comment "$PR" --bode "<your comment>"`

          Rules:
          - Only comment on changed lines in the PR.
          - If you are not confident about an inline location, omit it.
          PROMPT

          codex exec --yolo "$(cat prompt.txt)"
```

I greatly simplified the prompt, I had to experiment a bit with it to get it to do what I wanted. However, I really recommend trying to keep it short, the shorter you can make your point the better the prompt (in my experience).

I think it's fun having an LLM buddy commenting on my blog posts, and it has given good feedback although it has a problem with swear words occasionally, weird.
