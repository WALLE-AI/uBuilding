package permission

import (
	"reflect"
	"testing"
)

func TestParseRuleValue(t *testing.T) {
	cases := []struct {
		in       string
		wantTool string
		wantPat  string
		wantArgs bool
	}{
		{"Bash", "Bash", "", false},
		{"  Bash  ", "Bash", "", false},
		{"Bash(git *)", "Bash", "git *", true},
		{"Agent(worker, researcher)", "Agent", "worker, researcher", true},
		{"Agent()", "Agent", "", true},
		{"   ", "", "", false},
		// Unbalanced: keep verbatim.
		{"Bash(oops", "Bash(oops", "", false},
		{"(oops)", "(oops)", "", false},
	}
	for _, tc := range cases {
		got := ParseRuleValue(tc.in)
		if got.Tool != tc.wantTool || got.Pattern != tc.wantPat || got.HasArgs != tc.wantArgs {
			t.Errorf("ParseRuleValue(%q) = %+v; want {%q %q %v}", tc.in, got, tc.wantTool, tc.wantPat, tc.wantArgs)
		}
	}
}

func TestParseCommaList(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"alpha", []string{"alpha"}},
		{"alpha, beta,gamma", []string{"alpha", "beta", "gamma"}},
		{" , , ", nil},
		{"one,,two", []string{"one", "two"}},
	}
	for _, tc := range cases {
		got := ParseCommaList(tc.in)
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseCommaList(%q) = %v; want %v", tc.in, got, tc.want)
		}
	}
}
