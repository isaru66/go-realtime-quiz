package config

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type Option struct {
	ID   string `json:"id" yaml:"id"`
	Text string `json:"text" yaml:"text"`
}

type Question struct {
	ID       string   `json:"id" yaml:"id"`
	Type     string   `json:"type" yaml:"type"`
	Text     string   `json:"text" yaml:"text"`
	Duration int64    `json:"duration_seconds" yaml:"duration_seconds"`
	Points   int64    `json:"points" yaml:"points"`
	Options  []Option `json:"options" yaml:"options"`
	Correct  string   `json:"correct_answer" yaml:"correct_answer"`
}

type Quiz struct {
	ID          string     `json:"id" yaml:"id"`
	Title       string     `json:"title" yaml:"title"`
	Description string     `json:"description" yaml:"description"`
	Questions   []Question `json:"questions" yaml:"questions"`
}

type Catalog struct {
	Quizzes []Quiz `json:"quizzes" yaml:"quizzes"`
	Version string `json:"-" yaml:"-"`
}

type Metadata struct {
	ID            string `json:"id"`
	Title         string `json:"title"`
	Description   string `json:"description"`
	QuestionCount int    `json:"question_count"`
}

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func Parse(data []byte) (*Catalog, error) {
	if len(data) > 1024*1024 {
		return nil, fmt.Errorf("catalog exceeds 1 MiB")
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		// Parser errors can contain YAML values, including configured answers.
		return nil, fmt.Errorf("invalid YAML syntax or unknown configuration fields")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("exactly one YAML document is required")
	}
	if len(catalog.Quizzes) == 0 || len(catalog.Quizzes) > 100 {
		return nil, fmt.Errorf("catalog must contain 1-100 quizzes")
	}
	quizzes := map[string]bool{}
	for qi, quiz := range catalog.Quizzes {
		if !identifier.MatchString(quiz.ID) || quizzes[quiz.ID] || !text(quiz.Title, 200) || utf8.RuneCountInString(quiz.Description) > 2000 {
			return nil, fmt.Errorf("quiz %d: invalid or duplicate ID or metadata", qi)
		}
		quizzes[quiz.ID] = true
		if len(quiz.Questions) == 0 || len(quiz.Questions) > 100 {
			return nil, fmt.Errorf("quiz %d: requires 1-100 questions", qi)
		}
		questions := map[string]bool{}
		for i, q := range quiz.Questions {
			if !identifier.MatchString(q.ID) || questions[q.ID] || q.Type != "single_choice" || !text(q.Text, 2000) {
				return nil, fmt.Errorf("quiz %d question %d: invalid ID, type, or text", qi, i)
			}
			questions[q.ID] = true
			if q.Duration < 1 || q.Duration > 3600 || q.Points < 1 || q.Points > 90071992547409 || len(q.Options) < 2 || len(q.Options) > 8 {
				return nil, fmt.Errorf("quiz %d question %d: invalid duration, points, or option count", qi, i)
			}
			options := map[string]bool{}
			for _, o := range q.Options {
				if !identifier.MatchString(o.ID) || options[o.ID] || !text(o.Text, 500) {
					return nil, fmt.Errorf("quiz %d question %d: invalid or duplicate option", qi, i)
				}
				options[o.ID] = true
			}
			if !options[q.Correct] {
				return nil, fmt.Errorf("quiz %d question %d: correct answer must reference an option", qi, i)
			}
		}
		encoded, _ := json.Marshal(quiz)
		if len(encoded) > 256*1024 {
			return nil, fmt.Errorf("quiz %d exceeds 256 KiB", qi)
		}
	}
	// Round-trip to generic objects so encoding/json sorts object keys recursively.
	raw, err := json.Marshal(catalog)
	if err != nil {
		return nil, err
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, err
	}
	var canonical bytes.Buffer
	encoder := json.NewEncoder(&canonical)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(normalized); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(bytes.TrimSuffix(canonical.Bytes(), []byte("\n")))
	catalog.Version = hex.EncodeToString(sum[:])
	return &catalog, nil
}

func text(value string, max int) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max
}

func (c *Catalog) Metadata() []Metadata {
	result := make([]Metadata, 0, len(c.Quizzes))
	for _, q := range c.Quizzes {
		result = append(result, Metadata{q.ID, q.Title, q.Description, len(q.Questions)})
	}
	return result
}

type Manager struct {
	mu      sync.RWMutex
	catalog *Catalog
	status  string
	at      string
	errors  []string
}

func (m *Manager) Reload(path, at string) error {
	data, err := os.ReadFile(path)
	if err == nil {
		var catalog *Catalog
		catalog, err = Parse(data)
		if err == nil {
			m.mu.Lock()
			m.catalog, m.status, m.at, m.errors = catalog, "valid", at, []string{}
			m.mu.Unlock()
			return nil
		}
	} else {
		err = fmt.Errorf("cannot read mounted quiz configuration")
	}
	m.mu.Lock()
	m.status, m.at, m.errors = "reload_rejected", at, []string{err.Error()}
	m.mu.Unlock()
	return err
}

func (m *Manager) Get() (*Catalog, string, string, []string) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.catalog, m.status, m.at, append([]string{}, m.errors...)
}
