package render

import (
	"bytes"
	"testing"

	"forge.local/tools/forge-cli/internal/control"
)

func TestWriteTableAlignsProjectColumns(t *testing.T) {
	var output bytes.Buffer
	err := Write(&output, "table", []control.Project{
		{ID: "project-1", Name: "acme", Slug: "acme"},
		{ID: "project-22", Name: "longer-name", Slug: "longer-slug"},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	const want = "ID          NAME         SLUG\n" +
		"project-1   acme         acme\n" +
		"project-22  longer-name  longer-slug\n"
	if got := output.String(); got != want {
		t.Fatalf("table output = %q, want %q", got, want)
	}
}

func TestWriteJSONHasStableResourceAndListShapes(t *testing.T) {
	project := control.Project{ID: "project-1", Name: "acme", Slug: "acme", CreatedAt: "now", UpdatedAt: "later"}

	var single bytes.Buffer
	if err := Write(&single, "json", project); err != nil {
		t.Fatalf("single Write() error = %v", err)
	}
	const wantSingle = "{\"id\":\"project-1\",\"name\":\"acme\",\"slug\":\"acme\",\"createdAt\":\"now\",\"updatedAt\":\"later\"}\n"
	if got := single.String(); got != wantSingle {
		t.Fatalf("single JSON = %q, want %q", got, wantSingle)
	}

	var list bytes.Buffer
	if err := Write(&list, "json", []control.Project{project}); err != nil {
		t.Fatalf("list Write() error = %v", err)
	}
	wantList := "[" + wantSingle[:len(wantSingle)-1] + "]\n"
	if got := list.String(); got != wantList {
		t.Fatalf("list JSON = %q, want %q", got, wantList)
	}
}
