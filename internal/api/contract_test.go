package api

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// The code and openapi.yaml drifted once (available vs is_available, event
// availability vs avail, 400 vs 422). These checks keep the published spec
// and the implementation in step.

func readSpec(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatalf("read openapi.yaml: %v", err)
	}
	return string(b)
}

func TestSpecListsEveryErrorCode(t *testing.T) {
	spec := readSpec(t)
	for _, code := range []string{
		CodeMissingInput, CodeInputTooLong, CodeAmbiguousTLDFilter, CodeUnknownTLD,
		CodeBothTiersFailed, CodeUnknownCategory, CodeTooManyUnavailable,
		CodeTooManyInspireFrom, CodeInvalidDomain, CodeInvalidJSON, CodeRequestTooLarge,
	} {
		if !strings.Contains(spec, "- "+code+"\n") {
			t.Errorf("openapi.yaml error-code enum is missing %q", code)
		}
	}
}

func TestSpecDescribesEverySuggestionField(t *testing.T) {
	spec := readSpec(t)
	typ := reflect.TypeOf(Suggestion{})
	for i := range typ.NumField() {
		tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")[0]
		if !strings.Contains(spec, "        "+tag+":") {
			t.Errorf("openapi.yaml Suggestion schema has no %q property", tag)
		}
	}
}

func TestSpecNamesTheStreamEvents(t *testing.T) {
	spec := readSpec(t)
	for _, ev := range []string{"event: suggestions", "event: avail", "event: done"} {
		if !strings.Contains(spec, ev) {
			t.Errorf("openapi.yaml does not mention %q", ev)
		}
	}
}
