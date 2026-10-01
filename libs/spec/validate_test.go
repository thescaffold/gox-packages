package spec

import (
	"fmt"
	"strings"
	"testing"
)

func codesOf(diags []Diagnostic) string {
	var out []string
	for _, d := range diags {
		out = append(out, d.Code)
	}
	return strings.Join(out, ",")
}

func TestTheReferenceExampleIsClean(t *testing.T) {
	if diags := Validate(grocery(t)); len(diags) != 0 {
		t.Fatalf("%v", diags)
	}
}

func TestValidateSeparatesErrorsFromWarnings(t *testing.T) {
	d, _ := Parse(`ospec: 1
# T

## Features

### Orders {#orders}
Needs [[Nothing]] and [[#nope]] and [[Twin]].

### Twin {#twin}
done when:
- [ ] works {#c-1}

### Twin {#twin-2}
done when:
- [ ] works {#c-1}

## Questions
? Which colour? {#q-colour}
`)
	diags := Validate(d)
	if !HasErrors(diags) {
		t.Fatal("a repeated id is an error")
	}
	if diags[0].Severity != Error || diags[0].Code != "dup-id" || !strings.Contains(diags[0].Message, "“c-1”") {
		t.Fatalf("errors come first: %v", diags[0])
	}
	got := codesOf(diags)
	for _, want := range []string{"unresolved-ref", "ambiguous-ref", "question-no-choices", "feature-no-checks"} {
		if !strings.Contains(got, want) {
			t.Errorf("no %s in %s", want, got)
		}
	}
	for _, dg := range diags {
		if dg.Code == "unresolved-ref" && dg.Line != 7 {
			t.Errorf("the reference is on line 7, not %d", dg.Line)
		}
		if dg.Code == "feature-no-checks" && !strings.Contains(dg.Message, "Orders") {
			t.Errorf("name the feature: %s", dg.Message)
		}
	}
	// without the repeated id, nothing blocks a commit
	d2, _ := Parse("ospec: 1\n# T\n## Features\n### A {#a}\ndone when:\n- [ ] x {#c-1}\n")
	if HasErrors(Validate(d2)) {
		t.Fatalf("%v", Validate(d2))
	}
}

func TestParseTreatsUnterminatedBlocksAsErrors(t *testing.T) {
	_, diags := Parse("# T\n## Notes\n<!-- never closed\n")
	if !HasErrors(diags) {
		t.Fatalf("an unterminated comment blocks a commit: %v", diags)
	}
	_, diags = Parse("# T\n## Notes\n```\ncode\n")
	if !HasErrors(diags) {
		t.Fatalf("an unterminated code block blocks a commit: %v", diags)
	}
}

func TestSecretsAreSpottedAndNamesOfSettingsAreNot(t *testing.T) {
	secrets := []string{
		"The key is sk-ant-abcdefghijklmnopqrstuvwx for now.",
		"token: ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"- password: hunter2abc",
		"api key = Zx9qLm3vTb8wRp2a",
		"aws AKIAIOSFODNN7EXAMPLE",
		"Authorization eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N",
		"-----BEGIN RSA PRIVATE KEY-----",
		"STRIPE sk_live_abcdefghijklmnopqrstuv",
	}
	for _, s := range secrets {
		if diags := ScanSecrets(s); len(diags) != 1 || diags[0].Code != "secret" || diags[0].Severity != Warning {
			t.Errorf("%q: %v", s, diags)
		}
	}
	fine := []string{
		"needs keys: PAYSTACK_SECRET, PAYSTACK_WEBHOOK_SECRET, SMTP_HOST",
		"password: required",
		"Passwords must be at least 12 characters.",
		"token: [[Session]]",
		"The secret to a good order is speed.",
		"sk-short",
		"We use a JWT for sign-in.",
		"api key: PAYSTACK_KEY",
		"token: STRIPE_SECRET_KEY_V2",
		"visit https://example.com/a-long-path/that-is-just-a-url",
	}
	for _, s := range fine {
		if diags := ScanSecrets(s); len(diags) != 0 {
			t.Errorf("%q was flagged: %v", s, diags)
		}
	}
	diags := ScanSecrets("fine\nthe key sk-abcdefghijklmnopqrstuvwx\n")
	if len(diags) != 1 || diags[0].Line != 2 || diags[0].Col != 9 {
		t.Fatalf("%v", diags)
	}
	if strings.Contains(diags[0].Message, "abcdefghij") {
		t.Fatalf("the message must not repeat the secret: %s", diags[0].Message)
	}
	// Validate scans the whole document, header included
	d, _ := Parse("ospec: 1\n# T\n> use sk-abcdefghijklmnopqrstuvwx\n\n## Notes\nok\n")
	if !HasSecret(Validate(d)) {
		t.Fatal("Validate did not scan the summary")
	}
}

func TestOutlineIsTheCompactView(t *testing.T) {
	d := grocery(t)
	out := Outline(d)
	if len(out) != 23 {
		t.Fatalf("%d lines, want one per addressable thing", len(out))
	}
	text := OutlineText(d)
	for _, want := range []string{
		"Grocery Restocking\n",
		"\nrecurring-orders · feature · Recurring orders · decided\n",
		"\n  c-3f2a · criterion · A household sets Milk to repeat weekly and an order appears the next week. · decided\n",
		"\nq-auto-charge · question · Should recurring purchases charge automatically? · answered\n",
		"\na-delivery-days · assumption · Deliveries happen Monday to Saturday. (assumed by Origine) · assumed\n",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	open := mustApply(t, d, PatchOp{Op: "answer", Item: "q-auto-charge", Answer: sp("")})
	if !strings.Contains(OutlineText(open), "· open\n") {
		t.Error("an unanswered question is open")
	}
	if got := clip(strings.Repeat("word ", 40), 20); len([]rune(got)) != 20 || !strings.HasSuffix(got, "…") {
		t.Errorf("clip: %q", got)
	}
	_ = fmt.Sprint
}
