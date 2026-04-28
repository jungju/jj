package run

import (
	"reflect"
	"testing"
)

func TestExtractMarkdownSectionItems(t *testing.T) {
	got := extractMarkdownSectionItems(`# Title

## Goals

- First goal
1. Second goal

## Other

- Ignore me
`, "Goals")
	want := []string{"First goal", "Second goal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items mismatch:\nwant %#v\ngot  %#v", want, got)
	}
}

func TestExtractMarkdownSectionItemsFallsBackToAllListItems(t *testing.T) {
	got := extractMarkdownSectionItems("- one\n* two\n3. three\n", "Missing")
	want := []string{"one", "two", "three"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("items mismatch:\nwant %#v\ngot  %#v", want, got)
	}
}

func TestEmptyFallback(t *testing.T) {
	if got := emptyFallback("  value  ", "fallback"); got != "value" {
		t.Fatalf("unexpected non-empty value: %q", got)
	}
	if got := emptyFallback(" \n", "fallback"); got != "fallback" {
		t.Fatalf("unexpected fallback value: %q", got)
	}
}
