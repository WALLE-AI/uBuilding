package memory

import (
	"encoding/json"
	"testing"
)

func TestMemoryType_StringAndIsValid(t *testing.T) {
	cases := []struct {
		in    MemoryType
		str   string
		valid bool
	}{
		{MemoryTypeManaged, "Managed", true},
		{MemoryTypeUser, "User", true},
		{MemoryTypeProject, "Project", true},
		{MemoryTypeLocal, "Local", true},
		{MemoryTypeAutoMem, "AutoMem", true},
		{MemoryTypeTeamMem, "TeamMem", true},
		{MemoryType(""), "", false},
		{MemoryType("Bogus"), "Bogus", false},
	}
	for _, tc := range cases {
		if got := tc.in.String(); got != tc.str {
			t.Errorf("String(%q) = %q; want %q", tc.in, got, tc.str)
		}
		if got := tc.in.IsValid(); got != tc.valid {
			t.Errorf("IsValid(%q) = %v; want %v", tc.in, got, tc.valid)
		}
	}
}

func TestParseMemoryType_CaseInsensitive(t *testing.T) {
	cases := []struct {
		raw  string
		want MemoryType
		ok   bool
	}{
		{"Managed", MemoryTypeManaged, true},
		{"managed", MemoryTypeManaged, true},
		{"  MANAGED  ", MemoryTypeManaged, true},
		{"User", MemoryTypeUser, true},
		{"project", MemoryTypeProject, true},
		{"AutoMem", MemoryTypeAutoMem, true},
		{"TeamMem", MemoryTypeTeamMem, true},
		{"", "", false},
		{"Bogus", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseMemoryType(tc.raw)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseMemoryType(%q) = (%q, %v); want (%q, %v)",
				tc.raw, got, ok, tc.want, tc.ok)
		}
	}
}

func TestMemoryType_JSONRoundTrip(t *testing.T) {
	for _, in := range AllMemoryTypes {
		data, err := json.Marshal(in)
		if err != nil {
			t.Fatalf("marshal %q: %v", in, err)
		}
		var out MemoryType
		if err := json.Unmarshal(data, &out); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}
		if out != in {
			t.Errorf("round-trip %q => %q", in, out)
		}
	}

	// Empty marshals to null and unmarshals back to "".
	data, err := json.Marshal(MemoryType(""))
	if err != nil {
		t.Fatalf("marshal empty: %v", err)
	}
	if string(data) != "null" {
		t.Errorf("empty marshal = %s; want null", data)
	}
	var out MemoryType
	if err := json.Unmarshal([]byte(`null`), &out); err != nil {
		t.Fatalf("unmarshal null: %v", err)
	}
	if out != "" {
		t.Errorf("null unmarshal = %q; want empty", out)
	}

	// Unknown values are rejected.
	var bogus MemoryType
	if err := json.Unmarshal([]byte(`"bogus"`), &bogus); err == nil {
		t.Error("expected error unmarshalling unknown MemoryType")
	}
}

func TestMemoryFileInfo_ContentDiffersFlag(t *testing.T) {
	info := MemoryFileInfo{
		Path:                   "/tmp/CLAUDE.md",
		Type:                   MemoryTypeProject,
		RawContent:             "a\n<!-- x -->\nb",
		Content:                "a\nb",
		ContentDiffersFromDisk: true,
	}
	data, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Round-trip to ensure the struct shape is stable.
	var out MemoryFileInfo
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Path != info.Path || out.Type != info.Type ||
		out.Content != info.Content || !out.ContentDiffersFromDisk {
		t.Errorf("round-trip mismatch: %+v", out)
	}
}
