package cmd

import (
	"bytes"
	"testing"

	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
)

func TestRootHasRequestedServices(t *testing.T) {
	root := newRootCommand(dependencies{out: &bytes.Buffer{}, errOut: &bytes.Buffer{}})
	for _, name := range []string{"auth", "docs", "calendar", "slides"} {
		if root.Commands()[0] == nil {
			t.Fatal("unexpected empty command list")
		}
		if command, _, err := root.Find([]string{name}); err != nil || command.Name() != name {
			t.Fatalf("missing command %q: command=%v err=%v", name, command, err)
		}
	}
}

func TestBuildServiceTreeIncludesNestedMethods(t *testing.T) {
	doc := &discovery.Document{Name: "calendar", Resources: map[string]*discovery.Resource{
		"calendars": {Resources: map[string]*discovery.Resource{
			"events": {Methods: map[string]*discovery.Method{"list": {HTTPMethod: "GET", Path: "events"}}},
		}},
	}}
	command := buildServiceTree(doc, &bytes.Buffer{}, &bytes.Buffer{})
	events, _, err := command.Find([]string{"calendars", "events", "list"})
	if err != nil || events.Name() != "list" {
		t.Fatalf("method=%v err=%v", events, err)
	}
}
