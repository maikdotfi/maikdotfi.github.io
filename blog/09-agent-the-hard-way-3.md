---
authors:
- mike
tags:
- hard way
- agentic coding
- llms
date: 2026-07-14
---

# Agent the Hard Way: Everything is a Tool

Give LLMs tools and they make stuff happen.

## Tangent

Before I get into why everything is a tool (me included):

I wanted to split the Agent the Hard Way "project" into two repos:

1. [agent-the-hard-way](https://github.com/maikdotfi/agent-the-hard-way)
1. [metaharness](https://github.com/maikdotfi/metaharness)

The `metaharness` repo is a library and `agent-the-hard-way` contains concrete agent implementations that are composed of the meta harness pieces. This makes also sense because I don't want to limit the things I put into `agent-the-hard-way` repo to just my own stuff, I might very well try some pre-baked stuff as well there. I just don't know what yet. The repo itself has no writing right now, maybe one day it will or maybe I should just link to my blog. Remains to be seen.

## Everything is a Tool

Alright, so everything is a tool. What do I mean? Well in order for an LLM agent to do things it needs only tools:

1. read and write files -> tools
1. read a skill -> tool
1. interact with MCP server? Just another tool

To simplify it even further, a conversation with an LLM just contains text and tool calls (and responses to tool calls). That's why everything we add on top of the API is either text or a tool that the LLM can choose to call. Basically the conversation with an LLM looks like this abstractly:

```text
<system prompt>
<user message>
<tool definitions>
<thinking>
<tool call>
<tool result>
...
<assistant message>
```

Everything interesting happens via tool calls. The idea of tools is kinda simple, but some seem to make a big deal out of MCP etc. It's just tool calls. Let's look at some tools next next.

## What the Tools

Pi has 4 built-in tools:

- read
- write
- edit
- bash

Claude Code has lots more tools built-in and then more for which the tool definition is not loaded into context, but you access via `ToolSearch` tool of course:

- Read
- Write
- Edit
- Bash
- Agent
- AskUserQuestion
- ScheduleWakeup
- ShareOnboardingGuide
- Skill
- ToolSearch
- Workflow

Here are the deferred tools that are included only by name, if you are curious:

- CronCreate
- CronDelete
- CronList
- DesignSync
- EnterPlanMode
- EnterWorktree
- ExitPlanMode
- ExitWorktree
- Monitor
- NotebookEdit
- PushNotification
- RemoteTrigger
- TaskCreate
- TaskGet
- TaskList
- TaskOutput
- TaskStop
- TaskUpdate
- WebFetch
- WebSearch

Man, lots of tools. And this list will surely be longer soon.

Actually I forgot to mention some more tools:

- mcp__claude_ai_Asana__authenticate
- mcp__claude_ai_Asana__complete_authentication
- mcp__claude_ai_Atlassian__authenticate
- mcp__claude_ai_Atlassian__complete_authentication
- mcp__claude_ai_Box__authenticate
- mcp__claude_ai_Box__complete_authentication
- mcp__claude_ai_Canva__authenticate
- mcp__claude_ai_Canva__complete_authentication
- mcp__claude_ai_EULER__authenticate
- mcp__claude_ai_EULER__complete_authentication
- mcp__claude_ai_Figma__authenticate
- mcp__claude_ai_Figma__complete_authentication
- mcp__claude_ai_Gamma__authenticate
- mcp__claude_ai_Gamma__complete_authentication
- mcp__claude_ai_HubSpot__authenticate
- mcp__claude_ai_HubSpot__complete_authentication
- mcp__claude_ai_Intercom__authenticate
- mcp__claude_ai_Intercom__complete_authentication
- mcp__claude_ai_Linear__authenticate
- mcp__claude_ai_Linear__complete_authentication
- mcp__claude_ai_Notion__authenticate
- mcp__claude_ai_Notion__complete_authentication
- mcp__claude_ai_monday_com__authenticate
- mcp__claude_ai_monday_com__complete_authentication

Those are some sort of MCP servers you can opt-into, so the auth tools are there by default(?). If you have some MCP servers of your own there are, you guessed it, more tools:

- mcp__lightpanda__click
- mcp__lightpanda__detectForms
- mcp__lightpanda__eval
- mcp__lightpanda__evaluate
- mcp__lightpanda__fill
- mcp__lightpanda__findElement
- mcp__lightpanda__goto
- mcp__lightpanda__hover
- mcp__lightpanda__interactiveElements
- mcp__lightpanda__links
- mcp__lightpanda__markdown
- mcp__lightpanda__navigate
- mcp__lightpanda__nodeDetails
- mcp__lightpanda__press
- mcp__lightpanda__scroll
- mcp__lightpanda__selectOption
- mcp__lightpanda__semantic_tree
- mcp__lightpanda__setChecked
- mcp__lightpanda__structuredData
- mcp__lightpanda__waitForSelector

That's an example when I had [lightpanda](https://lightpanda.io/) loaded as MCP server.

## Skills as a Tool

Skills don't actually have to be implemented as a tool, but it makes sense to me you'd have a skill to read the full `SKILL.md`. That means you don't necessarily have to have the skill files even on a filesystem, but not having them on a filesystem can feel weird if the agent has access to the shell anyways. It depends.

Claude Code has a tool for skills which you might have noticed above, pi does not, skills are just listed in the system prompt and the agent can choose on it's own to read the `SKILL.md` if it wants. Both approaches are fine, but to me it feels better actually to implement the skills via a tool. That way it will be easy to see if a skill was called, that sounds like a good thing? Honestly


## Conclusion

When I saw how many tools Claude Code has and knowing Pi has so few I felt like writing this because it's nuts. I'll soon publish another post going into implementation of tools.



