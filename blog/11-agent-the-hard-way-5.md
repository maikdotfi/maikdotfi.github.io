---
authors:
- mike
tags:
- hard way
- agentic coding
- llms
date: 2026-07-31
---

# Agent the Hard Way: Putting it together

For the first time let's actually create a `main.go` and run an agent.

## Code Review Agent

We'll put together a code review agent. It will be given basic coding agent tools to use (bash, read, write, edit). You could think a code review agent does not need access to edit files, but I kinda do think it needs access to bash so it would be silly not to let it use edit files too. In addition it will have the `skill` tool and a skill for the review of course, it's not just a generic coding agent given a review task... It's something special.

## Grug Review

Our code review skill is a bit special. It is, of course, based on the legendary [Grug Brained Developer](https://grugbrain.dev/) article which I adore.

This skill is simply defined in Go:

```go
func GrugReview() Skill {
	return Skill{
		Name:         "grug-review",
		Description:  "Review code for complexity, brittle APIs, bugs, and changes that would make maintainers reach for the club.",
		Instructions: grugReviewSkillInstruction,
	}
}

const grugReviewSkillInstruction = `Review code looking for things that would lead the grug brained developer to reach for his club.
...`
```

The full instructions/`SKILL.md` is way too long to include here, but you can read it all [here](https://github.com/maikdotfi/metaharness/blob/main/skills/grugreview.go).

## main.go

We'll make it possible to set `ANTHROPIC_API_URL` and of course `ANTHROPIC_API_TOKEN` to use another provider (Ollama in my case):

```go
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/maikdotfi/metaharness/agent"
	"github.com/maikdotfi/metaharness/model"
	"github.com/maikdotfi/metaharness/sandbox"
	"github.com/maikdotfi/metaharness/skills"
	"github.com/maikdotfi/metaharness/tools"
)

const systemPrompt = `You are a code review agent. Review the Go code in the current working directory.

Inspect the code and tests with your file tools, run anything useful (like go test ./...) with bash, and finish with review feedback that references files and line numbers.

Before writing any feedback, load the grug-review skill with the skill tool and follow its instructions for the tone and focus of the review.`

const defaultPrompt = "Review this checkout package. Inspect the code and tests, run anything useful, and return review feedback with file and line references."

func main() {
	modelName := flag.String("model", "gemma4:31b-cloud", "Anthropic model id")
	workdir := flag.String("workdir", "checkout", "directory the agent's tools run in")
	prompt := flag.String("prompt", defaultPrompt, "user prompt to start the review with")
	think := flag.Bool("think", false, "enable extended thinking output")
	effort := flag.String("effort", "medium", "thinking effort level (low, medium, high, xhigh, max); only used with -think")
	flag.Parse()

	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		log.Fatal("ANTHROPIC_API_KEY is not set")
	}
	if err := run(context.Background(), *modelName, *workdir, *prompt, *think, *effort); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, modelName, workdir, prompt string, think bool, effort string) error {
	cfg := model.Config{
		Provider: model.ProviderAnthropic,
		APIKey:   os.Getenv("ANTHROPIC_API_KEY"),
		BaseURL:  os.Getenv("ANTHROPIC_API_URL"),
	}
	if think {
		cfg.Thinking = &model.Thinking{Effort: model.Effort(effort)}
	}
	m, err := model.New(cfg)
	if err != nil {
		return err
	}

	box, err := sandbox.Dir(workdir)
	if err != nil {
		return err
	}

	a := agent.New(systemPrompt,
		agent.WithModel(m),
		agent.WithTools(
			agent.Adapt(tools.Bash{}),
			agent.Adapt(tools.ReadFile{}),
			agent.Adapt(tools.EditFile{}),
			agent.Adapt(tools.WriteFile{}),
			tools.NewSkill(skills.GrugReview()),
		),
	)

	sess := agent.NewSession(fmt.Sprintf("review-%d", time.Now().Unix()), modelName, box)
	defer sess.Close()
	sess.Messages = append(sess.Messages, model.NewUserMessage(prompt))
	fmt.Printf("session %s in sandbox %s\n\n", sess.ID, sess.SandboxName())

	events, err := a.Run(ctx, sess)
	if err != nil {
		return err
	}
	for ev := range events {
		switch ev.Type {
		case agent.EventAssistant:
			printAssistant(ev.Message)
		case agent.EventToolResult:
			printToolResult(ev.Message)
		case agent.EventDone:
			fmt.Printf("\n--- done: %d input tokens, %d output tokens ---\n",
				sess.Usage.InputTokens, sess.Usage.OutputTokens)
		case agent.EventError:
			return ev.Err
		}
	}
	return nil
}
```

You also saw these couple helping functions, nothing much to mention about them as you probably guessed:

```go
func printAssistant(m *model.Message) {
	for _, reasoning := range model.ReasoningParts(m) {
		if reasoning.Text != "" {
			fmt.Printf("\n💭 %s\n", reasoning.Text)
		}
	}
	for _, text := range model.TextParts(m) {
		if text.Text != "" {
			fmt.Printf("\n%s\n", text.Text)
		}
	}
	for _, call := range model.ToolCalls(m) {
		fmt.Printf("→ %s %s\n", call.Name, clip(call.Input, 120))
	}
}

func printToolResult(m *model.Message) {
	for _, result := range model.ToolResults(m) {
		if result.Error != nil {
			fmt.Printf("← error: %v\n", result.Error)
		} else {
			fmt.Printf("← %s\n", clip(result.Text, 200))
		}
	}
}

// clip keeps event logging one-line-ish.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
```

I rather like how the agent is constructed using the [functional options pattern](https://github.com/tmrts/go-patterns/blob/master/idiom/functional-options.md):

```go
a := agent.New(systemPrompt,
	agent.WithModel(m),
	agent.WithTools(
		agent.Adapt(tools.Bash{}),
		agent.Adapt(tools.ReadFile{}),
		agent.Adapt(tools.EditFile{}),
		agent.Adapt(tools.WriteFile{}),
		tools.NewSkill(skills.GrugReview()),
	),
)
```

We can see there the whole skills thing just passed as another tool.

Then when we run this we pass in the session, so that session (the messages rather) could be stored somewhere in future. We start it up and receive a channel we can then listen for the events to show a user, or just log out to stdout/telemetry backends:


```go
events, err := a.Run(ctx, sess)
if err != nil {
	return err
}
for ev := range events {
	switch ev.Type {
	case agent.EventAssistant:
		printAssistant(ev.Message)
	case agent.EventToolResult:
		printToolResult(ev.Message)
	case agent.EventDone:
		fmt.Printf("\n--- done: %d input tokens, %d output tokens ---\n",
			sess.Usage.InputTokens, sess.Usage.OutputTokens)
	case agent.EventError:
		return ev.Err
	}
}
```

## Let it rip

Alright let's run this once without and then with thinking.

First without thinking flags:

```bash
go run .
session review-1784888752

→ skill {"skill":"grug-review"}
← <skill_content name="grug-review">
Review code looking for things that would lead the grug brained developer to reach for his club.
Use the grug brained developer tone when giving the feedback.

Below…
→ bash {"cmd":"find . -maxdepth 2 -not -path '*/.*'"}
← .
./go.mod
./cart.go
./cart_test.go

→ read_file {"path":"cart.go"}
←      1        package checkout
     2
     3  import "fmt"
     4
     5  // Inventory maps a SKU to the number of units available.
     6  type Inventory map[string]int
     7
     8  type Item struct {
    …
→ read_file {"path":"cart_test.go"}
←      1        package checkout
     2
     3  import "testing"
     4
     5  func TestTotalCentsAppliesCoupon(t *testing.T) {
     6          var cart Cart
     7          if err := cart.Add("coffee", 2, 500); err != nil {…
→ bash {"cmd":"go test -v ./..."}
← error: === RUN   TestTotalCentsAppliesCoupon
--- PASS: TestTotalCentsAppliesCoupon (0.00s)
=== RUN   TestAddRejectsNegativeQuantities
    cart_test.go:21: expected negative quantity to be rejected
--- FAIL: TestAddRejectsNegativeQuantities (0.00s)
=== RUN   TestCheckoutRejectsMissingInventory
--- PASS: TestCheckoutRejectsMissingInventory (0.00s)
FAIL
FAIL    example.com/checkout    0.354s
FAIL


grug look at code. grug find things that make grug reach for club.

### 1. Bug: Grug find lie in test
`cart_test.go:17` say it reject negative quantity. But `cart.go:24` not check quantity!
Test fail because code not do what it say. This make grug very angry. Fix code or fix test, but not lie to grug.

### 2. Precision: Math go boom
`cart.go:52`: `lineTotal -= lineTotal * c.Coupon.PercentOff / 100`
This is "big brain" math. If `PercentOff` is 100, total 0. Good. But if `PercentOff` is 110, price become negative? Store pay customer to take item?
Grug not like store giving money away. Put limit on coupon or use better math.

### 3. API: Coupon too simple
`cart.go:40`: `ApplyCoupon(code string, percentOff int)`
Why grug need to tell cart how much percent off? Cart should just take `code`. If cart know percent, cart is doing too much. Logic for "what code means" should be in separate place, not passed in by whoever call `ApplyCoupon`.

### 4. Complexity: Inventory map
`cart.go:59`: `Checkout(inventory Inventory)`
Pass map of all things in world to checkout? Very big brain. Very brittle.
If inventory is huge, grug think too much. Better to have `Inventory` be thing with `Check(sku, qty)` method.

### Summary for big brain:
- `cart.go:24`: Missing check for `quantity < 0`.
- `cart.go:52`: Potential for negative totals with high coupon values.
- `cart.go:40`: API leak. Caller should not provide the discount value.
- `cart.go:59`: Poor abstraction of inventory.

--- done: 11346 input tokens, 556 output tokens ---
```

Nice! Our grug brained reviewer didn't really appreciate the big brain stuff going on in the [checkout example code](https://github.com/maikdotfi/metaharness/tree/main/examples/code-review/checkout).


## Thinking

We can also run with thinking enabled and printed to the screen:

````bash
go run . -think -effort medium
session review-1784897445


💭 Okay, I need to review the checkout package.
First, I'll start by loading the `grug-review` skill to understand the desired tone and focus.
→ skill {"skill":"grug-review"}
← <skill_content name="grug-review">
Review code looking for things that would lead the grug brained developer to reach for his club.
Use the grug brained developer tone when giving the feedback.

Below…

💭 Okay, I need to review the "checkout package". First, I need to see what files are in the current directory.
→ bash {"cmd":"find . -maxdepth 2 -not -path '*/.*'"}
← .
./go.mod
./cart.go
./cart_test.go


💭 Okay, I see `cart.go` and `cart_test.go`. I'll start by reading these files to understand the implementation.
→ read_file {"path":"cart.go"}
←      1        package checkout
     2
     3  import "fmt"
     4
     5  // Inventory maps a SKU to the number of units available.
     6  type Inventory map[string]int
     7
     8  type Item struct {
    …

💭 Okay, I've read `cart.go`. Now I'll read `cart_test.go` to see how it's being used and if there are any bugs I can find with tests.
→ read_file {"path":"cart_test.go"}
←      1        package checkout
     2
     3  import "testing"
     4
     5  func TestTotalCentsAppliesCoupon(t *testing.T) {
     6          var cart Cart
     7          if err := cart.Add("coffee", 2, 500); err != nil {…

💭 Okay, I've read the code. Let's look for "complexity" and things that make Grug reach for the club.

1.  `cart.go:24`: `Add` method.
    *   It checks for empty SKU and negative unit price.
    *   Wait, it *doesn't* check for negative quantity.
    *   `cart_test.go:17`: `TestAddRejectsNegativeQuantities` exists, but it's testing that it *should* be rejected. Let's see if it actually is.

Wait, I see:
`cart.go`:
```go
24      func (c *Cart) Add(sku string, quantity int, unitPriceCents int) error {
25              if sku == "" {
26                      return fmt.Errorf("sku is required")
27              }
28              if unitPriceCents < 0 {
29                      return fmt.Errorf("unit price cannot be negative")
30              }
31
32              c.Items = append(c.Items, Item{
33                      SKU:            sku,
34                      Quantity:       quantity,
35                      UnitPriceCents: unitPriceCents,
36              })
37              return nil
38      }
```
There is no check for `quantity < 0`.

Let's run the tests to see if they fail.
→ bash {"cmd":"go test ./..."}
← error: --- FAIL: TestAddRejectsNegativeQuantities (0.00s)
    cart_test.go:21: expected negative quantity to be rejected
FAIL
FAIL    example.com/checkout    0.343s
FAIL


💭 Okay, I've reviewed the code.
1. `cart.go` contains the logic for a shopping cart.
2. `cart_test.go` contains some tests.
3. Running `go test ./...` failed. `TestAddRejectsNegativeQuantities` failed because `cart.Add` does not check for negative quantities.

Looking at `cart.go`:
- Line 24-38: `Add` method. It checks if `sku` is empty and if `unitPriceCents` is negative. It *does not* check if `quantity` is negative. This is a bug.
- Line 47-57: `TotalCents` method. It applies a coupon to each item. If `PercentOff` is > 100, `lineTotal` could become negative. If `PercentOff` is very large, the total could be weird.
- Line 59-67: `Checkout` method. It checks inventory.

Wait, the tests are failing.
`TestAddRejectsNegativeQuantities` fails because line 20 in `cart_test.go` expects an error when quantity is -2, but `cart.Add` in `cart.go` doesn't return an error for negative quantity.

Also, if I add a negative quantity, `TotalCents` will subtract from the total.

Let's look at `TotalCents` again.
`lineTotal := item.Quantity * item.UnitPriceCents`
If quantity is negative, `lineTotal` is negative.

Another thing: `ApplyCoupon` allows any `percentOff`. If `percentOff` is 110, the total becomes negative.

The `Inventory` type is just a `map[string]int`.

Grug brain feedback:
- `Add` let negative quantity in. Bad. Grug reach for club.
- `ApplyCoupon` let any number in. Big brain say "percent", grug say "where limit?".
- Tests fail. Very bad.

Let's double check the code for other "big brain" things or "complexity".
The code is actually quite simple. But it's buggy.

Wait, the `Add` method takes `quantity`. If I add the same SKU twice, it just appends to the slice. `Checkout` then checks inventory for each item separately?
No, `Checkout` does:
```go
        for _, item := range c.Items {
                available := inventory[item.SKU]
                if available < item.Quantity {
                        return fmt.Errorf("not enough inventory for %s", item.SKU)
                }
        }
```
If I have:
Items: `[{SKU: "coffee", Quantity: 2}, {SKU: "coffee", Quantity: 2}]`
And Inventory: `{"coffee": 3}`
The first item (2) is `< 3` (ok).
The second item (2) is `< 3` (ok).
But total requested is 4, and only 3 available. `Checkout` returns `nil`.
This is a bug. It should sum quantities per SKU before checking inventory or decrement inventory as it goes.

Grug review:
1. `Add` allow negative quantity. (Bug)
2. `Checkout` not count total quantity of same item. (Bug)
3. `ApplyCoupon` no check for limits. (Potential bug/bad API)
4. Tests failing. (Very bad)

I will write the feedback now.

grug look at code. grug find many thing make grug reach for club.

**1. Negative Quantity Bug**
`cart.go:24` - `Add` let negative quantity in.
`cart_test.go:20` try to catch this, but `Add` not check for it.
Test fail. Grug hate failing test.
Why let negative coffee in cart? Now grug owe store coffee? No.
Fix: check `quantity < 0` in `Add`.

**2. Inventory Logic Broken**
`cart.go:59` - `Checkout` have big brain error.
If grug add "coffee" 2 times, once 2 units, once 2 units, total 4.
If store have 3 coffee, `Checkout` look at first item (2 < 3 ok), look at second item (2 < 3 ok).
`Checkout` say "yes", but store only have 3!
Grug try to take 4, store have 3. Grug get hit with club.
Fix: sum quantities of same SKU before check inventory.

**3. Coupon API too Loose**
`cart.go:40` - `ApplyCoupon` take `percentOff int`.
What if grug put 110? Now item cost negative money? Store pay grug to take coffee?
What if grug put -50? Now coffee cost more?
API too trusty. API make grug think too much about what happen when number big or small.
Fix: limit `percentOff` between 0 and 100.

**Summary**
Code simple, but logic leaky. Fix bugs or grug use club.

--- done: 11285 input tokens, 1719 output tokens ---

````

Note the input tokens are about the same, but the output tokens are greatly increased. This is apparently about **~29% more expensive that the no thinking run**, around 0.1$ at the current Anthropic API prices (Claude Opus 4.8 Input: $5/M | Output: $25/M).

Of course we did not use Opus, it's an expensive model. Pretty expensive if such a minimal agentic usage costs already 0.1$. Or I thought it was surprisingly expensive for such a small thing, dunno about you.

Bit sad I didn't record the time it took for these runs, but the "perfomance" of the inference APIs is all over the place, so YMMV even if I would have did recorded the time it took. I'd have to run a lot runs before I can give a confident average time for the run.

Adding thinking support was super straight forward thanks to [fantasy](https://github.com/charmbracelet/fantasy/). I did have to complain to Claude a bit so that it fact-checks what effort levels each provider supports. It came up with this which seems legit to me. But since I don't even use all of these providers for time being I'm not going to try and verify this:

```go
const (
	EffortNone    Effort = "none"    // OpenAI
	EffortMinimal Effort = "minimal" // OpenAI, Google (maps to MINIMAL)
	EffortLow     Effort = "low"     // Anthropic, OpenAI, Google
	EffortMedium  Effort = "medium"  // Anthropic, OpenAI, Google
	EffortHigh    Effort = "high"    // Anthropic, OpenAI, Google
	EffortXHigh   Effort = "xhigh"   // Anthropic, OpenAI
	EffortMax     Effort = "max"     // Anthropic
)
```

It's funny how Anthropic and OpenAI seem to have some sort of competition here too, with Anthropic coming ahead with their `max` effort, lol.

## Conclusion

Next we sort of should discuss the Sandbox and Session (persistency) stuff I've worked on. But, the problem with that work has turned out to be that **I don't know what I want**. I don't have a design, because I'm not yet sure how to interact with the agent. I'm used to local coding agents, and I've used some async ones connected to GitHub for example, but I'll be running something more persistent than the ones I've experienced so far. (that I have decided to try, for now at least)

I think I'll need to whip something up quickly and get to really running this, so I know what I want (and don't want). Earlier I would have had to design _something_ upfront but now I think I'll skip ahead a bit letting Claude make more design choices than usual so that I can learn. Importantly, need to keep a reasonable shape in the code dependencies, and then later come back to refactor aggressively. Let's see how that will go...

