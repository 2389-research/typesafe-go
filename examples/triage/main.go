// ABOUTME: A runnable support-ticket triage example for the TypeSafe Go SDK.
// ABOUTME: Needs TYPESAFE_API_KEY; run it with: go run ./examples/triage

package main

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"time"

	"github.com/2389-research/typesafe-go"
)

// department is the caller's own option type. Declaring it means the compiler
// checks every comparison against an answer, and the SDK hands back these
// values rather than bare strings.
type department string

const (
	billing   department = "billing"
	technical department = "technical"
	sales     department = "sales"
)

const ticket = "Help! My payouts have been failing for 3 days. " +
	"I have emailed twice and nobody has replied. This is costing us money."

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "triage:", err)
		os.Exit(1)
	}
}

func run() error {
	client, err := typesafe.New()
	if errors.Is(err, typesafe.ErrNoAPIKey) {
		return errors.New("set TYPESAFE_API_KEY; keys live at https://console.typesafe.ai/keys")
	}
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Each question is a value you keep. It carries its own id and answer
	// type, so no key string is repeated below.
	urgent := typesafe.Noul("is_urgent", "Does this convey urgency?").
		WithCriteria("Explicitly time-sensitive", "No urgency expressed")

	team := typesafe.Choice[department]("department", "Which team should handle this?",
		typesafe.Opts[department]{
			billing:   "Payments, invoicing, refunds",
			technical: "Bugs, outages, integrations",
			sales:     "Pricing, upgrades, new accounts",
		})

	anger := typesafe.Score("frustration", "How frustrated is the customer?",
		"Calm", "Frustrated", "Very angry")

	// One request, three judgments. They are answered independently, so none
	// can see another's answer.
	res, err := client.Ask(ctx, ticket, urgent, team, anger)
	if err != nil {
		return err
	}

	urgency, err := urgent.From(res)
	if err != nil {
		return err
	}
	assignment, err := team.From(res)
	if err != nil {
		return err
	}
	frustration, err := anger.From(res)
	if err != nil {
		return err
	}

	fmt.Printf("model:       %s\n", res.Model)
	fmt.Printf("urgent:      %.2f\n", urgency)
	fmt.Printf("department:  %s (confidence %.2f)\n", assignment.Value, assignment.Confidence)
	for _, option := range slices.Sorted(maps.Keys(assignment.Probabilities)) {
		fmt.Printf("             %-10s %.2f\n", option, assignment.Probabilities[option])
	}
	fmt.Printf("frustration: %.2f of %d\n", frustration.Value, len(frustration.Legend)-1)
	for _, level := range slices.Sorted(maps.Keys(frustration.Legend)) {
		fmt.Printf("             %d %-12s %.2f\n", level, frustration.Legend[level], frustration.Probabilities[level])
	}
	fmt.Printf("input tokens: %d\n", res.Usage.InputTokens)

	// Route on the probabilities. Collapsing them to yes/no first would
	// throw away the part you paid for.
	switch {
	case urgency > 0.8 && frustration.Value > 1.5:
		fmt.Println("\nroute: page the on-call for", assignment.Value)
	case urgency > 0.5:
		fmt.Println("\nroute: same-day queue for", assignment.Value)
	default:
		fmt.Println("\nroute: normal queue for", assignment.Value)
	}
	return nil
}
