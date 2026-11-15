package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sample = `quizzes:
  - id: demo
    title: Demo
    questions:
      - id: q1
        type: single_choice
        text: Pick one
        duration_seconds: 20
        points: 1000
        options:
          - id: A
            text: First
          - id: B
            text: Second
        correct_answer: A
`

func TestValidationAndAtomicReload(t *testing.T) {
	for name, input := range map[string]string{
		"unknown":            strings.Replace(sample, "points: 1000", "unexpected: secret-answer", 1),
		"wrong answer":       strings.Replace(sample, "correct_answer: A", "correct_answer: C", 1),
		"duplicate option":   strings.Replace(sample, "id: B", "id: A", 1),
		"negative duration":  strings.Replace(sample, "duration_seconds: 20", "duration_seconds: -1", 1),
		"multiple documents": sample + "\n---\nquizzes: []",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(input)); err == nil {
				t.Fatal("invalid configuration accepted")
			} else if strings.Contains(err.Error(), "secret-answer") {
				t.Fatal("configuration value leaked")
			}
		})
	}
	path := filepath.Join(t.TempDir(), "quizzes.yaml")
	if err := os.WriteFile(path, []byte(sample), 0600); err != nil {
		t.Fatal(err)
	}
	m := &Manager{}
	if err := m.Reload(path, "first"); err != nil {
		t.Fatal(err)
	}
	before, _, _, _ := m.Get()
	if err := os.WriteFile(path, []byte("quizzes: []"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.Reload(path, "second"); err == nil {
		t.Fatal("invalid reload accepted")
	}
	after, status, _, _ := m.Get()
	if before != after || status != "reload_rejected" {
		t.Fatal("last known-good catalog was replaced")
	}
}
func TestVersionPreservesAuthoredOrder(t *testing.T) {
	a, err := Parse([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Parse([]byte("# comment\n" + sample))
	if err != nil {
		t.Fatal(err)
	}
	if a.Version != b.Version {
		t.Fatal("comments changed semantic version")
	}
	c, err := Parse([]byte(strings.Replace(sample, "text: Second", "text: Changed", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if a.Version == c.Version {
		t.Fatal("content change did not change version")
	}
}
