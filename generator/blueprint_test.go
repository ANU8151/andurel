package generator

import (
	"os"
	"testing"

	"github.com/mbvlabs/andurel/generator/files"
)

type MockFileManager struct {
	*files.UnifiedManager
}

func (m *MockFileManager) RunSQLCGenerate() error {
	return nil // Skip SQLC in tests
}

func TestBlueprintLifecycle(t *testing.T) {
	tmpDir := t.TempDir()
	oldWd, _ := os.Getwd()
	defer func() { _ = os.Chdir(oldWd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("Failed to change to temp directory: %v", err)
	}

	// Create a minimal project structure
	if err := os.MkdirAll("database/migrations", 0755); err != nil {
		t.Fatalf("Failed to create migrations dir: %v", err)
	}
	if err := os.MkdirAll("models", 0755); err != nil {
		t.Fatalf("Failed to create models dir: %v", err)
	}
	if err := os.MkdirAll("controllers", 0755); err != nil {
		t.Fatalf("Failed to create controllers dir: %v", err)
	}
	if err := os.WriteFile("controllers/controller.go", []byte("package controllers\n"), 0644); err != nil {
		t.Fatalf("Failed to write controller file: %v", err)
	}
	if err := os.MkdirAll("views", 0755); err != nil {
		t.Fatalf("Failed to create views dir: %v", err)
	}
	if err := os.MkdirAll("router/routes", 0755); err != nil {
		t.Fatalf("Failed to create routes dir: %v", err)
	}
	if err := os.WriteFile("go.mod", []byte("module testapp\n\ngo 1.21\n"), 0644); err != nil {
		t.Fatalf("Failed to write go.mod: %v", err)
	}
	
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
	if err := os.MkdirAll("database/queries", 0755); err != nil {
		t.Fatalf("Failed to create queries dir: %v", err)
	}
	if err := os.WriteFile("database/sqlc.yaml", []byte(sqlcYaml), 0644); err != nil {
		t.Fatalf("Failed to write sqlc.yaml: %v", err)
	}

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
	if err := os.WriteFile("andurel.yaml", []byte(andurelYaml), 0644); err != nil {
		t.Fatalf("Failed to write andurel.yaml: %v", err)
	}

	// Create draft.yaml
	draftYaml := `models:
  Post:
    title: string:200
    content: text

controllers:
  Post:
    resource: true
`
	if err := os.WriteFile("draft.yaml", []byte(draftYaml), 0644); err != nil {
		t.Fatalf("Failed to write draft.yaml: %v", err)
	}

	// Run generation
	coordinator, err := NewCoordinator()
	if err != nil {
		t.Fatalf("Failed to create coordinator: %v", err)
	}

	// Use MockFileManager
	mockFM := &MockFileManager{files.NewUnifiedFileManager()}
	coordinator.ModelManager.fileManager = mockFM

	gen := Generator{coordinator: coordinator}

	// 1. Build
	if err := gen.GenerateFromBlueprint("draft.yaml"); err != nil {
		t.Fatalf("Failed to build from blueprint: %v", err)
	}

	// Verify files exist
	modelPath := "models/post.go"
	if _, err := os.Stat(modelPath); os.IsNotExist(err) {
		t.Errorf("Model file %s was not generated", modelPath)
	}

	// 2. Erase
	if err := gen.EraseFromBlueprint("draft.yaml"); err != nil {
		t.Fatalf("Failed to erase from blueprint: %v", err)
	}

	// Verify files are removed
	if _, err := os.Stat(modelPath); err == nil {
		t.Errorf("Model file %s should have been removed", modelPath)
	}
}
