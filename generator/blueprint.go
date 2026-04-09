package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mbvlabs/andurel/pkg/naming"
	"gopkg.in/yaml.v3"
)

const manifestFileName = ".blueprint.json"

type Blueprint struct {
	Models      map[string]map[string]string         `yaml:"models"`
	Controllers map[string]BlueprintControllerConfig `yaml:"controllers"`
}

type BlueprintControllerConfig struct {
	Resource bool  `yaml:"resource"`
	Views    *bool `yaml:"views"` // Use pointer to distinguish between missing and false
}

type BlueprintManifest struct {
	GeneratedFiles []string `json:"generated_files"`
}

type BlueprintManager struct {
	modelManager      *ModelManager
	controllerManager *ControllerManager
	viewManager       *ViewManager
	migrationManager  *MigrationManager
	config            *UnifiedConfig
	generatedFiles    []string
}

func NewBlueprintManager(
	modelManager *ModelManager,
	controllerManager *ControllerManager,
	viewManager *ViewManager,
	migrationManager *MigrationManager,
	config *UnifiedConfig,
) *BlueprintManager {
	return &BlueprintManager{
		modelManager:      modelManager,
		controllerManager: controllerManager,
		viewManager:       viewManager,
		migrationManager:  migrationManager,
		config:            config,
		generatedFiles:    make([]string, 0),
	}
}

func (bm *BlueprintManager) addGeneratedFile(path string) {
	bm.generatedFiles = append(bm.generatedFiles, path)
}

func (bm *BlueprintManager) saveManifest() error {
	manifest := BlueprintManifest{
		GeneratedFiles: bm.generatedFiles,
	}

	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal manifest: %w", err)
	}

	if err := os.WriteFile(manifestFileName, data, 0644); err != nil {
		return fmt.Errorf("failed to save manifest: %w", err)
	}

	return nil
}

func (bm *BlueprintManager) GenerateFromBlueprint(filePath string) error {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read blueprint file: %w", err)
	}

	var bp Blueprint
	if err := yaml.Unmarshal(data, &bp); err != nil {
		return fmt.Errorf("failed to parse blueprint YAML: %w", err)
	}

	// 1. Generate Migrations
	for modelName, fields := range bp.Models {
		tableName := naming.DeriveTableName(modelName)
		sql, err := bm.buildCreateTableSQL(tableName, fields)
		if err != nil {
			return fmt.Errorf("failed to build SQL for %s: %w", modelName, err)
		}

		migrationName := fmt.Sprintf("create_%s", tableName)
		path, err := bm.migrationManager.CreateMigration(migrationName, sql, bm.config)
		if err != nil {
			return fmt.Errorf("failed to create migration for %s: %w", modelName, err)
		}
		bm.addGeneratedFile(path)
		fmt.Printf("✓ Created migration: %s\n", path)
	}

	// 2. Generate Models
	for modelName := range bp.Models {
		tableName := naming.DeriveTableName(modelName)
		snakeName := naming.ToSnakeCase(modelName)
		
		if err := bm.modelManager.GenerateModel(modelName, "", false); err != nil {
			return fmt.Errorf("failed to generate model %s: %w", modelName, err)
		}

		bm.addGeneratedFile(filepath.Join(bm.config.Paths.Models, snakeName+".go"))
		bm.addGeneratedFile(filepath.Join(bm.config.Paths.Models, "factories", snakeName+".go"))
		bm.addGeneratedFile(filepath.Join(bm.config.Paths.Queries, tableName+".sql"))
	}

	// 3. Generate Controllers
	for controllerName, config := range bp.Controllers {
		withViews := true
		if config.Views != nil {
			withViews = *config.Views
		}

		if config.Resource {
			if err := bm.controllerManager.GenerateControllerFromModel(controllerName, withViews); err != nil {
				return fmt.Errorf("failed to generate resource controller %s: %w", controllerName, err)
			}

			tableName := naming.DeriveTableName(controllerName)
			bm.addGeneratedFile(filepath.Join(bm.config.Paths.Controllers, tableName+".go"))
			bm.addGeneratedFile(filepath.Join(bm.config.Paths.Routes, tableName+".go"))
			bm.addGeneratedFile(filepath.Join("router", "connect_"+tableName+"_routes.go"))
			
			if withViews {
				bm.addGeneratedFile(filepath.Join(bm.config.Paths.Views, tableName+"_resource.templ"))
			}
		}
	}

	return bm.saveManifest()
}

