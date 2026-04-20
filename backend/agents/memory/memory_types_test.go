package memory

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// M5.T1 · Enum + parser.
// ---------------------------------------------------------------------------

func TestParseMemoryContentType(t *testing.T) {
	cases := []struct {
		in       interface{}
		wantType MemoryContentType
		wantOk   bool
	}{
		{"user", MemoryContentTypeUser, true},
		{"feedback", MemoryContentTypeFeedback, true},
		{"project", MemoryContentTypeProject, true},
		{"reference", MemoryContentTypeReference, true},
		{"unknown", "", false},
		{"", "", false},
		{nil, "", false},
		{42, "", false},
		{[]string{"user"}, "", false},
	}
	for _, tc := range cases {
		got, ok := ParseMemoryContentType(tc.in)
		if got != tc.wantType || ok != tc.wantOk {
			t.Errorf("ParseMemoryContentType(%#v) = (%q, %v); want (%q, %v)",
				tc.in, got, ok, tc.wantType, tc.wantOk)
		}
	}
}

func TestMemoryContentTypesOrder(t *testing.T) {
	want := []MemoryContentType{
		MemoryContentTypeUser,
		MemoryContentTypeFeedback,
		MemoryContentTypeProject,
		MemoryContentTypeReference,
	}
	if len(MemoryContentTypes) != len(want) {
		t.Fatalf("len=%d; want %d", len(MemoryContentTypes), len(want))
	}
	for i, v := range want {
		if MemoryContentTypes[i] != v {
			t.Errorf("[%d] = %q; want %q", i, MemoryContentTypes[i], v)
		}
	}
}

func TestFormatMemoryContentTypes(t *testing.T) {
	got := FormatMemoryContentTypes()
	want := "user, feedback, project, reference"
	if got != want {
		t.Errorf("got %q; want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// M5.T2 · Prompt section smoke checks.
// ---------------------------------------------------------------------------

func TestTypesSections_MentionEveryType(t *testing.T) {
	for _, sec := range [][]string{TypesSectionCombined, TypesSectionIndividual} {
		joined := strings.Join(sec, "\n")
		for _, tp := range MemoryContentTypes {
			needle := "<name>" + string(tp) + "</name>"
			if !strings.Contains(joined, needle) {
				t.Errorf("section missing %q", needle)
			}
		}
	}
}

func TestTypesSectionCombined_HasScopeTags(t *testing.T) {
	// Combined variant MUST include <scope> tags; individual must NOT.
	joinedCombined := strings.Join(TypesSectionCombined, "\n")
	joinedIndividual := strings.Join(TypesSectionIndividual, "\n")
	if !strings.Contains(joinedCombined, "<scope>") {
		t.Error("TypesSectionCombined should contain <scope> tags")
	}
	if strings.Contains(joinedIndividual, "<scope>") {
		t.Error("TypesSectionIndividual must NOT contain <scope> tags")
	}
}

func TestWhatNotToSaveSection_HasHeader(t *testing.T) {
	if len(WhatNotToSaveSection) == 0 ||
		WhatNotToSaveSection[0] != "## What NOT to save in memory" {
		t.Errorf("unexpected header: %q", WhatNotToSaveSection[0])
	}
}

func TestWhenToAccessSection_ContainsDriftCaveat(t *testing.T) {
	joined := strings.Join(WhenToAccessSection, "\n")
	if !strings.Contains(joined, MemoryDriftCaveat) {
		t.Error("WhenToAccessSection missing MemoryDriftCaveat")
	}
}

func TestTrustingRecallSection_HasHeader(t *testing.T) {
	if TrustingRecallSection[0] != "## Before recommending from memory" {
		t.Errorf("header wrong: %q", TrustingRecallSection[0])
	}
}

func TestMemoryFrontmatterExample_MentionsAllTypes(t *testing.T) {
	joined := strings.Join(MemoryFrontmatterExample, "\n")
	for _, tp := range MemoryContentTypes {
		if !strings.Contains(joined, string(tp)) {
			t.Errorf("frontmatter example missing type %q", tp)
		}
	}
	if !strings.Contains(joined, "```markdown") {
		t.Error("frontmatter example must open with markdown fence")
	}
}
