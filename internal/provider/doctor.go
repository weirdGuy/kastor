package provider

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/weirdGuy/kastor/internal/schema"
)

// Check kinds emitted by the engine itself. Providers contribute their own
// (see Checker); these are the ones no platform is needed to establish.
const (
	CheckRemote      = "remote"      // the state's id still resolves to a live object
	CheckEnvironment = "environment" // an env:// ref or api_key_env the module needs
)

// ResourceReport is one managed resource's readiness.
type ResourceReport struct {
	Addr   string  `json:"addr"`
	ID     string  `json:"id,omitempty"` // remote id from state
	Checks []Check `json:"checks"`
}

// Report is a kastor doctor run for one platform target: what the module needs
// from the environment, plus one entry per resource the state tracks. The whole
// thing is one serializable tree, so the --json rendering of SPEC.md §9 is a
// second renderer over this data rather than a second pipeline (§5.3).
type Report struct {
	Target string `json:"target"`
	// Environment is module-wide rather than per-resource: it answers "what
	// does this module need from my environment before it will run", which is
	// a question about the module, not about any one agent.
	Environment []Check          `json:"environment,omitempty"`
	Resources   []ResourceReport `json:"resources"`
	Diagnostics []Diagnostic     `json:"diagnostics,omitempty"`
}

// Counts tallies every check in the report by status.
func (r *Report) Counts() (ok, failed, unknown int) {
	tally := func(checks []Check) {
		for _, c := range checks {
			switch c.Status {
			case StatusOK:
				ok++
			case StatusFailed:
				failed++
			case StatusUnknown:
				unknown++
			}
		}
	}
	tally(r.Environment)
	for _, res := range r.Resources {
		tally(res.Checks)
	}
	return ok, failed, unknown
}

// Ready reports whether nothing needs attention. StatusUnknown counts against
// readiness: the command did not establish that the module is ready, and
// saying otherwise would be claiming an assurance it does not have.
func (r *Report) Ready() bool {
	_, failed, unknown := r.Counts()
	return failed == 0 && unknown == 0
}

// BuildReport runs the readiness checks for one platform target (SPEC.md
// §5.3). It is strictly read-only: it issues Read and, for a provider that
// implements Checker, Check — never Create, Update, or Delete — and never
// touches the state file.
//
// Resources come from state rather than from the spec, because readiness is a
// question about what is deployed. A block that has never been applied has
// nothing to check and is reported as a diagnostic, not as a failure: the
// answer there is kastor apply, and plan already says so.
func BuildReport(ctx context.Context, p Provider, job *Job) (*Report, error) {
	report := &Report{Target: job.Target.Name}
	report.Environment = environmentChecks(job)

	checker, _ := p.(Checker)
	resources := stateResources(job)

	for _, addr := range agentOrder(job) {
		st, tracked := resources[addr]
		if !tracked {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Severity: SeverityWarning,
				Addr:     addr,
				Summary:  "not deployed on this target",
				Detail:   "the block is in the spec but not in state, so there is nothing deployed to check; run kastor plan",
			})
			continue
		}

		res := ResourceReport{Addr: addr, ID: st.ID}
		remote, found, err := p.Read(ctx, st.ID)
		switch {
		case err != nil:
			// The platform could not answer, so nothing downstream of the
			// read is knowable either. That is unknown, not failed.
			res.Checks = append(res.Checks, Check{
				Kind:    CheckRemote,
				Status:  StatusUnknown,
				Subject: st.ID,
				Summary: "could not verify that the remote object exists",
				Detail:  err.Error(),
			})
		case !found:
			res.Checks = append(res.Checks, Check{
				Kind:    CheckRemote,
				Status:  StatusFailed,
				Subject: st.ID,
				Summary: "remote object is missing",
				Detail:  fmt.Sprintf("state maps %s to %s, but the platform has no such object; run kastor plan", addr, st.ID),
			})
		default:
			res.Checks = append(res.Checks, Check{
				Kind:    CheckRemote,
				Status:  StatusOK,
				Subject: st.ID,
				Summary: "remote object exists",
			})
			if checker != nil {
				desired, err := desiredResource(job, addr)
				if err != nil {
					return nil, err
				}
				checks, err := checker.Check(ctx, desired, remote)
				if err != nil {
					return nil, fmt.Errorf("%s: checking readiness: %w", addr, err)
				}
				res.Checks = append(res.Checks, checks...)
			}
		}
		report.Resources = append(report.Resources, res)
	}

	if checker == nil && len(report.Resources) > 0 {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{
			Severity: SeverityWarning,
			Summary:  fmt.Sprintf("target.%s verifies remote existence only", job.Target.Name),
			Detail:   "its provider implements no readiness checks, so credentials and tool permissions are not covered",
		})
	}
	return report, nil
}

// environmentChecks reports what the module needs from the process
// environment. It contacts nothing: this is the part of doctor that works with
// no credentials and no network, the same standing kastor validate has.
//
// The env:// refs are module-wide rather than restricted to the selected
// target, because the question being answered is "what does this module need
// before it will run" — a langgraph module's env:// refs are needed by the
// generated project whichever platform target doctor was pointed at.
func environmentChecks(job *Job) []Check {
	var checks []Check

	if apiKeyEnv, ok := job.Target.ConfigString("api_key_env"); ok && apiKeyEnv != "" {
		checks = append(checks, envCheck(
			apiKeyEnv,
			fmt.Sprintf("%s authenticates against this platform", job.Target.Addr()),
		))
	}

	// One check per variable, not per ref: two servers behind the same
	// variable are one thing to set.
	users := map[string][]string{}
	for _, s := range job.Module.MCPServers {
		for _, auth := range s.Auth {
			scheme, name, err := schema.ParseCredentialRef(auth.Ref)
			if err != nil || scheme != schema.SchemeEnv {
				continue
			}
			where := s.Addr()
			if len(auth.Targets) > 0 {
				where += " on " + strings.Join(auth.Targets, ", ")
			}
			users[name] = append(users[name], where)
		}
	}
	names := make([]string, 0, len(users))
	for name := range users {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		checks = append(checks, envCheck(name, strings.Join(users[name], "; ")))
	}
	return checks
}

// envCheck reports one environment variable. An empty value counts as unset:
// nothing that dials a server can authenticate with it, so treating it as
// present would be the same lie as reporting it missing when it is set.
func envCheck(name, detail string) Check {
	value, exists := os.LookupEnv(name)
	if !exists || value == "" {
		return Check{
			Kind:    CheckEnvironment,
			Status:  StatusFailed,
			Subject: name,
			Summary: "environment variable is not set",
			Detail:  detail,
		}
	}
	return Check{
		Kind:    CheckEnvironment,
		Status:  StatusOK,
		Subject: name,
		Summary: "environment variable is set",
		Detail:  detail,
	}
}
