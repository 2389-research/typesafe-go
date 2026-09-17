# typesafe-go

A Go client for the [TypeSafe](https://typesafe.ai) System One API. Zero
dependencies outside the standard library.

System One answers questions about content with a typed judgment and a
probability instead of generating text you then have to parse.

```bash
go get github.com/2389-research/typesafe-go
```

## Quick start

```go
package main

import (
	"context"
	"fmt"

	"github.com/2389-research/typesafe-go"
)

type department string

const (
	billing   department = "billing"
	technical department = "technical"
	sales     department = "sales"
)

func main() {
	client, err := typesafe.New() // reads TYPESAFE_API_KEY
	if err != nil {
		panic(err)
	}

	ticket := "Help! My payouts have been failing for 3 days."

	urgent := typesafe.Noul("is_urgent", "Does this convey urgency?")
	team := typesafe.Choice[department]("department", "Which team should handle this?",
		typesafe.Opts[department]{
			billing:   "Payments, invoicing, refunds",
			technical: "Bugs, outages, integrations",
			sales:     "Pricing, upgrades, new accounts",
		})

	res, err := client.Ask(context.Background(), ticket, urgent, team)
	if err != nil {
		panic(err)
	}

	p, _ := urgent.From(res)        // 0.92
	assignment, _ := team.From(res) // Value: technical, Confidence: 0.82

	fmt.Println(p, assignment.Value, assignment.Probabilities)
}
```

Run the full example: `go run ./examples/triage`

## Question handles

A question is a value you keep. It carries its own id and its own answer
type, so you read the answer back through the question that asked it:

```go
urgent := typesafe.Noul("is_urgent", "Does this convey urgency?")
res, _ := client.Ask(ctx, ticket, urgent)
p, err := urgent.From(res)
```

The id appears once. A `Score` handle cannot read a `Noul` answer, because
`From` returns a different type for each — the mistake is a compile error,
not a zero value at runtime.

## The three primitives

| Primitive | Question | Answer |
| --- | --- | --- |
| `Noul` | yes or no | `float64` — the probability of yes |
| `Choice` | one option from a set you define | `ChoiceAnswer[T]` — winner, full distribution, confidence |
| `Score` | a position on ordered levels | `ScoreAnswer` — weighted value, legend, distribution, confidence |

A `Noul` has no confidence field. Its probability *is* the answer: 0.5 means
the model is genuinely torn.

Instructions and descriptions take a string, or a map or slice when a
sentence is not enough:

```go
typesafe.Noul("refund", map[string]any{
	"question": "Is this a refund request?",
	"exclude":  []any{"chargebacks", "partial credits"},
})
```

## Probabilities are the point

```go
switch {
case urgency > 0.8 && frustration.Value > 1.5:
	page(onCall)
case urgency > 0.5:
	queue(sameDay)
default:
	queue(normal)
}
```

A 0.95 and a 0.51 both become "yes" after a threshold. Route on the number.

## Configuration

```go
client, err := typesafe.New(
	typesafe.WithAPIKey(key),
	typesafe.WithModel("jev-1.13.0"),
	typesafe.WithTimeout(20*time.Second),
	typesafe.WithRetry(policy),
)
```

| Variable | Default | Option |
| --- | --- | --- |
| `TYPESAFE_API_KEY` | none — required | `WithAPIKey` |
| `TYPESAFE_BASE_URL` | `https://api.typesafe.ai` | `WithBaseURL` |
| `TYPESAFE_DEFAULT_MODEL` | `jev-latest` | `WithModel` |

Also `WithHTTPClient` for your own transport.

Transient failures retry without you asking: two retries, 500ms doubling to
5s with 25% jitter, honoring `Retry-After`, inside a 30-second budget.
`DefaultRetryPolicy()` returns that; `WithRetry` replaces it.

## Errors

```go
res, err := client.Ask(ctx, ticket, urgent)
switch {
case errors.Is(err, typesafe.ErrRateLimited):
	// already retried; you are over your limit
case errors.Is(err, typesafe.ErrUnprocessable):
	// the request was malformed
case err != nil:
	var apiErr *typesafe.Error
	if errors.As(err, &apiErr) {
		log.Printf("status %d: %s", apiErr.StatusCode, apiErr.Body)
	}
}
```

`Error` keeps the status code and the raw body. TypeSafe does not document
the shape of its error bodies, so the SDK preserves them rather than guessing.

## Keep the key on the server

The API key authenticates your account and carries your billing. It belongs
in a server-side process, never in a browser bundle, a mobile app, or
anything a user can read.

## Tests

```bash
./scripts/check                              # fmt, vet, lint, race tests
TYPESAFE_API_KEY=sk-... ./scripts/check      # adds live API tests
```

Live tests are behind the `e2e` build tag and skip without a key. They cost
roughly a thousandth of a cent per call.

