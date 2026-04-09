package generator

import (
	"fmt"
	"os"
	"strings"

	"github.com/mbvlabs/andurel/pkg/naming"
	"gopkg.in/yaml.v3"
)

type Blueprint struct {
	Models      map[string]map[string]string         `yaml:"models"`
	Controllers map[string]BlueprintControllerConfig `yaml:"controllers"`
}

type BlueprintControllerConfig struct {
	Resource bool  `yaml:"resource"`
	Views    *bool `yaml:"views"` // Use pointer to distinguish between missing and false
}

type BlueprintManager struct {
	modelManager      *ModelManager
	controllerManager *ControllerManager
	viewManager       *ViewManager
	migrationManager  *MigrationManager
	config            *UnifiedConfig
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
	}
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
		fmt.Printf("✓ Created migration: %s\n", path)
	}

	// 2. Generate Models
	for modelName := range bp.Models {
		if err := bm.modelManager.GenerateModel(modelName, "", false); err != nil {
			return fmt.Errorf("failed to generate model %s: %w", modelName, err)
		}
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
		}
	}

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
			// Check if the second part is a number (length)
			sqlType = fmt.Sprintf("VARCHAR(%s)", parts[1])
			parts = parts[1:] // Consume length
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

	// Handle constraints
	for _, part := range parts {
		switch strings.ToLower(part) {
		case "nullable":
			// Default is nullable in most DBs, but we can be explicit if needed
		case "required", "notnull":
			sqlType += " NOT NULL"
		case "unique":
			sqlType += " UNIQUE"
		}
	}

	return sqlType
}
