package main

import (
	"context"
	"io"

	"github.com/spf13/cobra"

	"github.com/weirdGuy/agentform/internal/provider"
)

func newPlanCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "plan [--target name] [dir]",
		Short: "Show what apply would change on the module's platform targets",
		Long:  "plan runs the full validate pipeline, then compares the spec against the state file and the remote platform (three-way) for each platform target and prints the pending changes. plan is a pure read: it never modifies remote resources or the state file. With --target only the named target is planned; otherwise all platform targets in lexicographic name order.",
		Args:  usageMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runPlan(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "plan only the named platform target (default: all platform targets)")
	return cmd
}

func runPlan(ctx context.Context, stdout, stderr io.Writer, dir, targetName string) error {
	jobs, release, err := preparePlatform(stderr, dir, targetName)
	if err != nil {
		return err
	}
	defer releaseAndWarn(stderr, release)

	for i, pj := range jobs {
		plan, err := provider.BuildPlan(ctx, pj.provider, pj.job)
		if err != nil {
			return err
		}
		if i > 0 {
			io.WriteString(stdout, "\n")
		}
		renderPlan(stdout, plan)
	}
	return nil
}
