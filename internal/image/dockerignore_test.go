package image

import "testing"

func TestDockerignoreMatcherUsesOrderedPatternsAndNegation(t *testing.T) {
	matcher, err := parseDockerignore(`
# comment
*.txt
!keep.txt
private/**
!private/public/*.txt
tmp/**/generated?.log
`)
	if err != nil {
		t.Fatalf("parseDockerignore() error = %v", err)
	}

	tests := []struct {
		path     string
		excluded bool
	}{
		{path: "notes.txt", excluded: true},
		{path: "docs/notes.txt", excluded: true},
		{path: "keep.txt", excluded: false},
		{path: "docs/keep.txt", excluded: false},
		{path: "private/secret.bin", excluded: true},
		{path: "private/public/readme.txt", excluded: false},
		{path: "private/public/readme.bin", excluded: true},
		{path: "tmp/generated1.log", excluded: true},
		{path: "tmp/a/b/generated2.log", excluded: true},
		{path: "tmp/a/b/generated22.log", excluded: false},
		{path: "src/main.py", excluded: false},
	}
	for _, test := range tests {
		t.Run(test.path, func(t *testing.T) {
			if got := matcher.excludes(test.path); got != test.excluded {
				t.Fatalf("excludes(%q) = %t, want %t", test.path, got, test.excluded)
			}
		})
	}
}

func TestDockerignoreLastMatchingPatternWins(t *testing.T) {
	matcher, err := parseDockerignore("*.pem\n!public.pem\npublic.pem\n")
	if err != nil {
		t.Fatalf("parseDockerignore() error = %v", err)
	}
	if !matcher.excludes("public.pem") {
		t.Fatal("final matching exclusion should override earlier negation")
	}
}

func TestDockerignoreEscapedCommentAndNegationPrefixes(t *testing.T) {
	matcher, err := parseDockerignore("\\#literal\n\\!literal\n")
	if err != nil {
		t.Fatalf("parseDockerignore() error = %v", err)
	}
	if !matcher.excludes("#literal") {
		t.Fatal("escaped comment prefix should be treated as a pattern")
	}
	if !matcher.excludes("!literal") {
		t.Fatal("escaped negation prefix should be treated as a pattern")
	}
}
