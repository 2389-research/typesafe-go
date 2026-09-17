// ABOUTME: Package documentation for the TypeSafe Go SDK.
// ABOUTME: Kept in its own file so the overview does not crowd the client code.

// Package typesafe is a client for the TypeSafe System One API.
//
// System One answers questions about content with a typed judgment and a
// calibrated probability instead of generating text you then have to parse.
// Ask three questions about a support ticket and you get back a number, an
// option, and a rating. No JSON mode, no retry loop around malformed output.
//
// # Getting started
//
//	client, err := typesafe.New() // reads TYPESAFE_API_KEY
//	if err != nil {
//		return err
//	}
//
//	urgent := typesafe.Noul("is_urgent", "Does this convey urgency?")
//
//	res, err := client.Ask(ctx, ticket, urgent)
//	if err != nil {
//		return err
//	}
//
//	p, err := urgent.From(res) // 0.92
//
// # Question handles
//
// A question is a value you keep. It carries its own id and its own answer
// type, so you read an answer back through the question that asked it. The id
// string appears exactly once in your code, and a Score handle cannot read a
// Noul answer, because From returns a different type for each.
//
// # The three primitives
//
// Noul asks yes or no and returns the probability of yes. It has no
// confidence field: 0.5 means the model is genuinely torn, which is an
// answer, not a failure.
//
// Choice picks one option from a set you define and returns the whole
// distribution alongside the winner. Declare the option set as your own
// string type and the compiler checks every comparison you write against it.
//
// Score rates content against ordered levels and returns a
// probability-weighted position that can land between them. On a
// calm/frustrated/angry scale, 1.6 means mostly angry.
//
// # Probabilities are the point
//
// Route on the number. A 0.95 and a 0.51 both become "yes" after a threshold,
// and collapsing them throws away what you paid for.
//
// # Configuration
//
// New reads TYPESAFE_API_KEY, TYPESAFE_BASE_URL, and TYPESAFE_DEFAULT_MODEL,
// and an option overrides any of them. Transient failures retry on their own:
// two retries, 500ms doubling to 5s, honoring Retry-After, inside a
// 30-second budget. See RetryPolicy to change that.
//
// # Errors
//
// Match failures with errors.Is against ErrUnauthorized, ErrUnprocessable,
// ErrRateLimited, ErrOverloaded, and ErrServer. For the raw response, use
// errors.As to reach *Error, which keeps the status code and the body.
package typesafe
