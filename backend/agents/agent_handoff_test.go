package agents

import (
	"context"
	"errors"
	"testing"
)

func TestClassifyHandoff_DefaultPassThrough(t *testing.T) {
	RegisterHandoffClassifier(nil) // ensure clean state
	got, reason, err := ClassifyHandoffIfNeeded(context.Background(), HandoffRequest{
		FromAgentID: "a1", Message: "hello",
	})
	if err != nil || got != HandoffDecisionPass || reason != "" {
		t.Fatalf("default = (%q, %q, %v)", got, reason, err)
	}
	if HasHandoffClassifier() {
		t.Fatal("no classifier should be registered initially")
	}
}

func TestClassifyHandoff_CustomClassifierApplied(t *testing.T) {
	prev := RegisterHandoffClassifier(func(_ context.Context, req HandoffRequest) (HandoffDecision, string, error) {
		if req.Message == "boom" {
			return HandoffDecisionSecurityBlock, "contains forbidden token", nil
		}
		return HandoffDecisionWarning, "default warn", nil
	})
	t.Cleanup(func() { RegisterHandoffClassifier(prev) })

	if !HasHandoffClassifier() {
		t.Fatal("classifier should now be registered")
	}

	got, reason, err := ClassifyHandoffIfNeeded(context.Background(), HandoffRequest{Message: "safe"})
	if err != nil || got != HandoffDecisionWarning || reason != "default warn" {
		t.Fatalf("warn path = (%q, %q, %v)", got, reason, err)
	}
	got, reason, err = ClassifyHandoffIfNeeded(context.Background(), HandoffRequest{Message: "boom"})
	if err != nil || got != HandoffDecisionSecurityBlock || reason != "contains forbidden token" {
		t.Fatalf("block path = (%q, %q, %v)", got, reason, err)
	}
}

func TestClassifyHandoff_ClassifierErrorPropagates(t *testing.T) {
	prev := RegisterHandoffClassifier(func(context.Context, HandoffRequest) (HandoffDecision, string, error) {
		return HandoffDecisionPass, "", errors.New("policy server down")
	})
	t.Cleanup(func() { RegisterHandoffClassifier(prev) })

	_, _, err := ClassifyHandoffIfNeeded(context.Background(), HandoffRequest{})
	if err == nil || err.Error() != "policy server down" {
		t.Fatalf("expected classifier error, got %v", err)
	}
}

func TestRegisterHandoffClassifier_RestoresPrevious(t *testing.T) {
	RegisterHandoffClassifier(nil)
	first := HandoffClassifier(func(context.Context, HandoffRequest) (HandoffDecision, string, error) {
		return HandoffDecisionWarning, "one", nil
	})
	RegisterHandoffClassifier(first)
	prev := RegisterHandoffClassifier(nil)
	if prev == nil {
		t.Fatal("RegisterHandoffClassifier should return the previously installed fn")
	}
	if !HasHandoffClassifier() {
		// We just set to nil, so should NOT be registered.
	} else {
		t.Fatal("after nil register, HasHandoffClassifier should be false")
	}
	// Restore `first` via the returned pointer.
	RegisterHandoffClassifier(prev)
	got, reason, _ := ClassifyHandoffIfNeeded(context.Background(), HandoffRequest{})
	if got != HandoffDecisionWarning || reason != "one" {
		t.Fatalf("restored classifier not active: (%q, %q)", got, reason)
	}
	RegisterHandoffClassifier(nil)
}
