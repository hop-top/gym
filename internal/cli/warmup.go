package cli

import (
	"context"
	"errors"
	"fmt"
	"os"

	"hop.top/gym/internal/events"
	"hop.top/gym/internal/state"
	"hop.top/gym/internal/warmup"
)

// runWarmup renders the skills an agent in the command's folder sees: the
// global scope and, unless noInherit, every scope on the folder chain, the
// nearest holder of a name winning. noInherit keeps only the nearest scope.
func (a *App) runWarmup(format, scope, output string, noInherit bool) error {
	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}

	layers, err := a.skillLayers(rt, scope == "all" || scope == "global", scope == "all" || scope == "project", !noInherit)
	if err != nil {
		return err
	}
	// Winners only, listed general to specific: global first, then the
	// outermost folder down to the innermost.
	var allSkills []warmup.SkillInfo
	won := map[string]listEntry{}
	for _, e := range resolveEffective(layers) {
		if e.ShadowedBy == "" {
			won[e.Scope+"\x00"+e.Folder+"\x00"+e.Name] = e
		}
	}
	for i := len(layers) - 1; i >= 0; i-- {
		l := layers[i]
		var kept []state.InstalledSkill
		for _, r := range l.Skills {
			if _, ok := won[l.Scope+"\x00"+l.Folder+"\x00"+r.Name]; ok {
				kept = append(kept, r)
			}
		}
		infos := warmup.BuildSkillInfo(kept, l.Scope)
		for k := range infos {
			infos[k].Folder = l.Folder
			infos[k].Depth = l.Depth
		}
		if l.Scope == "global" {
			allSkills = append(infos, allSkills...)
		} else {
			allSkills = append(allSkills, infos...)
		}
	}

	if len(allSkills) == 0 {
		return errors.New("no skills installed")
	}

	var content string
	switch format {
	case "xml":
		content = warmup.GenerateXML(allSkills)
	case "json":
		content, err = warmup.GenerateJSON(allSkills)
		if err != nil {
			return fmt.Errorf("generate JSON: %w", err)
		}
	case "agents-md":
		content = warmup.GenerateAgentsMD(allSkills)
	default:
		return fmt.Errorf("unsupported format: %s (use xml, json, or agents-md)", format)
	}

	// Write to file or stdout
	if output != "" {
		if err := os.WriteFile(output, []byte(content), 0644); err != nil {
			return fmt.Errorf("write output file: %w", err)
		}
		a.printlnf("wrote %s to %s", format, output)
	} else {
		fmt.Fprint(a.Stdout, content)
	}

	// Publish gym.warmup.metadata.emitted so subscribers
	// (tip's index, judge cache, etc.) can pick up the metadata.
	// Best-effort — never fail the command on a publish error.
	a.publishWarmupMetadata(format, scope, allSkills)

	return nil
}

// publishWarmupMetadata mirrors the rendered skill list onto the bus
// under TopicWarmupMetadataEmitted. Best-effort: a missing bus or
// publish error never fails the warmup command.
func (a *App) publishWarmupMetadata(format, scope string, skills []warmup.SkillInfo) {
	if a == nil || a.bus == nil {
		return
	}
	out := make([]events.WarmupSkill, 0, len(skills))
	for _, s := range skills {
		out = append(out, events.WarmupSkill{
			Name:    s.Name,
			Version: s.Version,
			Scope:   s.Scope,
			Path:    s.InstallPath,
			Summary: s.Description,
		})
	}
	_ = a.bus.Publish(context.Background(), events.TopicWarmupMetadataEmitted,
		events.WarmupMetadataEmittedPayload{
			Format: format,
			Scope:  scope,
			Skills: out,
		})
}
