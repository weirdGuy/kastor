package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/getkastordev/kastor/internal/provider"
)

func newDoctorCmd() *cobra.Command {
	var target string
	cmd := &cobra.Command{
		Use:   "doctor [--target name] [dir]",
		Short: "Check whether what is deployed can actually run",
		Long: "doctor answers a different question from plan: not \"does the remote match the spec\" but " +
			"\"can the thing that is deployed actually serve a request\". For every agent the state tracks it " +
			"confirms the remote object exists, verifies the connection:// credentials its MCP servers reference " +
			"against the target's vault, reports which env:// refs the module needs and are unset, and checks that " +
			"the deployed agent is permitted to call the tools it declares.\n\n" +
			"doctor is read-only: it never invokes an agent, never changes a remote object, and never writes state. " +
			"It exits 0 when everything is ready and 1 when anything is not — including a check it could not " +
			"perform, since \"could not verify\" is not \"ready\".",
		Args: usageMaxArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			dir := "."
			if len(args) == 1 {
				dir = args[0]
			}
			return runDoctor(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr(), dir, target)
		},
	}
	cmd.Flags().StringVar(&target, "target", "", "check only the named platform target (default: all platform targets)")
	return cmd
}

func runDoctor(ctx context.Context, stdout, stderr io.Writer, dir, targetName string) error {
	jobs, release, err := preparePlatform(ctx, stderr, dir, targetName)
	if err != nil {
		return err
	}
	defer releaseAndWarn(stderr, release)

	ready := true
	for i, pj := range jobs {
		report, err := provider.BuildReport(ctx, pj.provider, pj.job)
		if err != nil {
			return err
		}
		if i > 0 {
			io.WriteString(stdout, "\n")
		}
		renderReport(stdout, report)
		ready = ready && report.Ready()
	}
	if !ready {
		// The findings are the output; re-stating them as an error message
		// would print them twice. The exit code carries the verdict.
		return withExitCode(1, errSilent)
	}
	return nil
}

// errSilent is a failure whose message main must not print, because the
// command already wrote everything the user needs to standard output.
var errSilent = silentError{}

type silentError struct{}

func (silentError) Error() string { return "" }

// renderReport prints one target's readiness report, following the plan
// renderer's conventions (SPEC.md §5.2, §5.3): one line per finding, warnings
// before the summary, and a countable summary line last.
func renderReport(w io.Writer, r *provider.Report) {
	if len(r.Environment) > 0 {
		fmt.Fprintln(w, "Environment:")
		for _, c := range r.Environment {
			renderCheck(w, c)
		}
		fmt.Fprintln(w)
	}

	for _, res := range r.Resources {
		fmt.Fprintf(w, "%s (%s)\n", res.Addr, res.ID)
		for _, c := range res.Checks {
			renderCheck(w, c)
		}
		fmt.Fprintln(w)
	}

	renderDiagnostics(w, r.Diagnostics)

	ok, failed, unknown := r.Counts()
	if failed == 0 && unknown == 0 {
		fmt.Fprintf(w, "target.%s is ready: %s passed.\n", r.Target, countNoun(ok, "check"))
		return
	}
	fmt.Fprintf(w, "Readiness for target.%s: %d ok, %d failed, %d could not be verified.\n",
		r.Target, ok, failed, unknown)
}

// renderCheck prints one check. The status marker is the scannable part; the
// subject carries its display name alongside so an opaque platform id stays
// decodable from the output without ever becoming the identifier (§5.3).
func renderCheck(w io.Writer, c provider.Check) {
	line := "  " + statusMarker(c.Status) + " "
	if subject := renderSubject(c); subject != "" {
		line += subject + ": "
	}
	line += c.Summary
	fmt.Fprintln(w, line)
	if c.Detail != "" {
		fmt.Fprintf(w, "      %s\n", c.Detail)
	}
}

// renderSubject formats a check's subject as `id ("Display Name")`, falling
// back to the bare id when the platform has no name for it — display names are
// nullable there, which is exactly why they cannot be the identifier.
func renderSubject(c provider.Check) string {
	if c.SubjectName == "" {
		return c.Subject
	}
	if c.Subject == "" {
		return c.SubjectName
	}
	return fmt.Sprintf("%s (%q)", c.Subject, c.SubjectName)
}

// statusMarker keeps the three statuses visually distinct at a glance: "?" is
// deliberately not "x", because an unverified check is not a failed one.
func statusMarker(s provider.Status) string {
	switch s {
	case provider.StatusOK:
		return "✓"
	case provider.StatusFailed:
		return "✗"
	default:
		return "?"
	}
}
