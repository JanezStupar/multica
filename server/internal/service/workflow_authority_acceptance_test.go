package service

import "testing"

func TestWorkflowRepositoryURLMatchesBase(t *testing.T) {
	base := "https://git.example/team/project"
	for _, tc := range []struct {
		name      string
		candidate string
		want      bool
	}{
		{name: "provider web URL", candidate: base, want: true},
		{name: "git clone URL", candidate: base + ".git", want: true},
		{name: "unrelated suffix", candidate: base + "/wiki", want: false},
		{name: "malicious suffix", candidate: base + ".git.evil", want: false},
		{name: "lookalike host", candidate: "https://git.example.evil/team/project", want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := workflowRepositoryURLMatchesBase(base, tc.candidate); got != tc.want {
				t.Fatalf("workflowRepositoryURLMatchesBase(%q, %q) = %t, want %t", base, tc.candidate, got, tc.want)
			}
		})
	}
}
