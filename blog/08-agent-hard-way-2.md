---
authors:
- mike
tags:
- hard way
- agentic coding
- llms
date: 2026-07-11
---

# Agent The Hard Way: Fantasy

Alright so in the last post we were rawdogging the Anthropic API, no SDK and no way to use OpenAI GPT models nor Google Gemini models. But we're going to change that.

The thing is though, I am not in the mood to start writing bunch of code to deal with all these different providers and more-or-less _compatible_ APIs in addition to the official APIs. So instead I went hunting for a library that handles this business for me. I was already aware that there is a coding agent written in Go by the lovely [charm](https://charm.land/) folks that have built extremely cool and useful TUI libraries (and more!). Their CLI agent is called [crush](https://github.com/charmbracelet/crush) and it's actually fairly popular one although sadly I've not been using it (planning to change that).

I was super happy to find that `crush` has a lovely dependency called [fantasy](https://github.com/charmbracelet/fantasy) which the `crush` CLI builds upon. Sounds fantastic, eh?

## Fantasy Agents

Looking at the example in the README fantasy library is primarily positioned as higher level library than I'd hoped:

```go
import "charm.land/fantasy"
import "charm.land/fantasy/providers/openrouter"

// Choose your fave provider.
provider, err := openrouter.New(openrouter.WithAPIKey(myHotKey))
if err != nil {
	fmt.Fprintln(os.Stderr, "Whoops:", err)
	os.Exit(1)
}

ctx := context.Background()

// Pick your fave model.
model, err := provider.LanguageModel(ctx, "moonshotai/kimi-k2")
if err != nil {
	fmt.Fprintln(os.Stderr, "Dang:", err)
	os.Exit(1)
}

// Make your own tools.
cuteDogTool := fantasy.NewAgentTool(
  "cute_dog_tool",
  "Provide up-to-date info on cute dogs.",
  fetchCuteDogInfoFunc,
)

// Equip your agent.
agent := fantasy.NewAgent(
  model,
  fantasy.WithSystemPrompt("You are a moderately helpful, dog-centric assistant."),
  fantasy.WithTools(cuteDogTool),
)

// Put that agent to work!
const prompt = "Find all the cute dogs in Silver Lake, Los Angeles."
result, err := agent.Generate(ctx, fantasy.AgentCall{Prompt: prompt})
if err != nil {
    fmt.Fprintln(os.Stderr, "Oof:", err)
    os.Exit(1)
}
fmt.Println(result.Response.Content.Text())
```

Using this `Agent` type would not work for me because I want to have more control over the tools, I want to handle executing them. However, large amount of the fantasy package is made public so I thought it's still worth trying to use it, just at a lower level.

## Rewrite in Fantasy

So I had Claude attempt a rewrite the Anthropic speficic model interactions with fantasy. It came out pretty good:

```go
package main

import (
	"context"
	"fmt"

	"charm.land/fantasy"
	"charm.land/fantasy/providers/anthropic"
	"charm.land/fantasy/providers/google"
	"charm.land/fantasy/providers/openai"
)

const defaultModel string = "gemma4:31b-cloud"

type ModelConfig struct {
	Provider string // "anthropic" (default), "openai", "google"
	Model    string
	BaseURL  string // optional endpoint override
	APIKey   string
	// Headers adds extra HTTP headers to every request. Needed for endpoints
	// that don't accept the provider's native auth scheme — e.g. Ollama Cloud
	// speaks the Anthropic API but wants "Authorization: Bearer <key>" instead
	// of the X-Api-Key header that WithAPIKey sets.
	Headers map[string]string
}

func NewModel(ctx context.Context, cfg ModelConfig) (fantasy.LanguageModel, error) {
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}

	var (
		provider fantasy.Provider
		err      error
	)
	switch cfg.Provider {
	case "", "anthropic":
		opts := []anthropic.Option{}
		if cfg.APIKey != "" {
			opts = append(opts, anthropic.WithAPIKey(cfg.APIKey))
		}
		if cfg.BaseURL != "" {
			opts = append(opts, anthropic.WithBaseURL(cfg.BaseURL))
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, anthropic.WithHeaders(cfg.Headers))
		}
		provider, err = anthropic.New(opts...)
	case "openai":
		opts := []openai.Option{}
		if cfg.APIKey != "" {
			opts = append(opts, openai.WithAPIKey(cfg.APIKey))
		}
		if cfg.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(cfg.BaseURL))
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, openai.WithHeaders(cfg.Headers))
		}
		provider, err = openai.New(opts...)
	case "google":
		opts := []google.Option{}
		if cfg.APIKey != "" {
			opts = append(opts, google.WithGeminiAPIKey(cfg.APIKey))
		}
		if cfg.BaseURL != "" {
			opts = append(opts, google.WithBaseURL(cfg.BaseURL))
		}
		if len(cfg.Headers) > 0 {
			opts = append(opts, google.WithHeaders(cfg.Headers))
		}
		provider, err = google.New(opts...)
	default:
		return nil, fmt.Errorf("unknown provider %q", cfg.Provider)
	}
	if err != nil {
		return nil, err
	}
	return provider.LanguageModel(ctx, cfg.Model)
}

// AssistantMessage turns a model response into an assistant turn ready to append
// to the session and feed back in.
func AssistantMessage(resp *fantasy.Response) fantasy.Message {
	var parts []fantasy.MessagePart
	for _, c := range resp.Content {
		switch c.GetType() {
		case fantasy.ContentTypeText:
			if t, ok := fantasy.AsContentType[fantasy.TextContent](c); ok {
				parts = append(parts, fantasy.TextPart{
					Text:            t.Text,
					ProviderOptions: fantasy.ProviderOptions(t.ProviderMetadata),
				})
			}
		case fantasy.ContentTypeReasoning:
			if r, ok := fantasy.AsContentType[fantasy.ReasoningContent](c); ok {
				parts = append(parts, fantasy.ReasoningPart{
					Text:            r.Text,
					ProviderOptions: fantasy.ProviderOptions(r.ProviderMetadata),
				})
			}
		case fantasy.ContentTypeToolCall:
			if tc, ok := fantasy.AsContentType[fantasy.ToolCallContent](c); ok {
				parts = append(parts, fantasy.ToolCallPart{
					ToolCallID:      tc.ToolCallID,
					ToolName:        tc.ToolName,
					Input:           tc.Input,
					ProviderOptions: fantasy.ProviderOptions(tc.ProviderMetadata),
				})
			}
		}
	}
	return fantasy.Message{Role: fantasy.MessageRoleAssistant, Content: parts}
}

// ToolResultMessage wraps the outputs of the tools we ran into a single tool
// turn. Each result is keyed by the tool_call_id it answers.
func ToolResultMessage(parts ...fantasy.ToolResultPart) fantasy.Message {
	content := make([]fantasy.MessagePart, len(parts))
	for i, p := range parts {
		content[i] = p
	}
	return fantasy.Message{Role: fantasy.MessageRoleTool, Content: content}
}
```

We now have bunch of bullshit config parsing going on in the `NewModel` function that returns a `fantasy.LanguageModel` interface which replaces the `AnthropicClient` we had previously. But now you can create a LanguageModel using three different provider implementations:

1. "charm.land/fantasy/providers/anthropic"
1. "charm.land/fantasy/providers/google"
1. "charm.land/fantasy/providers/openai"

Noice!

Only thing we need to really handle from now on relates to us storing the session (the list of messages). For that we have the `AssistantMessage` and `ToolResultMessage` functions that help to convert generated requests and our tool call executions respectively, back to `fantasy.Message` structs that we can store in the session.

In the tests we use these functions around handling the session. For an example let's look at simplified tool call execution code:

```go
// run each requested tool and collect the results as tool_result parts
session = append(session, AssistantMessage(resp)) // assistant turn carrying the tool calls
var results []fantasy.ToolResultPart
for _, call := range toolCalls {
	t.Logf("model called tool %q with input %s", call.ToolName, call.Input)

	out, _ := tool.Handler(call.Input)

	results = append(results, fantasy.ToolResultPart{
		ToolCallID: call.ToolCallID,
		Output:     fantasy.ToolResultOutputContentText{Text: out},
	})
}
session = append(session, ToolResultMessage(results...))
```

There, we can use `AssistantMessage()` to transform any kind of response contents in to a `fantasy.Message` and `ToolResultMessage()` we use after we have executed our tools. That session is again just fed back into the next request to `Generate` by the `fantasy.LanguageModel`.

## Tool Definitions

Our tools are nicely converted to `fantasy` too:

```go
package main

import (
	"time"

	"charm.land/fantasy"
)

type Tool struct {
	fantasy.FunctionTool
	Handler func(input string) (string, error)
}

// FunctionTools extracts the wire definitions from tools for a fantasy.Call.
func FunctionTools(tools ...Tool) []fantasy.Tool {
	out := make([]fantasy.Tool, len(tools))
	for i, t := range tools {
		out[i] = t.FunctionTool
	}
	return out
}

func CurrentTimeTool() Tool {
	return Tool{
		FunctionTool: fantasy.FunctionTool{
			Name:        "current_time",
			Description: "Returns the current time with one second precision, formatted as an RFC3339 timestamp.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		Handler: func(string) (string, error) {
			return time.Now().Truncate(time.Second).Format(time.RFC3339), nil
		},
	}
}
```

Now our `Tool` struct embeds the `fantasy.FunctionTool`, before we make a call to the model to generate a response, we use `FunctionTools()` to "extract" a plain `[]fantasy.Tool` we can then use in the `Generate` method:

```go
model := newTestModel(t)

tool := CurrentTimeTool()
tools := FunctionTools(tool)

session := fantasy.Prompt{
	fantasy.NewUserMessage("What is the current time? Use the current_time tool to find out."),
}

// turn 1: expect the model to ask for the tool
resp, err := model.Generate(ctx, fantasy.Call{
	Prompt: session,
	Tools: tools,
	MaxOutputTokens: new(maxTokens)
})
```

I wasn't fully happy with this refactoring by Claude at first, but there are several interfaces for tools in the fantasy package (`AgentTool`, `Tool`) and it seems we are now using the underlying `Tool` type here. I guess fair to say we kind of implement our own `AgentTool` type (just called a `Tool` in our package). A `fantasy.FunctionTool` is what we need to pass into the Generate method because there is a type assertion there:

```go
# from fantasy/providers/anthropic/anthropic.go
func (a languageModel) toTools(tools []fantasy.Tool, ...) (rawTools []json.RawMessage, ...) {
	for _, tool := range tools {
		if tool.GetType() == fantasy.ToolTypeFunction {
			// here our tools passed should be FunctionTools!
			ft, ok := tool.(fantasy.FunctionTool)
			if !ok {
				continue
			}
```

Because we embedded the struct this is given:

```go
type Tool struct {
        fantasy.FunctionTool
        Handler func(input string) (string, error)
}
```

I guess this implementation is _fine_. It does not feel restrictive in any way, very glad they made these types public so we can do this without a fork. There are also provider defined tools, so that's why `FunctionTool` is the type for our implementations.

## Ackshually

Technically, our requirement was that we want to execute the tool which was a very loose requirement. That is still possible with an `fantasy.AgentTool`:

```go
func bashTool(machine Machine) fantasy.AgentTool {
	return fantasy.NewAgentTool(
		"bash",
		"Run a shell command inside the machine's workspace and return its combined output.",
		func(ctx context.Context, in bashInput, _ fantasy.ToolCall) (fantasy.ToolResponse, error) {
			out, exitCode, err := machine.Exec(ctx, in.Command)
			if err != nil {
				return fantasy.NewTextErrorResponse(fmt.Sprintf("failed to run command: %v", err)), nil
			}
			return fantasy.NewTextResponse(fmt.Sprintf("exit code: %d\n%s", exitCode, out)), nil
		},
	)
}
```

You can think of the `machine` as a sandbox, something like a container or a microVM. This is why I want to control the tool calls, because I want the option to run individual tool calls in sandboxes instead of the agent process itself. We could still control much of the execution like this, but this feels like adopting a framework.

This isn't same as having full control when this runs, and perhaps also handling persistency around the agent execution. That's why I think it's best to go with the implementation that ignores all the Agent types. But I might change my mind... Let's see. I'm not saying that would bad either, just perhaps I want to do something differently and that is reason enough to be careful what to adopt.

## Conclusion

You can read the full code in [the repo](https://github.com/maikdotfi/agent-the-hard-way/tree/main/01-multimodel). After the refactoring all the tests seem to output the same raw requests/responses with fantasy as before. Only difference is that streaming is separate method (`Stream` insted of `Generate`), so the entire `stream` field is missing when using `Generate`. Unless doing something user-facing, I don't really see point in streaming. It makes it more cumbersome to spy on the traffic when you have to "unstream" the responses.

Last but not least, fantasy seems sweet so far for this lower level use-case of just talking to the LLM APIs instead of opting into the `Agent` type and tools.

