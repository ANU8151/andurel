package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mbvlabs/andurel/generator"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func newQueriesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "query",
		Aliases: []string{"q"},
		Short:   "SQL query management",
		Long:    "Generate and compile SQL queries for database tables.",
	}

	cmd.AddCommand(
		newQueriesGenerateCommand(),
		newQueriesRefreshCommand(),
		newQueriesCompileCommand(),
		newQueriesValidateCommand(),
	)

	return cmd
}

func newQueriesGenerateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "generate [table_name]",
		Short: "Generate CRUD queries for a database table",
		Long: `Generate SQL query file and SQLC types for a database table.
This is useful for tables that don't need a full model wrapper.

The command generates:
  - SQL queries file (database/queries/{table_name}.sql)
  - SQLC-generated query functions and types

The table name is used exactly as provided - no naming conventions are applied.
An error is returned if the table is not found in the migrations.

	Examples:
  andurel query generate user_roles           # Generate queries for 'user_roles' table
  andurel query generate users_organizations  # Generate queries for a junction table`,
		Args: cobra.ExactArgs(1),
		RunE: runQueriesGenerate,
	}

	return cmd
}

func runQueriesGenerate(cmd *cobra.Command, args []string) error {
	if err := chdirToProjectRoot(); err != nil {
		return err
	}

	tableName := args[0]

	gen, err := generator.New()
	if err != nil {
		return err
	}

	return gen.GenerateQueriesOnly(tableName)
}

func newQueriesRefreshCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh [table_name]",
		Short: "Refresh CRUD queries for a database table",
		Long: `Refresh an existing SQL query file and SQLC types for a database table.
This keeps the queries-only file in sync with the current table schema.

Examples:
  andurel query refresh user_roles          # Refresh queries for 'user_roles' table
  andurel query refresh users_organizations # Refresh queries for a junction table`,
		Args: cobra.ExactArgs(1),
		RunE: runQueriesRefresh,
	}
}

func runQueriesRefresh(cmd *cobra.Command, args []string) error {
	if err := chdirToProjectRoot(); err != nil {
		return err
	}

	tableName := args[0]

	gen, err := generator.New()
	if err != nil {
		return err
	}

	return gen.RefreshQueriesOnly(tableName)
}

func newQueriesCompileCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "compile",
		Short: "Compile SQL queries and generate Go code",
		Long: `Compile SQL queries to check for errors and generate Go code.

This runs both 'sqlc compile' and 'sqlc generate' in sequence.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := runSqlcCommand("compile"); err != nil {
				return err
			}
			return runSqlcCommand("generate")
		},
	}
}

func newQueriesValidateCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "validate",
		Short: "Validate database/sqlc.yaml against framework requirements",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSqlcValidate()
		},
	}
}

const (
	sqlcUserRelativePath = "database/sqlc.yaml"
	sqlcBaseRelativePath = "internal/storage/andurel_sqlc_config.yaml"
)

func sqlcUserPath(rootDir string) string {
	return filepath.Join(rootDir, sqlcUserRelativePath)
}

func sqlcBasePath(rootDir string) string {
	return filepath.Join(rootDir, sqlcBaseRelativePath)
}

func validateSQLCConfigAgainstBase(rootDir string) (string, error) {
	basePath := sqlcBasePath(rootDir)
	if _, err := os.Stat(basePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("missing %s", basePath)
		}
		return "", fmt.Errorf("failed to read base sqlc config: %w", err)
	}

	userPath := sqlcUserPath(rootDir)
	if _, err := os.Stat(userPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf(
				"missing %s; create it from %s",
				userPath,
				sqlcBaseRelativePath,
			)
		}
		return "", fmt.Errorf("failed to read user sqlc config: %w", err)
	}

	baseMap, err := readYAMLAsMap(basePath)
	if err != nil {
		return "", fmt.Errorf("failed to parse base sqlc config: %w", err)
	}
	userMap, err := readYAMLAsMap(userPath)
	if err != nil {
		return "", fmt.Errorf("failed to parse user sqlc config: %w", err)
	}
	if len(userMap) == 0 {
		return "", errors.New("database/sqlc.yaml cannot be empty")
	}

	issues := collectSQLCSubsetIssues(baseMap, userMap, basePath, userPath, "")
	if len(issues) > 0 {
		return "", formatSQLCValidationIssues(issues)
	}

	return userPath, nil
}

func collectSQLCSubsetIssues(base, user any, basePath, userPath, fieldPath string) []string {
	switch baseTyped := base.(type) {
	case map[string]any:
		userTyped, ok := user.(map[string]any)
		if !ok {
			return []string{fmt.Sprintf("%s must be a map", renderSQLCFieldPath(fieldPath))}
		}
		issues := make([]string, 0)
		for key, baseValue := range baseTyped {
			userValue, ok := userTyped[key]
			childPath := joinSQLCFieldPath(fieldPath, key)
			if !ok {
				issues = append(issues, fmt.Sprintf("missing required key %q in database/sqlc.yaml", childPath))
				continue
			}
			issues = append(issues, collectSQLCSubsetIssues(baseValue, userValue, basePath, userPath, childPath)...)
		}
		return issues
	case []any:
		userTyped, ok := user.([]any)
		if !ok {
			return []string{fmt.Sprintf("%s must be a list", renderSQLCFieldPath(fieldPath))}
		}
		issues := make([]string, 0)
		for _, baseValue := range baseTyped {
			bestIssues := []string{"no candidate entries found"}
			for _, userValue := range userTyped {
				candidateIssues := collectSQLCSubsetIssues(baseValue, userValue, basePath, userPath, fieldPath)
				if len(candidateIssues) == 0 {
					bestIssues = nil
					break
				}
				if len(candidateIssues) < len(bestIssues) {
					bestIssues = candidateIssues
				}
			}
			if bestIssues == nil {
				continue
			}
			issue := fmt.Sprintf(
				"missing required entry under %s from %s: %s",
				renderSQLCFieldPath(fieldPath),
				sqlcBaseRelativePath,
				summarizeSQLCYAMLValue(baseValue),
			)
			if len(bestIssues) > 0 {
				issue = issue + fmt.Sprintf(" (closest mismatch: %s)", bestIssues[0])
			}
			issues = append(issues, issue)
		}
		return issues
	default:
		if valuesEqualForSQLCField(base, user, basePath, userPath, fieldPath) {
			return nil
		}
		return []string{fmt.Sprintf(
			"required value mismatch at %s: expected %v from %s, got %v",
			renderSQLCFieldPath(fieldPath),
			base,
			sqlcBaseRelativePath,
			user,
		)}
	}
}

func formatSQLCValidationIssues(issues []string) error {
	const maxIssues = 12
	if len(issues) > maxIssues {
		remaining := len(issues) - maxIssues
		issues = append(issues[:maxIssues], fmt.Sprintf("... and %d more issue(s)", remaining))
	}
	return fmt.Errorf(
		"database/sqlc.yaml does not satisfy required settings from %s:\n- %s",
		sqlcBaseRelativePath,
		strings.Join(issues, "\n- "),
	)
}

func summarizeSQLCYAMLValue(value any) string {
	raw, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	summary := strings.TrimSpace(string(raw))
	summary = strings.ReplaceAll(summary, "\n", "; ")
	return summary
}

func valuesEqualForSQLCField(base, user any, basePath, userPath, fieldPath string) bool {
	baseStr, baseIsString := base.(string)
	userStr, userIsString := user.(string)
	if baseIsString && userIsString && isSQLCPathField(fieldPath) {
		baseResolved := resolveSQLCPath(baseStr, basePath)
		userResolved := resolveSQLCPath(userStr, userPath)
		return baseResolved == userResolved
	}
	return fmt.Sprint(base) == fmt.Sprint(user)
}

func isSQLCPathField(fieldPath string) bool {
	return strings.HasSuffix(fieldPath, ".schema") ||
		strings.HasSuffix(fieldPath, ".queries") ||
		strings.HasSuffix(fieldPath, ".out")
}

func resolveSQLCPath(value, configPath string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Clean(filepath.Join(filepath.Dir(configPath), value))
}

func joinSQLCFieldPath(parent, child string) string {
	if parent == "" {
		return child
	}
	return parent + "." + child
}

func renderSQLCFieldPath(fieldPath string) string {
	if fieldPath == "" {
		return "root"
	}
	return fieldPath
}

func readYAMLAsMap(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return map[string]any{}, nil
	}
	result := map[string]any{}
	if err := yaml.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return map[string]any{}, nil
	}
	return result, nil
}

func runSqlcValidate() error {
	rootDir, err := findGoModRoot()
	if err != nil {
		return err
	}

	configPath, err := validateSQLCConfigAgainstBase(rootDir)
	if err != nil {
		return err
	}

	relativePath, err := filepath.Rel(rootDir, configPath)
	if err != nil {
		relativePath = configPath
	}

	fmt.Fprintf(os.Stdout, "SQLC configuration is valid.\nRuntime config: %s\n", relativePath)
	return nil
}

func runSqlcCommand(action string) error {
	rootDir, err := findGoModRoot()
	if err != nil {
		return err
	}

	configPath, err := validateSQLCConfigAgainstBase(rootDir)
	if err != nil {
		return err
	}

	sqlcBin := filepath.Join(rootDir, "bin", "sqlc")
	if _, err := os.Stat(sqlcBin); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf(
				"sqlc binary not found at %s\nRun 'andurel tool sync' to download it",
				sqlcBin,
			)
		}
		return err
	}

	cmd := exec.Command(sqlcBin, "-f", configPath, action)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Dir = rootDir

	return cmd.Run()
}
