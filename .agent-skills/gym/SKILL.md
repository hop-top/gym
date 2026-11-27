---
name: gym
description: Use the gym CLI to discover, install, and manage skills (Agent Skills Spec) with curated registries, validation gates, and progressive disclosure. Use when agents need to install skills, update registries, or generate skill manifests.
---

# Gym - Universal Skill Package Manager

Use this skill when working with the gym CLI for skill management operations.

## Core Commands

### Registry Management (Coaches)

```bash
# Add a coach (registry source)
gym coach hire owner/repo
gym coach hire https://example.com/skills.json
gym coach hire --global owner/repo  # Add to user config

# List configured coaches
gym coach list

# Remove a coach
gym coach dismiss <name-or-url>
gym coach dismiss --global <name-or-url>

# Update local registry index
gym update
```

### Installing Skills (Learn)

```bash
# Install by name from registry
gym learn <skill-name>

# Install from URL or local path
gym learn https://github.com/owner/skill.git
gym learn /path/to/local/skill

# Install globally (default is project-local)
gym learn --global <skill-name>
gym learn -g <skill-name>

# Filter install targets by agent
gym learn --agent claude --agent "!codex" <skill-name>

# Install to specific path
gym learn --path ~/.cursor/skills <skill-name>

# Compatibility alias
gym install <skill-name>
```

Default behavior:
- Installs to `CWD/.gym/skills` (project-local)
- Runs validator (`skills-ref validate {path}`)
- If multiple paths match scope, installs to all
- Installation fails if validation fails

### Upgrading Skills (Train)

```bash
# Upgrade all skills
gym train

# Upgrade specific skills
gym train skill1 skill2

# Upgrade by pattern
gym train "auth-*"

# Disable global mirror when upgrading project skills
gym train --no-global

# Compatibility alias
gym upgrade
```

Train mirror policy:
- Inside project: upgrades project skills + matching global skills
- Outside project: upgrades global skills only
- Use `--no-global` to skip global mirror

### Progressive Disclosure (Warmup)

Generate lightweight skill metadata for context-efficient agent startup:

```bash
# Generate XML for Claude Code
gym warmup --format xml -o ~/.claude/skills-manifest.xml

# Generate AGENTS.md for multi-agent compatibility
gym warmup --format agents-md -o .agents/AGENTS.md

# Generate JSON
gym warmup --format json > skills.json

# Filter by scope
gym warmup --scope global    # Only global skills
gym warmup --scope project   # Only project skills
gym warmup --scope all       # Both (default)
```

**What is progressive disclosure?**

Following the [agentskills.io specification](https://agentskills.io/specification#progressive-disclosure):
1. Agents load only metadata (~100 tokens for 50 skills) at startup
2. Fetch full content (~5000 tokens per skill) on-demand when needed

This keeps context clean and reduces startup latency.

### Path Management

```bash
# List configured and detected install paths
gym paths

# Add global install path
gym paths add --scope global --path ~/.claude/skills

# Add project install path
gym paths add --scope project --path .claude/skills
```

Detected IDE/TDE paths:
- `~/.claude/skills`
- `~/.cursor/skills`
- `~/.gemini/skills`
- `~/.codex/skills`
- `~/.windsurf/skills`

### Serving a Registry

```bash
# Start registry server
gym serve --host 127.0.0.1 --port 4873

# Daemon mode
gym serve --daemon --pidfile /tmp/gym.pid
gym serve --status --pidfile /tmp/gym.pid
gym serve --stop --pidfile /tmp/gym.pid

# Auto-refresh registry
gym serve --refresh 10m
```

Endpoints:
- `GET /skills.json` - Registry manifest
- `GET /_index.json` - Full merged index (debug)
- `GET /health` - Health check

## Configuration

### User Config
`$XDG_CONFIG_HOME/gym/config.json` (or `~/.config/gym/config.json`)

### Project Config
`<project-root>/.gym/config.json`

### CLI Config (Viper)
- `--config <file>`
- `.gym/cli.yaml` in current directory
- `$XDG_CONFIG_HOME/gym/cli.yaml`

Environment overrides:
- `GYM_LEARN_GLOBAL=true`
- `GYM_TRAIN_NO_GLOBAL=true`

### Example Config

```json
{
  "repositories": [
    {
      "name": "official-curated",
      "url": "https://registry.example.com/skills.json",
      "curated": true
    }
  ],
  "install_paths": [
    {
      "name": "claude-global",
      "path": "~/.claude/skills",
      "scope": "global"
    }
  ],
  "curation": {
    "min_stars": 100
  },
  "validator": {
    "command": ["skills-ref", "validate", "{path}"],
    "timeout_seconds": 20
  },
  "upgrade": {
    "mirror_global_from_project": true
  }
}
```

## State Files

- Global state: `$XDG_STATE_HOME/gym/installed-global.json`
- Project state: `<project-root>/.gym/installed.json`
- Registry index: `$XDG_DATA_HOME/gym/registry-index.json`

## Common Workflows

### JIT Skill Installation

```bash
# 1. Preflight
gym version
gym update

# 2. Install skill
gym learn <skill-name>

# 3. Update skill manifest for progressive disclosure
gym warmup --format xml -o ~/.claude/skills-manifest.xml
```

### Autonomous Skill Discovery

```bash
# 1. Ensure curated coach exists
gym coach list
gym coach hire --curated owner/repo
gym update

# 2. Install from curated sources
gym learn <skill-name>

# 3. Report: skill name, source, curated status, validation result

# 4. Update manifest
gym warmup
```

### Multi-Agent Setup

```bash
# Install skills globally
gym learn --global skill1 skill2 skill3

# Generate manifests for each agent
gym warmup --format xml -o ~/.claude/skills-manifest.xml
gym warmup --format agents-md -o ~/.cursor/AGENTS.md
gym warmup --format json -o ~/.gemini/skills.json
```

## Interactive Modes

```bash
# Terminal UI
gym tui

# REPL
gym repl
```

## Notes

- Validator gate is enabled by default (`skills-ref validate {path}`)
- Skills must follow agentskills.io specification
- Curated skills require `min_stars` threshold (default: 100)
- Install paths can be filtered by agent patterns
- Progressive disclosure reduces context overhead from ~250k to ~100 tokens