func (bm *BlueprintManager) EraseFromBlueprint(filePath string) error {
	// Try to load from manifest first
	manifestData, err := os.ReadFile(manifestFileName)
	if err == nil {
		var manifest BlueprintManifest
		if err := json.Unmarshal(manifestData, &manifest); err == nil {
			for _, path := range manifest.GeneratedFiles {
				if err := os.Remove(path); err == nil {
					fmt.Printf("✓ Removed: %s\n", path)
				}
			}
			_ = os.Remove(manifestFileName)
			fmt.Println("\n✓ Erase complete (using manifest).")
			return nil
		}
	}

	// Fallback to calculated erase if manifest is missing or invalid
	fmt.Println("Warning: Manifest not found or invalid, falling back to calculated erase...")
	
	data, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("failed to read blueprint file: %w", err)
	}

	var bp Blueprint
	if err := yaml.Unmarshal(data, &bp); err != nil {
		return fmt.Errorf("failed to parse blueprint YAML: %w", err)
	}

	// 1. Erase Controllers and Views
	for controllerName := range bp.Controllers {
		tableName := naming.DeriveTableName(controllerName)
		
		filesToRemove := []string{
			filepath.Join(bm.config.Paths.Controllers, tableName+".go"),
			filepath.Join(bm.config.Paths.Views, tableName+"_resource.templ"),
			filepath.Join(bm.config.Paths.Routes, tableName+".go"),
			filepath.Join("router", "connect_"+tableName+"_routes.go"),
		}

		for _, f := range filesToRemove {
			if err := os.Remove(f); err == nil {
				fmt.Printf("✓ Removed: %s\n", f)
			}
		}
	}

	// 2. Erase Models and Queries
	for modelName := range bp.Models {
		tableName := naming.DeriveTableName(modelName)
		snakeName := naming.ToSnakeCase(modelName)

		filesToRemove := []string{
			filepath.Join(bm.config.Paths.Models, snakeName+".go"),
			filepath.Join(bm.config.Paths.Models, "factories", snakeName+".go"),
			filepath.Join(bm.config.Paths.Queries, tableName+".sql"),
		}

		for _, f := range filesToRemove {
			if err := os.Remove(f); err == nil {
				fmt.Printf("✓ Removed: %s\n", f)
			}
		}

		// Erase Migrations for this table
		if len(bm.config.Database.MigrationDirs) > 0 {
			migrationDir := bm.config.Database.MigrationDirs[0]
			entries, _ := os.ReadDir(migrationDir)
			for _, entry := range entries {
				if strings.Contains(entry.Name(), "create_"+tableName) {
					path := filepath.Join(migrationDir, entry.Name())
					if err := os.Remove(path); err == nil {
						fmt.Printf("✓ Removed migration: %s\n", path)
					}
				}
			}
		}
	}

	fmt.Println("\n✓ Erase complete. Some shared files (like router/routes.go additions) might need manual cleanup if they were modified.")
	return nil
}

func (bm *BlueprintManager) buildCreateTableSQL(tableName string, fields map[string]string) (string, error) {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("CREATE TABLE %s (\n", tableName))
	sb.WriteString("    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),\n")

	for fieldName, fieldDef := range fields {
		sqlType := bm.mapBlueprintTypeToSQL(fieldDef)
		sb.WriteString(fmt.Sprintf("    %s %s,\n", naming.ToSnakeCase(fieldName), sqlType))
	}

	sb.WriteString("    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),\n")
	sb.WriteString("    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()\n")
	sb.WriteString(");")

	return sb.String(), nil
}

func (bm *BlueprintManager) mapBlueprintTypeToSQL(def string) string {
	parts := strings.Fields(strings.ReplaceAll(def, ":", " "))
	if len(parts) == 0 {
		return "TEXT"
	}

	blueprintType := parts[0]
	var sqlType string

	switch strings.ToLower(blueprintType) {
	case "string":
		if len(parts) > 1 {
			sqlType = fmt.Sprintf("VARCHAR(%s)", parts[1])
			parts = parts[1:]
		} else {
			sqlType = "VARCHAR(255)"
		}
	case "text":
		sqlType = "TEXT"
	case "integer", "int":
		sqlType = "INTEGER"
	case "bigint":
		sqlType = "BIGINT"
	case "boolean", "bool":
		sqlType = "BOOLEAN"
	case "timestamp":
		sqlType = "TIMESTAMP WITH TIME ZONE"
	case "date":
		sqlType = "DATE"
	case "uuid":
		sqlType = "UUID"
	case "id":
		sqlType = "UUID"
	case "decimal":
		sqlType = "DECIMAL"
	default:
		sqlType = "TEXT"
	}

	for _, part := range parts {
		switch strings.ToLower(part) {
		case "nullable":
		case "required", "notnull":
			sqlType += " NOT NULL"
		case "unique":
			sqlType += " UNIQUE"
		}
	}

	return sqlType
}
