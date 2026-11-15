package transport

import (
	"testing"
)

func TestStrictRejectsDuplicateAndUnknownFields(t *testing.T) {
	var body struct {
		ID string `json:"id"`
	}
	for _, input := range []string{`{"id":"a","id":"b"}`, `{"id":"a","extra":true}`, `{"id":"a"} {}`} {
		if err := strict([]byte(input), &body); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	var raw any
	if err := strict([]byte(`{"payload":{"id":"a","id":"b"}}`), &raw); err == nil {
		t.Fatal("nested duplicate accepted")
	}
	if err := strict([]byte(`{"id":"a"}`), &body); err != nil {
		t.Fatal(err)
	}
}
func TestNameValidation(t *testing.T) {
	for _, name := range []string{"", " Name", "Name\n", "a\nb"} {
		if validName(name) {
			t.Fatalf("invalid name accepted: %q", name)
		}
	}
	if !validName("Player 123") {
		t.Fatal("valid name rejected")
	}
}
