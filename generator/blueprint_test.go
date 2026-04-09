package generator

import (
	"os"
	"strings"
	"testing"

	"github.com/mbvlabs/andurel/generator/files"
)

type MockFileManager struct {
	*files.UnifiedManager
}

func (m *MockFileManager) RunSQLCGenerate() error {
	return nil // Skip SQLC in tests
}

func TestBlueprintGeneration(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer os.Chdir(oldWd)
	os.Chdir(tmpDir)

	// Create a minimal project structure
	os.MkdirAll("database/migrations", 0755)
	os.MkdirAll("models", 0755)
	os.MkdirAll("controllers", 0755)
	os.WriteFile("controllers/controller.go", []byte("package controllers\n"), 0644)
	os.MkdirAll("views", 0755)
	os.MkdirAll("router/routes", 0755)
	os.WriteFile("go.mod", []byte("module testapp\n\ngo 1.21\n"), 0644)
	
	// Create a minimal sqlc.yaml
	sqlcYaml := `version: "2"
sql:
  - engine: "postgresql"
    queries: "database/queries"
    schema: "database/migrations"
    gen:
      go:
        package: "db"
        out: "models/internal/db"
`
	os.MkdirAll("database/queries", 0755)
	os.WriteFile("database/sqlc.yaml", []byte(sqlcYaml), 0644)

	// Create andurel.yaml (config)
	andurelYaml := `project:
  name: testapp
  module_path: testapp
database:
  type: postgresql
paths:
  models: models
  queries: database/queries
  controllers: controllers
  views: views
  routes: router/routes
`
	os.WriteFile("andurel.yaml", []byte(andurelYaml), 0644)

	// Create draft.yaml
	draftYaml := `models:
  Post:
    title: string:200
    content: text

controllers:
  Post:
    resource: true
`
	os.WriteFile("draft.yaml", []byte(draftYaml), 0644)

	// Run generation
	coordinator, err := NewCoordinator()
	if err != nil {
		t.Fatalf("Failed to create coordinator: %v", err)
	}

	// Use MockFileManager
	mockFM := &MockFileManager{files.NewUnifiedFileManager()}
	coordinator.ModelManager.fileManager = mockFM

	gen := Generator{coordinator: coordinator}

	if err := gen.GenerateFromBlueprint("draft.yaml"); err != nil {
		t.Fatalf("Failed to generate from blueprint: %v", err)
	}

	// Verify files
	filesToCheck := []string{
		"models/post.go",
		"controllers/posts.go",
		"database/queries/posts.sql",
	}

	for _, f := range filesToCheck {
		if _, err := os.Stat(f); os.IsNotExist(err) {
			t.Errorf("File %s was not generated", f)
		}
	}

	// Check if migration was created
	migrations, _ := os.ReadDir("database/migrations")
	foundMigration := false
	for _, m := range migrations {
		if strings.Contains(m.Name(), "create_posts") {
			foundMigration = true
			break
		}
	}
	if !foundMigration {
		t.Error("Migration for posts was not created")
	}
}
