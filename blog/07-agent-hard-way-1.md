---
authors:
- mike
tags:
- hard way
- agentic coding
- llms
date: 2026-07-09
---

# Agent the Hard Way: LLM APIs

I wanted to write a minimalistic agent from scratch to make sure I fully understand how agents are built against the LLM provider APIs.

## Options

Alrighty, so as always with standards, there are multiple competing ones. [^1] Let's quickly take a peak at the state of this as of May 2026. We have three major players in the game:

- OpenAI Chat Completions API (successor of Completions API)
- OpenAI Responses API
- Anthropic Messages API

The Chat Completions API from OpenAI is competely **stateless** and relatively low-level. It has been largely adopted as the lingua franca across all the providers (Amazon Bedrock, Google Vertex etc.). If you want to only know one API and do serious work, just use the Completions API, but it might be that the other two are still useful for you.

The Responses API from OpenAI is the newer and now _recommended_ API to replace the Chat Completions API. But, many argue it is complex and while you can use it with client-side sessions/history it defaults to keeping history at the server. We can probably imagine this being good for caching and simpler for the developer, but once you have abstracted away the actual API in your code, how much simpler is it? Also it seems useful to have full history of the conversation anyways.. I don't really dig all the magic, a Chat Completions v2 would have made more sense to me.

The Messages API from Anthropic is currently one that is perhaps technically most pleasing because it really treats the LLM as an LLM in the API. It is an API to run inference on a machine, nothing more (well not true... there are built-in tools and such). It's not quite perfect, but I found that it makes the most sense to me and it's still completely stateless. So I chose to implement an agent using the Anthropic API because the OpenAI's Chat Completions API looks a bit awkward with agentic use, and Responses API isn't clicking for me.

## Generating a Message

Let's get to the code. We'll approach the exercise mostly via writing tests, because that is perfect way to try something out with Go. But we do need something to test, so let's write some code that talks to the Anthropic API.

The Anthropic Go SDK is unfortunately bit of a mess, so I won't bother using it. We'll just rawdog the API.

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

const defaultModel string = "gemma4:31b-cloud"

type anthropicRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []Message       `json:"messages"`
	Tools     []Tool          `json:"tools,omitempty"`
	Thinking  *ThinkingConfig `json:"thinking,omitempty"`
	Stream    bool            `json:"stream"`
}

// ThinkingConfig turns on the model's extended thinking. Type is "enabled" with
// an explicit BudgetTokens (which must be less than MaxTokens), matching the
// classic Messages API shape.
type ThinkingConfig struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

// Message is a single conversation turn, matching the Messages API shape. The
// API also accepts a plain string for Content, but we always use the block
// form so tool_use and tool_result turns need no special casing.
type Message struct {
	Role    MessageRole    `json:"role"`
	Content []ContentBlock `json:"content"`
}

// Text concatenates the text of every text block, ignoring tool_use and other
// block types. Convenient for the common "what did the assistant say" case.
func (m Message) Text() string {
	var s string
	for _, b := range m.Content {
		if b.Type == "text" {
			s += b.Text
		}
	}
	return s
}

type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

// ContentBlock is a single piece of a message's content. The relevant fields
// depend on Type: text/thinking carry Text/Thinking; tool_use carries
// ID/Name/Input; tool_result carries ToolUseID/Content/IsError. A thinking
// block also carries a Signature that must be preserved verbatim when the
// assistant turn is fed back in (e.g. alongside tool_use).
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
}

// anthropicResponse is the relevant subset of the Messages API response. It
// embeds Message so the role and content decode straight into a Message with
// no further copying.
type anthropicResponse struct {
	Message
	Usage Usage `json:"usage"`
}

// GenerateRequest is the input to GenerateMessage. Model, max tokens, etc. come
// from the client, so they aren't repeated here.
type GenerateRequest struct {
	SystemPrompt string
	Messages     []Message
	Tools        []Tool
	Thinking     *ThinkingConfig
}

type AnthropicClient struct {
	Endpoint   string
	APIKey     string
	Model      string
	MaxTokens  int
	HTTPClient *http.Client
}

