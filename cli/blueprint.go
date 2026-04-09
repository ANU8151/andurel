package cli

import (
	"fmt"
	"os"

	"github.com/mbvlabs/andurel/generator"
	"github.com/spf13/cobra"
)

func newBlueprintCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "blueprint",
		Aliases: []string{"bp"},
		Short:   "Blueprint management commands",
		Long:    "Generate and manage resources using YAML blueprint files.",
	}

	cmd.AddCommand(
		newBlueprintInitCommand(),
		newBlueprintBuildCommand(),
		newBlueprintEraseCommand(),
	)

	return cmd
}

func newBlueprintInitCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "init",
		Short: "Initialize a new blueprint file (draft.yaml)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := chdirToProjectRoot(); err != nil {
				return err
			}

			const content = `models:
  Post:
    title: string:200 unique validate:required,max=100
    content: text validate:required
    published_at: timestamp nullable

controllers:
  Post:
    resource: true
    popular:
      query: all
      render: PostPopular
`
			if _, err := os.Stat("draft.yaml"); err == nil {
				return fmt.Errorf("draft.yaml already exists")
			}

			if err := os.WriteFile("draft.yaml", []byte(content), 0644); err != nil {
				return fmt.Errorf("failed to create draft.yaml: %w", err)
			}

			fmt.Println("✓ Created draft.yaml. Edit it and run 'andurel blueprint build' to generate resources.")
			return nil
		},
	}
}

func newBlueprintBuildCommand() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Generate resources from a blueprint file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := chdirToProjectRoot(); err != nil {
				return err
			}

			gen, err := generator.New()
			if err != nil {
				return err
			}

			return gen.GenerateFromBlueprint(file)
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "draft.yaml", "Path to the blueprint file")
	return cmd
}

func newBlueprintEraseCommand() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "erase",
		Short: "Remove resources generated from a blueprint file",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := chdirToProjectRoot(); err != nil {
				return err
			}

			gen, err := generator.New()
			if err != nil {
				return err
			}

			return gen.EraseFromBlueprint(file)
		},
	}

	cmd.Flags().StringVarP(&file, "file", "f", "draft.yaml", "Path to the blueprint file")
	return cmd
}
