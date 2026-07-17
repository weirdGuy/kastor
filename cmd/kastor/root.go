package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

// version is set at build time via -ldflags "-X main.version=...".
var version = "dev"

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "kastor",
		Short:         "Agent Definition Language — declarative AI agent specs",
		Long:          "kastor compiles declarative agent specs (.agent, .tool, .prompt) to agent frameworks or reconciles them against hosted platforms.",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	// Flag misuse is a usage error (exit 2), like bad positional args.
	root.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return withExitCode(2, err)
	})

	root.AddCommand(newVersionCmd())
	root.AddCommand(newInitCmd())
	root.AddCommand(newValidateCmd())
	root.AddCommand(newBuildCmd())
	root.AddCommand(newPlanCmd())
	root.AddCommand(newApplyCmd())
	root.AddCommand(newDestroyCmd())
	root.AddCommand(newFmtCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the kastor version",
		RunE: func(cmd *cobra.Command, args []string) error {
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "kastor version %s\n", version)
			return err
		},
	}
}
