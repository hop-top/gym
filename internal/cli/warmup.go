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

func (a *App) runWarmup(format, scope, output string) error {
	rt, err := a.loadRuntime()
	if err != nil {
		return err
	}

	var allSkills []warmup.SkillInfo

	// Load global skills
	if scope == "all" || scope == "global" {
		globalSkills, err := state.LoadGlobal(rt.Dirs.StateHome)
		if err != nil {
			return fmt.Errorf("load global skills: %w", err)
		}
		allSkills = append(allSkills, warmup.BuildSkillInfo(globalSkills, "global")...)
	}

	// Load project skills
	if scope == "all" || scope == "project" {
		if rt.InProject {
			projectSkills, err := state.LoadProject(rt.ProjectRoot)
			if err != nil {
				return fmt.Errorf("load project skills: %w", err)
			}
			allSkills = append(allSkills, warmup.BuildSkillInfo(projectSkills, "project")...)
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
