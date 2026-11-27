package cli

import (
	"context"
	"errors"

	"hop.top/gym/internal/install"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/upgrade"
)

// trustedPlan drops the plan items whose state record install_path fails
// install.CheckRecordPath. A project's installed.json is repository
// content, so its paths are never trusted blindly: an untrusted record is
// reported (error install-path-untrusted: <path>, one rejected event) and
// left alone while the other records proceed. The returned error joins the
// refusals and is returned by train once the rest has run.
func (a *App) trustedPlan(ctx context.Context, plan []upgrade.PlanItem, rt runtime, projectRoot string) ([]upgrade.PlanItem, error) {
	kept := make([]upgrade.PlanItem, 0, len(plan))
	var refused []error
	for _, item := range plan {
		parents := install.TrustedParents(item.Scope, rt.Config)
		if err := install.CheckRecordPath(item.Skill, item.Scope, projectRoot, parents); err != nil {
			emitFail(ctx, "upgrade", item.Skill.Name)
			a.publishInstallRejected(ctx, item.Skill.Name, item.Scope, newRequestID(), err)
			refused = append(refused, err)
			continue
		}
		kept = append(kept, item)
	}
	return kept, errors.Join(refused...)
}

// trustedRecords drops state records whose install_path fails
// install.CheckRecordPath before anything reads from the recorded
// directory (warmup, cheat). Each skipped record warns once and is never
// read.
func (a *App) trustedRecords(rt runtime, recs []state.InstalledSkill, scope string) []state.InstalledSkill {
	parents := install.TrustedParents(scope, rt.Config)
	out := make([]state.InstalledSkill, 0, len(recs))
	for _, r := range recs {
		if err := install.CheckRecordPath(r, scope, rt.ProjectRoot, parents); err != nil {
			a.Logger().Warnf("skipping installed skill %q: %v", r.Name, err)
			continue
		}
		out = append(out, r)
	}
	return out
}
