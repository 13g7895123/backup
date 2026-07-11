package api

import (
	"testing"

	"backup-manager/internal/store"
)

func TestProjectWithoutPassword(t *testing.T) {
	original := &store.Project{ID: 1, Name: "example", DbPassword: "secret"}
	public := projectWithoutPassword(original)
	if public.DbPassword != "" {
		t.Fatal("public project exposed db_password")
	}
	if original.DbPassword != "secret" {
		t.Fatal("sanitizing response mutated stored project")
	}
}

func TestProjectsWithoutPasswords(t *testing.T) {
	original := []store.Project{{ID: 1, DbPassword: "one"}, {ID: 2, DbPassword: "two"}}
	public := projectsWithoutPasswords(original)
	for _, project := range public {
		if project.DbPassword != "" {
			t.Fatalf("project %d exposed db_password", project.ID)
		}
	}
	if original[0].DbPassword != "one" || original[1].DbPassword != "two" {
		t.Fatal("sanitizing response mutated source slice")
	}
}
