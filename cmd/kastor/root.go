package main

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version and commit are set at release-build time via
// -ldflags "-X main.version=... -X main.commit=...".
var (
	version = "dev"
	commit  = "none"
)

// resolveVersion returns the version and commit to report, falling back to
// Go build info for binaries built without ldflags (go build, go install).
func resolveVersion() (string, string) {
	v, c := version, commit
	bi, ok := debug.ReadBuildInfo()
	if !ok {
		return v, c
	}
	if v == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		v = bi.Main.Version
	}
	if c == "none" {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				c = s.Value[:7]
			}
		}
	}
	return v, c
}

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
	root.AddCommand(newDoctorCmd())
	root.AddCommand(newDestroyCmd())
	root.AddCommand(newFmtCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the kastor version",
		RunE: func(cmd *cobra.Command, args []string) error {
			v, c := resolveVersion()
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "kastor version %s (commit %s)\n", v, c)
			return err
		},
	}
}