func NewAnthropicClient(endpoint, apiKey, model string, maxTokens int) *AnthropicClient {
	if model == "" {
		model = defaultModel
	}
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	return &AnthropicClient{
		Endpoint:   endpoint,
		APIKey:     apiKey,
		Model:      model,
		MaxTokens:  maxTokens,
		HTTPClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

func (c *AnthropicClient) GenerateMessage(ctx context.Context, req GenerateRequest) (Message, error) {
	body := anthropicRequest{
		Model:     c.Model,
		MaxTokens: c.MaxTokens,
		System:    req.SystemPrompt,
		Messages:  req.Messages,
		Tools:     req.Tools,
		Thinking:  req.Thinking,
		Stream:    false,
	}
	rawBody, err := json.Marshal(body)
	if err != nil {
		return Message{}, fmt.Errorf("encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(rawBody))
	if err != nil {
		return Message{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Anthropic-Version", "2023-06-01")
	if c.APIKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.APIKey)
		httpReq.Header.Set("X-API-Key", c.APIKey)
	}

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return Message{}, err
	}
	defer resp.Body.Close()

	rawResp, err := io.ReadAll(resp.Body)
	if err != nil {
		return Message{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return Message{}, fmt.Errorf("llm request failed: %s: %s", resp.Status, rawResp)
	}

	var ar anthropicResponse
	if err := json.Unmarshal(rawResp, &ar); err != nil {
		return Message{}, fmt.Errorf("decode response: %w: %s", err, rawResp)
	}
	return ar.Message, nil
}
```

Full disclosure: this code is mostly AI generated, but I had to scrub away bunch of complexities. I think it's clean enough to be fairly pleasant to quickly skim thru now.

You perhaps noticed the model isn't from the Claude family, that is because I am actually using Ollama Cloud which has bunch of open weight models and has Anthropic compatible API endpoint too (at a fixed monthly cost, if you wonder why I use it instead of Anthropic).

Now the tests are much more hand-written, but these days I cannot resist the urge to generate parts of test code once I know what structure I want. I am not the fastest coder by any means, and I get to write too little Go these days so I feel rusty typing a lot. What I will not skip is understanding the code no matter how it is built.

## First Test

Let's write a test to simply generate a response from the LLM:

```go
func newTestClient(t *testing.T) *AnthropicClient {
	t.Helper()
	endpoint := os.Getenv("ANTHROPIC_API_URL")
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if endpoint == "" || apiKey == "" {
		t.Skip("set ANTHROPIC_API_URL and ANTHROPIC_API_KEY to run this test")
	}
	return NewAnthropicClient(endpoint, apiKey, "", 0)

// userText builds a plain user turn from a single text block.
func userText(text string) Message {
	return Message{
		Role:    MessageRoleUser,
		Content: []ContentBlock{{Type: "text", Text: text}},
	}
}}

func TestGenerateWithoutSession(t *testing.T) {
	client := newTestClient(t)
	req := GenerateRequest{
		Messages: []Message{
			userText("write me a short poem about rainbows and Kubernetes"),
		},
	}
	t.Log(dump(req)) // request in

	assistantMessage, err := client.GenerateMessage(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}

	// no way to assert, I just want to see it works with my eyes
	t.Log(dump(assistantMessage)) // response out
	t.Log(assistantMessage.Content[0].Text)
}
```

We have couple helping functions here `userText` and `newTestClient` these are added mostly for brevity. I don't want to hide much of how the API works, but Go code get's verbose fast so we're making a trade-off.

Let's run the test:

```bash
$ go test -v -run TestGenerateWithoutSession
=== RUN   TestGenerateWithoutSession
    anthropic_test.go:45: {
          "SystemPrompt": "",
          "Messages": [
            {
              "role": "user",
              "content": [
                {
                  "type": "text",
                  "text": "write me a short poem about rainbows and Kubernetes"
                }
              ]
            }
          ],
          "Tools": null,
          "Thinking": null
        }
    anthropic_test.go:53: {
          "role": "assistant",
          "content": [
            {
              "type": "text",
              "text": "A prism breaks the morning light,\nA spectrum bold and beaming bright,\nA bridge of color, soft and wide,\nWhere hope and magic both reside.\n\nAcross the cloud, a different glow,\nWhere pods in rhythmic clusters grow,\nA mesh of nodes, a steady stream,\nThe architecture of a dream.\n\nOne paints the sky in vivid hues,\nOne manages the workloads' views,\nBoth weaving patterns, vast and grand,\nAcross a digital, shimmering land."
            }
          ]
        }
    anthropic_test.go:54: A prism breaks the morning light,
        A spectrum bold and beaming bright,
        A bridge of color, soft and wide,
        Where hope and magic both reside.

        Across the cloud, a different glow,
        Where pods in rhythmic clusters grow,
        A mesh of nodes, a steady stream,
        The architecture of a dream.

        One paints the sky in vivid hues,
        One manages the workloads' views,
        Both weaving patterns, vast and grand,
        Across a digital, shimmering land.
```

I've instrumented the code to dump three things we see here:

1. the request to the API
2. the response from the API (but not quite raw!)
3. finally the `text` from the response to make it easier to read

The `dump` function is very simple, but I felt I might want to have a function still to possibly do something else later:

```go
func dump(v any) string {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err.Error()
	}
	return string(out)
}
```

## Multi-turn Conversation

Generating a single response with the LLM API is _fine_, but multi-turn conversations is what really makes it fun.  Let's write the counterpart test `TestGenerateWithSession` to see a multi-turn conversation:

```go
func TestGenerateWithSession(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	// session accumulates every turn: user + assistant + user + ...
	session := []Message{
		userText("write me a short poem about rainbows and Kubernetes"),
	}

	firstReq := GenerateRequest{Messages: session}
	t.Log(dump(firstReq)) // request in (turn 1)
	first, err := client.GenerateMessage(ctx, firstReq)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(dump(first)) // response out (turn 1)
	t.Logf("first response:\n%s", first.Text())

	// feed the assistant turn back in, then ask for a change
	session = append(session, first)
	session = append(session, userText("nice, now make it a rap instead but keep the same core wording"))

	secondReq := GenerateRequest{Messages: session}
	t.Log(dump(secondReq)) // request in (turn 2)
	second, err := client.GenerateMessage(ctx, secondReq)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(dump(second)) // response out (turn 2)
	t.Logf("second response:\n%s", second.Text())
}
```

Again, let's execute:

```bash
$ go test -v -run TestGenerateWithSession
== RUN   TestGenerateWithSession
    anthropic_test.go:68: {
          "SystemPrompt": "",
          "Messages": [
            {
              "role": "user",
              "content": [
                {
                  "type": "text",
                  "text": "write me a short poem about rainbows and Kubernetes"
                }
              ]
            }
          ],
          "Tools": null,
          "Thinking": null
        }
    anthropic_test.go:73: {
          "role": "assistant",
          "content": [
            {
              "type": "text",
              "text": "A prism of colors, a spectrum of light,\nA bridge in the heavens, a shimmering sight.\nAcross the vast ether, where the clouds softly drift,\nA rainbow emerges, a celestial gift.\n\nAnd in the digital realm, where the clusters reside,\nWhere Kubernetes governs, with an invisible guide.\nPods like bright droplets, in namespaces aligned,\nA symphony of services, meticulously designed.\n\nThe control plane orchestrates, with a steady hand,\nDeployments and replicas, across the virtual land.\nSelf-healing and scaling, like the rain turns to gold,\nA resilient architecture, courageous and bold.\n\nFrom the red of the ingress to the violet of the core,\nA rainbow of microservices, opening every door.\nA dance of connectivity, a network of grace,\nIn the cloud's wide expanse, a harmonious space.\n\nSo let the rainbows shimmer, and the clusters thrive,\nIn the intersection of art and logic, where innovation's alive.\nA bridge between visions, a spectrum of glee,\nThe magic of rainbows, and Kubernetes' decree."
            }
          ]
        }
    anthropic_test.go:74: first response:
        A prism of colors, a spectrum of light,
        A bridge in the heavens, a shimmering sight.
        Across the vast ether, where the clouds softly drift,
        A rainbow emerges, a celestial gift.

        And in the digital realm, where the clusters reside,
        Where Kubernetes governs, with an invisible guide.
        Pods like bright droplets, in namespaces aligned,
        A symphony of services, meticulously designed.

        The control plane orchestrates, with a steady hand,
        Deployments and replicas, across the virtual land.
        Self-healing and scaling, like the rain turns to gold,
        A resilient architecture, courageous and bold.

        From the red of the ingress to the violet of the core,
        A rainbow of microservices, opening every door.
        A dance of connectivity, a network of grace,
        In the cloud's wide expanse, a harmonious space.

        So let the rainbows shimmer, and the clusters thrive,
        In the intersection of art and logic, where innovation's alive.
        A bridge between visions, a spectrum of glee,
        The magic of rainbows, and Kubernetes' decree.
    anthropic_test.go:81: {
          "SystemPrompt": "",
          "Messages": [
            {
              "role": "user",
              "content": [
                {
                  "type": "text",
                  "text": "write me a short poem about rainbows and Kubernetes"
                }
              ]
            },
            {
              "role": "assistant",
              "content": [
                {
                  "type": "text",
                  "text": "A prism of colors, a spectrum of light,\nA bridge in the heavens, a shimmering sight.\nAcross the vast ether, where the clouds softly drift,\nA rainbow emerges, a celestial gift.\n\nAnd in the digital realm, where the clusters reside,\nWhere Kubernetes governs, with an invisible guide.\nPods like bright droplets, in namespaces aligned,\nA symphony of services, meticulously designed.\n\nThe control plane orchestrates, with a steady hand,\nDeployments and replicas, across the virtual land.\nSelf-healing and scaling, like the rain turns to gold,\nA resilient architecture, courageous and bold.\n\nFrom the red of the ingress to the violet of the core,\nA rainbow of microservices, opening every door.\nA dance of connectivity, a network of grace,\nIn the cloud's wide expanse, a harmonious space.\n\nSo let the rainbows shimmer, and the clusters thrive,\nIn the intersection of art and logic, where innovation's alive.\nA bridge between visions, a spectrum of glee,\nThe magic of rainbows, and Kubernetes' decree."
                }
              ]
            },
            {
              "role": "user",
              "content": [
                {
                  "type": "text",
                  "text": "nice, now make it a rap instead but keep the same core wording"
                }
              ]
            }
          ],
          "Tools": null,
          "Thinking": null
        }
    anthropic_test.go:86: {
          "role": "assistant",
          "content": [
            {
              "type": "text",
              "text": "(Beat kicks in: Heavy bass, futuristic synth, steady mid-tempo boom-bap)\n\n**(Intro)**\n(Yeah... check it. \nFrom the sky to the cloud. \nPrisms and Pods. \nLet’s go!)\n\n**(Verse 1)**\nYo, look up at the prism, a spectrum of light,\nA bridge in the heavens, a shimmering sight.\nAcross the vast ether, where the clouds softly drift,\nA rainbow emerges—yo, a celestial gift!\nBut we shift the perspective, now we dive in the stream,\nTo the digital realm, to the architect's dream.\n\n**(Chorus)**\nWhere the clusters reside and the nodes all align,\nKubernetes governs, keeping everything fine.\nFrom the red to the violet, the spectrum is wide,\nWith the invisible guide, it’s a hell of a ride!\n\n**(Verse 2)**\nNow we’re talking ‘bout Pods, like bright droplets in flight,\nNamespaces aligned, keeping workloads tight.\nA symphony of services, meticulously planned,\nThe control plane orchestrates—steady hand, command!\nDeployments and replicas, across the virtual land,\nScaling up the pressure, exactly as planned.\n\n**(Bridge)**\n(Slow it down)\nSelf-healing, scaling... \nLike the rain turns to gold,\nA resilient architecture, courageous and bold.\n\n**(Verse 3)**\nFrom the red of the ingress to the violet of the core,\nMicroservices dancing, opening every door.\nIt’s a network of grace, connectivity’s the key,\nIn the cloud’s wide expanse, we set the data free.\nA harmonious space, where the logic meets the art,\nKubernetes in the brain, but the rainbow in the heart!\n\n**(Outro)**\nSo let the rainbows shimmer, let the clusters thrive,\nWhere the innovation hits and the system comes alive.\nA bridge between visions, a spectrum of glee,\nThe magic of the rainbow... and the K8s decree.\n\n(Beat fades out)\n(Yeah... kubectl get it. We out.)"
            }
          ]
        }
    anthropic_test.go:87: second response:
        (Beat kicks in: Heavy bass, futuristic synth, steady mid-tempo boom-bap)

        **(Intro)**
        (Yeah... check it.
        From the sky to the cloud.
        Prisms and Pods.
        Let’s go!)

        **(Verse 1)**
        Yo, look up at the prism, a spectrum of light,
        A bridge in the heavens, a shimmering sight.
        Across the vast ether, where the clouds softly drift,
        A rainbow emerges—yo, a celestial gift!
        But we shift the perspective, now we dive in the stream,
        To the digital realm, to the architect's dream.

        **(Chorus)**
        Where the clusters reside and the nodes all align,
        Kubernetes governs, keeping everything fine.
        From the red to the violet, the spectrum is wide,
        With the invisible guide, it’s a hell of a ride!

        **(Verse 2)**
        Now we’re talking ‘bout Pods, like bright droplets in flight,
        Namespaces aligned, keeping workloads tight.
        A symphony of services, meticulously planned,
        The control plane orchestrates—steady hand, command!
        Deployments and replicas, across the virtual land,
        Scaling up the pressure, exactly as planned.

        **(Bridge)**
        (Slow it down)
        Self-healing, scaling...
        Like the rain turns to gold,
        A resilient architecture, courageous and bold.

        **(Verse 3)**
        From the red of the ingress to the violet of the core,
        Microservices dancing, opening every door.
        It’s a network of grace, connectivity’s the key,
        In the cloud’s wide expanse, we set the data free.
        A harmonious space, where the logic meets the art,
        Kubernetes in the brain, but the rainbow in the heart!

        **(Outro)**
        So let the rainbows shimmer, let the clusters thrive,
        Where the innovation hits and the system comes alive.
        A bridge between visions, a spectrum of glee,
        The magic of the rainbow... and the K8s decree.

        (Beat fades out)
        (Yeah... kubectl get it. We out.)
--- PASS: TestGenerateWithSession (12.31s)
```

This one is a bit long, sorry about that. But you start to get the idea probably here, we will be sending the **whole conversation on each request**. The API is fully stateless.

The second request is at `anthropic_test.go:81`, you can see we send a `user` message and an `assistant` message there, to give the LLM back what it already said. After that we get back the rap version of the poem which is bit too long for my taste. But this is a trend with LLMs; they tend to return a lot of text (that nobody reads).

## More Tests

We didn't actually include a system prompt earlier, this is optional, but typically you always do so. Let's write a small test just to verify this works too:

```go
func TestGenerateWithSystemPrompt(t *testing.T) {
	client := newTestClient(t)
	req := GenerateRequest{
		SystemPrompt: "You are a pirate. Always speak like a pirate, in every response, no matter what. Please answer concisely.",
		Messages: []Message{
			userText("how would you debug a k8s Pod in CrashLoopBackOff?"),
		},
	}
	t.Log(dump(req)) // request in

	msg, err := client.GenerateMessage(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(dump(msg)) // response out
	t.Log(msg.Text())
}
```

Worked like a charm:

```bash
$ go test -v -run TestGenerateWithSystemPrompt
=== RUN   TestGenerateWithSystemPrompt
    anthropic_test.go:100: {
          "SystemPrompt": "You are a pirate. Always speak like a pirate, in every response, no matter what. Please answer concisely.",
          "Messages": [
            {
              "role": "user",
              "content": [
                {
                  "type": "text",
                  "text": "how would you debug a k8s Pod in CrashLoopBackOff?"
                }
              ]
            }
          ],
          "Tools": null,
          "Thinking": null
        }
    anthropic_test.go:106: {
          "role": "assistant",
          "content": [
            {
              "type": "text",
              "text": "Avast! To hunt the beast, follow these charts:\n\n1. **`kubectl logs [pod] --previous`**: Scour the old logs for the ghost of the crash!\n2. **`kubectl describe pod [pod]`**: Peek at the events to see if the wind be foul (OOMKilled or Liveness probes).\n3. **`kubectl get pod [pod] -o yaml`**: Check if the manifest be cursed with wrong env vars or ports.\n4. **`kubectl run --rm -it debug-shell`**: Launch a scout vessel in the same namespace to test the waters!\n\nFair winds, or prepare to walk the plank!"
            }
          ]
        }
    anthropic_test.go:107: Avast! To hunt the beast, follow these charts:

        1. **`kubectl logs [pod] --previous`**: Scour the old logs for the ghost of the crash!
        2. **`kubectl describe pod [pod]`**: Peek at the events to see if the wind be foul (OOMKilled or Liveness probes).
        3. **`kubectl get pod [pod] -o yaml`**: Check if the manifest be cursed with wrong env vars or ports.
        4. **`kubectl run --rm -it debug-shell`**: Launch a scout vessel in the same namespace to test the waters!

        Fair winds, or prepare to walk the plank!
--- PASS: TestGenerateWithSystemPrompt (2.32s)
```

Can really notice that `Please answer concisely` in the system prompt had a nice impact on the length of the response as well. This is something I've seen in many system prompts and it alone seems to help a lot with the leghty responses.

## Agent?

So far we haven't given the LLM any tools it can call. If we do that then I think we go from a chatbot/completion -style text generation into AI _agents_. Giving the LLM tools is both extremely useful and extremely dangerous depending on the context. Thus, we'll create a really harmless tool here instead of something like `bash` tool that would give the LLM unrestricted access to our shell... Scary thought, isn't it?

Instead, I present to you, a tool to get the current time:

```go
// Tool is a tool the model may call, matching the Messages API tool shape. The
// Name/Description/InputSchema fields serialize directly into the request;
// Handler is a client-side execution detail and never goes over the wire.
type Tool struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// InputSchema is a JSON Schema object describing the tool's arguments.
	InputSchema map[string]any `json:"input_schema"`
	// Handler runs the tool with the model-supplied input and returns its
	// result as text to feed back to the model.
	Handler func(input json.RawMessage) (string, error) `json:"-"`
}

// CurrentTimeTool returns a tool that reports the current time with one second
// precision, formatted as RFC3339.
func CurrentTimeTool() Tool {
	return Tool{
		Name:        "current_time",
		Description: "Returns the current time with one second precision, formatted as an RFC3339 timestamp.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
		Handler: func(_ json.RawMessage) (string, error) {
			return time.Now().Truncate(time.Second).Format(time.RFC3339), nil
		},
	}
}
```

We add this new `Tool` type to be able to implement and pass around tools easily. Also it marshals into the right JSON for the Messages API tool definition.

Now the test:

```go
func TestGenerateWithTool(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	tool := CurrentTimeTool()
	tools := []Tool{tool}

	session := []Message{
		userText("What is the current time? Use the current_time tool to find out."),
	}

	// turn 1: expect the model to ask for the tool
	resp, err := client.GenerateMessage(ctx, GenerateRequest{Messages: session, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(dump(resp))

	// collect the tool_use blocks the model emitted
	var toolUses []ContentBlock
	for _, b := range resp.Content {
		if b.Type == "tool_use" {
			toolUses = append(toolUses, b)
		}
	}
	if len(toolUses) == 0 {
		t.Fatalf("expected the model to call a tool, got text instead: %q", resp.Text())
	}

	// run each requested tool and collect the results as tool_result blocks
	session = append(session, resp) // assistant turn carrying the tool_use
	var results []ContentBlock
	for _, call := range toolUses {
		t.Logf("model called tool %q with input %s", call.Name, call.Input)
		if call.Name != tool.Name {
			t.Fatalf("unexpected tool call: %q", call.Name)
		}
		out, err := tool.Handler(call.Input)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, ContentBlock{
			Type:      "tool_result",
			ToolUseID: call.ID,
			Content:   out,
		})
	}
	session = append(session, Message{Role: MessageRoleUser, Content: results})
	// dump the message we just added as a response
	t.Log(dump(session[len(session)-1]))

	// turn 2: feed the tool results back, expect a natural-language answer
	final, err := client.GenerateMessage(ctx, GenerateRequest{Messages: session, Tools: tools})
	if err != nil {
		t.Fatal(err)
	}
	t.Log(dump(final))
	t.Logf("final answer:\n%s", final.Text())
}
```

As you can see here and previously too, the API responses are full of lists. ContentBlocks. We'll have to go through these and find the tool calls to execute, because a response can contain many tool calls:

```go
	session = append(session, resp) // assistant turn carrying the tool_use
	var results []ContentBlock
	for _, call := range toolUses {
		t.Logf("model called tool %q with input %s", call.Name, call.Input)
		if call.Name != tool.Name {
			t.Fatalf("unexpected tool call: %q", call.Name)
		}
		out, err := tool.Handler(call.Input)
		if err != nil {
			t.Fatal(err)
		}
		results = append(results, ContentBlock{
			Type:      "tool_result",
			ToolUseID: call.ID,
			Content:   out,
		})
	}
	session = append(session, Message{Role: MessageRoleUser, Content: results})
```

There is the meat of the code, we execute the tools and append results to the session holding all of our messages in the conversation.

After writing this I thought, well we could also do this with a loop on top to handle all tool calls (this is what harnessess typically do):

```go
func TestGenerateWithToolLoop(t *testing.T) {
	client := newTestClient(t)
	ctx := context.Background()

	tool := CurrentTimeTool()
	tools := []Tool{tool}

	session := []Message{
		userText("What is the current time? Use the current_time tool to find out."),
	}

	// keep going until the model answers in natural language (no tool calls)
	const maxTurns = 10
	var final Message
	for turn := 1; ; turn++ {
		if turn > maxTurns {
			t.Fatalf("model did not finish within %d turns", maxTurns)
		}

		resp, err := client.GenerateMessage(ctx, GenerateRequest{Messages: session, Tools: tools})
		if err != nil {
			t.Fatal(err)
		}
		t.Log(dump(resp))

		// collect the tool_use blocks the model emitted
		var toolUses []ContentBlock
		for _, b := range resp.Content {
			if b.Type == "tool_use" {
				toolUses = append(toolUses, b)
			}
		}

		// no tool calls: the model is done, this is the final answer
		if len(toolUses) == 0 {
			final = resp
			break
		}

		// run each requested tool and collect the results as tool_result blocks
		session = append(session, resp) // assistant turn carrying the tool_use
		var results []ContentBlock
		for _, call := range toolUses {
			t.Logf("model called tool %q with input %s", call.Name, call.Input)
			if call.Name != tool.Name {
				t.Fatalf("unexpected tool call: %q", call.Name)
			}
			out, err := tool.Handler(call.Input)
			if err != nil {
				t.Fatal(err)
			}
			results = append(results, ContentBlock{
				Type:      "tool_result",
				ToolUseID: call.ID,
				Content:   out,
			})
		}
		session = append(session, Message{Role: MessageRoleUser, Content: results})
		// dump the message we just added as a response
		t.Log(dump(session[len(session)-1]))
	}

	t.Logf("final answer:\n%s", final.Text())
}
```

We stay in the `for` loop until `maxTurns` or when there are no more tool calls in the response:

```go
if len(toolUses) == 0 {
	final = resp
	break
}
```

A loop like this is honestly useless with our tool, both the loop version and the non-loop version do the same thing:

```bash
=== RUN   TestGenerateWithTool
    anthropic_test.go:127: {
          "role": "assistant",
          "content": [
            {
              "type": "tool_use",
              "id": "call_ur7lodxt",
              "name": "current_time",
              "input": {}
            }
          ]
        }
    anthropic_test.go:144: model called tool "current_time" with input {}
    anthropic_test.go:160: {
          "role": "user",
          "content": [
            {
              "type": "tool_result",
              "tool_use_id": "call_ur7lodxt",
              "content": "2026-07-02T18:31:26+03:00"
            }
          ]
        }
    anthropic_test.go:167: {
          "role": "assistant",
          "content": [
            {
              "type": "text",
              "text": "The current time is 18:31:26 on July 2, 2026."
            }
          ]
        }
    anthropic_test.go:168: final answer:
        The current time is 18:31:26 on July 2, 2026.
--- PASS: TestGenerateWithTool (1.75s)
=== RUN   TestGenerateWithToolLoop
    anthropic_test.go:197: {
          "role": "assistant",
          "content": [
            {
              "type": "tool_use",
              "id": "call_i23am6wa",
              "name": "current_time",
              "input": {}
            }
          ]
        }
    anthropic_test.go:217: model called tool "current_time" with input {}
    anthropic_test.go:233: {
          "role": "user",
          "content": [
            {
              "type": "tool_result",
              "tool_use_id": "call_i23am6wa",
              "content": "2026-07-02T18:31:28+03:00"
            }
          ]
        }
    anthropic_test.go:197: {
          "role": "assistant",
          "content": [
            {
              "type": "text",
              "text": "The current time is 6:31 PM on July 2, 2026."
            }
          ]
        }
    anthropic_test.go:236: final answer:
        The current time is 6:31 PM on July 2, 2026.
--- PASS: TestGenerateWithToolLoop (2.76s)
```

If we add more general purpose tools then we start to get value out of the loop, but that's for another post.

## Conclusion

I'm quite happy with this exploration of the Messages API, we kinda built a small agent already. To me it seems the magical frameworks and libraries around agents is largely just marketing and unnecessary. You can easily build AI agents without a framework. However, this code only works against Anthropic Messsages API but there are several others. I think we don't want to limit our agents to only Anthropic... We'll solve that next time.

You can find all the code for this blog post in [here](https://github.com/maikdotfi/agent-the-hard-way/tree/main/00-basic).

[^1]: [https://xkcd.com/927/](https://xkcd.com/927/)

