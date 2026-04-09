package generator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mbvlabs/andurel/generator/controllers"
	"github.com/mbvlabs/andurel/pkg/naming"
	"gopkg.in/yaml.v3"
)

const manifestFileName = ".blueprint.json"

type Blueprint struct {
	Models      map[string]map[string]string         `yaml:"models"`
	Controllers map[string]BlueprintControllerConfig `yaml:"controllers"`
	Views       map[string]bool                      `yaml:"views"`
	Routes      map[string]string                    `yaml:"routes"`
}

type BlueprintControllerConfig struct {
	Resource bool                       `yaml:"resource"`
	Views    *bool                      `yaml:"views"`
	Methods  map[string]BlueprintMethod `yaml:",inline"`
}

type BlueprintMethod struct {
	Query  string `yaml:"query"`
	Render string `yaml:"render"`
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
	for modelName, fields := range bp.Models {
		validationRules := make(map[string]string)
		for fieldName, fieldDef := range fields {
			rules := bm.extractValidationRules(fieldDef)
			if rules != "" {
				validationRules[naming.ToSnakeCase(fieldName)] = rules
			}
		}

		if err := bm.modelManager.GenerateModel(modelName, "", false, validationRules); err != nil {
			return fmt.Errorf("failed to generate model %s: %w", modelName, err)
		}

		tableName := naming.DeriveTableName(modelName)
		snakeName := naming.ToSnakeCase(modelName)
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

		methods := make([]controllers.MethodConfig, 0)
		for methodName, methodConf := range config.Methods {
			methods = append(methods, controllers.MethodConfig{
				Name:   methodName,
				Query:  methodConf.Query,
				Render: methodConf.Render,
			})
		}

		if config.Resource || len(methods) > 0 {
			if err := bm.controllerManager.GenerateControllerFromModel(controllerName, withViews, methods); err != nil {
				return fmt.Errorf("failed to generate controller %s: %w", controllerName, err)
			}

			tableName := naming.DeriveTableName(controllerName)
			bm.addGeneratedFile(filepath.Join(bm.config.Paths.Controllers, tableName+".go"))
			bm.addGeneratedFile(filepath.Join(bm.config.Paths.Routes, tableName+".go"))
			bm.addGeneratedFile(filepath.Join("router", "connect_"+tableName+"_routes.go"))

			if withViews {
				if err := bm.viewManager.GenerateViewWithController(controllerName, tableName); err != nil {
					return fmt.Errorf("failed to generate views for %s: %w", controllerName, err)
				}
				bm.addGeneratedFile(filepath.Join(bm.config.Paths.Views, tableName+"_resource.templ"))
			}
		}
	}

	// 4. Generate standalone Views
	for modelName, active := range bp.Views {
		if !active {
			continue
		}

		if err := bm.viewManager.GenerateViewFromModel(modelName, false); err != nil {
			return fmt.Errorf("failed to generate standalone views for %s: %w", modelName, err)
		}

		tableName := naming.DeriveTableName(modelName)
		bm.addGeneratedFile(filepath.Join(bm.config.Paths.Views, tableName+"_resource.templ"))
	}

	// 5. Generate standalone Routes
	for modelName, routeType := range bp.Routes {
		if routeType != "resource" {
			continue
		}

		tableName := naming.DeriveTableName(modelName)
		// We'll use the controller manager but only for route generation logic
		// (Currently Andurel ties them together, so we'll generate standard resource routes)
		if err := bm.controllerManager.GenerateControllerFromModel(modelName, false, nil); err != nil {
			return fmt.Errorf("failed to generate routes for %s: %w", modelName, err)
		}

		bm.addGeneratedFile(filepath.Join(bm.config.Paths.Controllers, tableName+".go"))
		bm.addGeneratedFile(filepath.Join(bm.config.Paths.Routes, tableName+".go"))
		bm.addGeneratedFile(filepath.Join("router", "connect_"+tableName+"_routes.go"))
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

	// 1. Erase Controllers and explicit Routes
	controllersToErase := make(map[string]bool)
	for name := range bp.Controllers {
		controllersToErase[name] = true
	}
	for name := range bp.Routes {
		controllersToErase[name] = true
	}

	for name := range controllersToErase {
		tableName := naming.DeriveTableName(name)

		filesToRemove := []string{
			filepath.Join(bm.config.Paths.Controllers, tableName+".go"),
			filepath.Join(bm.config.Paths.Routes, tableName+".go"),
			filepath.Join("router", "connect_"+tableName+"_routes.go"),
		}

		for _, f := range filesToRemove {
			if err := os.Remove(f); err == nil {
				fmt.Printf("✓ Removed: %s\n", f)
			}
		}
	}

	// 2. Erase Models, Queries and Views
	modelsToErase := make(map[string]bool)
	for name := range bp.Models {
		modelsToErase[name] = true
	}
	for name := range bp.Views {
		modelsToErase[name] = true
	}

	for name := range modelsToErase {
		tableName := naming.DeriveTableName(name)
		snakeName := naming.ToSnakeCase(name)

		filesToRemove := []string{
			filepath.Join(bm.config.Paths.Models, snakeName+".go"),
			filepath.Join(bm.config.Paths.Models, "factories", snakeName+".go"),
			filepath.Join(bm.config.Paths.Queries, tableName+".sql"),
			filepath.Join(bm.config.Paths.Views, tableName+"_resource.templ"),
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

type sqlFieldInfo struct {
	columnType string
	fkTable    string
}

func (bm *BlueprintManager) extractValidationRules(def string) string {
	parts := strings.Fields(def)
	for _, part := range parts {
		if strings.HasPrefix(part, "validate:") {
			return strings.TrimPrefix(part, "validate:")
		}
	}
	return ""
}

func (bm *BlueprintManager) buildCreateTableSQL(tableName string, fields map[string]string) (string, error) {
	var sb strings.Builder
	var foreignKeys []string

	sb.WriteString(fmt.Sprintf("CREATE TABLE %s (\n", tableName))
	sb.WriteString("    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),\n")

	for fieldName, fieldDef := range fields {
		fieldInfo := bm.mapBlueprintTypeToSQL(fieldName, fieldDef)
		columnName := naming.ToSnakeCase(fieldName)
		sb.WriteString(fmt.Sprintf("    %s %s,\n", columnName, fieldInfo.columnType))

		if fieldInfo.fkTable != "" {
			fk := fmt.Sprintf("    CONSTRAINT fk_%s_%s FOREIGN KEY (%s) REFERENCES %s(id) ON DELETE CASCADE",
				tableName, columnName, columnName, fieldInfo.fkTable)
			foreignKeys = append(foreignKeys, fk)
		}
	}

	sb.WriteString("    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),\n")
	sb.WriteString("    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()")

	if len(foreignKeys) > 0 {
		sb.WriteString(",\n")
		sb.WriteString(strings.Join(foreignKeys, ",\n"))
	}

	sb.WriteString("\n);")

	return sb.String(), nil
}

func (bm *BlueprintManager) mapBlueprintTypeToSQL(fieldName, def string) sqlFieldInfo {
	// Filter out validation rules (anything containing :)
	// Laravel Blueprint uses validate:rule1,rule2 or rule1|rule2
	// Our previous logic replaced : with space, which was too aggressive for string:200
	
	rawParts := strings.Fields(def)
	var parts []string
	for _, p := range rawParts {
		if !strings.HasPrefix(p, "validate:") {
			parts = append(parts, p)
		}
	}

	if len(parts) == 0 {
		return sqlFieldInfo{columnType: "TEXT"}
	}

	// Now we process the type part which might still have : for length like string:200
	typeDef := parts[0]
	typeParts := strings.Split(typeDef, ":")
	blueprintType := typeParts[0]
	
	var info sqlFieldInfo

	switch strings.ToLower(blueprintType) {
	case "string":
		if len(typeParts) > 1 {
			info.columnType = fmt.Sprintf("VARCHAR(%s)", typeParts[1])
		} else {
			info.columnType = "VARCHAR(255)"
		}
	case "text":
		info.columnType = "TEXT"
	case "integer", "int":
		info.columnType = "INTEGER"
	case "bigint":
		info.columnType = "BIGINT"
	case "boolean", "bool":
		info.columnType = "BOOLEAN"
	case "timestamp":
		info.columnType = "TIMESTAMP WITH TIME ZONE"
	case "date":
		info.columnType = "DATE"
	case "uuid":
		info.columnType = "UUID"
	case "id":
		info.columnType = "UUID"
		// Relationship detection from type definition (id:user)
		if len(typeParts) > 1 {
			info.fkTable = naming.DeriveTableName(typeParts[1])
		} else if strings.HasSuffix(strings.ToLower(fieldName), "_id") {
			modelName := fieldName[:len(fieldName)-3]
			info.fkTable = naming.DeriveTableName(modelName)
		}
	case "decimal":
		info.columnType = "DECIMAL"
	default:
		info.columnType = "TEXT"
	}

	// Handle other constraints in the remaining parts
	for i := 1; i < len(parts); i++ {
		p := strings.ToLower(parts[i])
		switch p {
		case "required", "notnull":
			info.columnType += " NOT NULL"
		case "unique":
			info.columnType += " UNIQUE"
		}
	}

	return info
}
