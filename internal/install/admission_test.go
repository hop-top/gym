package install_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"hop.top/gym/internal/approvals"
	"hop.top/gym/internal/config"
	"hop.top/gym/internal/install"
	"hop.top/gym/internal/registry"
)

// isolateEnv keeps HOME and every XDG dir inside the test.
func isolateEnv(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	return home
}

type skillOpts struct {
	name     string
	tools    string // SKILL.md allowed-tools value ("" omits the field)
	manifest string // gym.json body ("" omits the file)
}

func writeSkill(t *testing.T, o skillOpts) string {
	t.Helper()
	if o.name == "" {
		o.name = "lint"
	}
	dir := filepath.Join(t.TempDir(), o.name)
	fm := "---\nname: " + o.name + "\ndescription: d\n"
	if o.tools != "" {
		fm += "allowed-tools: " + o.tools + "\n"
	}
	files := map[string]string{"SKILL.md": fm + "---\nbody\n"}
	if o.manifest != "" {
		files["gym.json"] = o.manifest
	}
	writeTree(t, dir, files)
	return dir
}

func manifestJSON(class string, tools ...string) string {
	q := make([]string, len(tools))
	for i, tk := range tools {
		q[i] = `"` + tk + `"`
	}
	return `{"schema":"gym.skill/v1","allowed_tools":[` + strings.Join(q, ",") + `],"data_class":"` + class + `"}`
}

func admitCfg() config.AdmissionConfig {
	return config.AdmissionConfig{MaxDataClass: "internal", RequireManifest: "warn"}
}

func admit(t *testing.T, dir string, mod func(*install.AdmissionInput)) (install.AdmissionResult, error) {
	t.Helper()
	in := install.AdmissionInput{
		SkillPath:     dir,
		Entry:         registry.SkillEntry{Name: filepath.Base(dir), SourceURL: dir},
		Admission:     admitCfg(),
		ApprovalsPath: filepath.Join(t.TempDir(), "approvals.json"),
	}
	if mod != nil {
		mod(&in)
	}
	return install.Admit(in)
}

// Case 1 (facts half): valid manifest, matching frontmatter.
func TestAdmit_ValidManifestMatchingFrontmatter(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{tools: "Read Bash(git:*)", manifest: manifestJSON("internal", "Read", "Bash(git:*)")})
	res, err := admit(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !res.ManifestPresent || len(res.UndeclaredTools) != 0 || res.DataClassOver || res.DataClass != "internal" {
		t.Fatalf("facts = %+v", res)
	}
	if res.DigestStatus != install.DigestUnpinned || res.ApprovalState != "unreviewed" || res.ManifestMode != "warn" {
		t.Fatalf("facts = %+v", res)
	}
	if want, _ := install.Digest(dir); res.Digest != want {
		t.Fatalf("digest = %s; want %s", res.Digest, want)
	}
}

// Case 14 (facts half): frontmatter token absent from manifest.
func TestAdmit_UndeclaredFrontmatterTool(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{tools: "Read Write", manifest: manifestJSON("internal", "Read")})
	res, err := admit(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.UndeclaredTools) != 1 || res.UndeclaredTools[0] != "Write" {
		t.Fatalf("undeclared = %v", res.UndeclaredTools)
	}
}

// Case 17: frontmatter allowed-tools as YAML list is manifest-invalid.
func TestAdmit_FrontmatterListIsManifestInvalid(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{manifest: manifestJSON("internal", "Read")})
	skill := "---\nname: lint\ndescription: d\nallowed-tools: [Read]\n---\nbody\n"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(skill), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := admit(t, dir, nil)
	wantReason(t, err, install.ReasonManifestInvalid)
}

// Case 18 (facts half) and 19: class over ceiling; default ceiling.
func TestAdmit_DataClassOverCeiling(t *testing.T) {
	isolateEnv(t)
	for class, want := range map[string]bool{
		"public": false, "internal": false, "confidential": true, "personal": true, "special_category": true,
	} {
		dir := writeSkill(t, skillOpts{manifest: manifestJSON(class)})
		res, err := admit(t, dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		if res.DataClassOver != want {
			t.Errorf("class %s over internal = %v; want %v", class, res.DataClassOver, want)
		}
	}
	dir := writeSkill(t, skillOpts{manifest: manifestJSON("personal")})
	res, err := admit(t, dir, func(in *install.AdmissionInput) { in.Admission.MaxDataClass = "personal" })
	if err != nil || res.DataClassOver {
		t.Fatalf("personal under personal ceiling: over=%v err=%v", res.DataClassOver, err)
	}
	// Ceiling unset: default internal.
	res, err = admit(t, writeSkill(t, skillOpts{manifest: manifestJSON("confidential")}),
		func(in *install.AdmissionInput) { in.Admission.MaxDataClass = "" })
	if err != nil || res.MaxDataClass != "internal" || !res.DataClassOver {
		t.Fatalf("unset ceiling: %+v %v", res, err)
	}
}

func TestAdmit_InvalidCeilingIsError(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{manifest: manifestJSON("public")})
	if _, err := admit(t, dir, func(in *install.AdmissionInput) { in.Admission.MaxDataClass = "secret" }); err == nil {
		t.Fatal("invalid ceiling accepted")
	}
}

func TestAdmit_DataClassEscalated(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{manifest: manifestJSON("internal")})
	for prev, want := range map[string]bool{"public": true, "internal": false, "confidential": false, "": false} {
		res, err := admit(t, dir, func(in *install.AdmissionInput) { in.PreviousDataClass = prev })
		if err != nil {
			t.Fatal(err)
		}
		if res.DataClassEscalated != want {
			t.Errorf("previous %q escalated = %v; want %v", prev, res.DataClassEscalated, want)
		}
	}
}

// Case 10: pin matches / differs / absent.
func TestAdmit_DigestPin(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{manifest: manifestJSON("internal")})
	d, _ := install.Digest(dir)

	res, err := admit(t, dir, func(in *install.AdmissionInput) { in.Entry.Digest = d })
	if err != nil || res.DigestStatus != install.DigestMatch || res.DigestSource != "registry" {
		t.Fatalf("match: %+v %v", res, err)
	}
	_, err = admit(t, dir, func(in *install.AdmissionInput) {
		in.Entry.Digest = "sha256:" + strings.Repeat("0", 64)
	})
	ae := wantReason(t, err, install.ReasonDigestMismatch)
	if !strings.Contains(ae.Detail, d) {
		t.Errorf("mismatch detail %q lacks computed digest", ae.Detail)
	}
	res, err = admit(t, dir, nil)
	if err != nil || res.DigestStatus != install.DigestUnpinned || res.DigestSource != "" {
		t.Fatalf("unpinned: %+v %v", res, err)
	}
}

func TestAdmit_ExpectDigestOverridesRegistry(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{manifest: manifestJSON("internal")})
	d, _ := install.Digest(dir)
	res, err := admit(t, dir, func(in *install.AdmissionInput) {
		in.Entry.Digest = "sha256:" + strings.Repeat("0", 64)
		in.ExpectDigest = d
	})
	if err != nil || res.DigestStatus != install.DigestMatch {
		t.Fatalf("%+v %v", res, err)
	}
	_, err = admit(t, dir, func(in *install.AdmissionInput) { in.ExpectDigest = "sha256:" + strings.Repeat("1", 64) })
	wantReason(t, err, install.ReasonDigestMismatch)
}

// Case 22: approval none / matching digest / other digest / revoked.
func TestAdmit_ApprovalStates(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	d, _ := install.Digest(dir)
	path := filepath.Join(t.TempDir(), "approvals.json")
	with := func(in *install.AdmissionInput) { in.ApprovalsPath = path }

	res, err := admit(t, dir, with)
	if err != nil || res.ApprovalState != "unreviewed" {
		t.Fatalf("none: %+v %v", res, err)
	}
	st, _ := approvals.Open(path)
	if err := st.Approve("lint", "", d, "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	if res, err = admit(t, dir, with); err != nil || res.ApprovalState != "approved" {
		t.Fatalf("matching: %+v %v", res, err)
	}
	other := "sha256:" + strings.Repeat("9", 64)
	if err := st.Approve("lint", "", other, "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "extra.txt"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res, err = admit(t, dir, with); err != nil || res.ApprovalState != "unreviewed" {
		t.Fatalf("content changed -> unreviewed: %+v %v", res, err)
	}
	if err := st.Revoke("lint", "", "me", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, err = admit(t, dir, with)
	wantReason(t, err, install.ReasonApprovalRevoked)
}

// Case 23 (facts half): require_approval turns unreviewed into the
// required-missing fact; an approved skill is unaffected.
func TestAdmit_RequireApproval(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	req := func(in *install.AdmissionInput) { in.Admission.RequireApproval = true }
	res, err := admit(t, dir, req)
	if err != nil || res.ApprovalState != install.ApprovalRequiredMissing {
		t.Fatalf("%+v %v", res, err)
	}
	res, err = admit(t, dir, func(in *install.AdmissionInput) { req(in); in.ApprovalState = "approved" })
	if err != nil || res.ApprovalState != "approved" {
		t.Fatalf("%+v %v", res, err)
	}
}

// Case 24: approval files inside the project tree or the skill tree
// are never consulted.
func TestAdmit_ProjectAndSkillTreeApprovalsIgnored(t *testing.T) {
	isolateEnv(t)
	project := t.TempDir()
	dir := filepath.Join(project, ".gym", "skills", "lint")
	writeTree(t, dir, map[string]string{"SKILL.md": "---\nname: lint\ndescription: d\n---\nb\n"})
	d, _ := install.Digest(dir)
	rec := `{"approvals":[{"skill":"lint","digest":"` + d + `","state":"approved","by":"x","at":"2026-01-01T00:00:00Z"}]}`
	for _, p := range []string{
		filepath.Join(project, ".gym", "approvals.json"),
		filepath.Join(project, "approvals.json"),
		filepath.Join(dir, "approvals.json"),
	} {
		writeTree(t, filepath.Dir(p), map[string]string{filepath.Base(p): rec})
	}
	// default (user-level) path: nothing there
	res, err := install.Admit(install.AdmissionInput{
		SkillPath: dir,
		Entry:     registry.SkillEntry{Name: "lint"},
		Admission: admitCfg(),
	})
	if err != nil || res.ApprovalState != "unreviewed" {
		t.Fatalf("project/skill-tree approvals honored: %+v %v", res, err)
	}
}

// Case 26: digest and approval still checked without a manifest.
func TestAdmit_NoManifestStillChecksDigestAndApproval(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	res, err := admit(t, dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.ManifestPresent || res.DataClass != "" || res.DataClassOver || len(res.UndeclaredTools) != 0 {
		t.Fatalf("no-manifest facts = %+v", res)
	}
	if res.Digest == "" || res.DigestStatus != install.DigestUnpinned || res.ApprovalState != "unreviewed" {
		t.Fatalf("digest/approval facts = %+v", res)
	}
	_, err = admit(t, dir, func(in *install.AdmissionInput) { in.ExpectDigest = "sha256:" + strings.Repeat("2", 64) })
	wantReason(t, err, install.ReasonDigestMismatch)
	_, err = admit(t, dir, func(in *install.AdmissionInput) { in.ApprovalState = "revoked" })
	wantReason(t, err, install.ReasonApprovalRevoked)
}

func TestAdmit_InvalidManifestIsHardError(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{manifest: `{"schema":"gym.skill/v1"}`})
	_, err := admit(t, dir, nil)
	wantReason(t, err, install.ReasonManifestInvalid)
}

func TestAdmit_SymlinkIsHardError(t *testing.T) {
	isolateEnv(t)
	dir := writeSkill(t, skillOpts{})
	if err := os.Symlink("/etc/hosts", filepath.Join(dir, "l")); err != nil {
		t.Fatal(err)
	}
	_, err := admit(t, dir, nil)
	wantReason(t, err, install.ReasonDigestUnsupportedEntry)
}

// --- InstallSkill pipeline ---

func installReq(t *testing.T, src, dest string) install.Request {
	t.Helper()
	return install.Request{
		Entry:    registry.SkillEntry{Name: filepath.Base(src), SourceURL: src, Version: "1.0.0"},
		Source:   src,
		Scope:    "global",
		PathHint: dest,
		Config: config.Config{
			Validator: config.ValidatorConfig{Command: []string{"sh", "-c", "true"}, TimeoutSeconds: 5},
			Admission: admitCfg(),
		},
		ApprovalsPath: filepath.Join(t.TempDir(), "approvals.json"),
		CWD:           t.TempDir(),
	}
}

func TestInstallSkill_GateReceivesAdmissionFactsAndStateRecordsDigest(t *testing.T) {
	isolateEnv(t)
	src := writeSkill(t, skillOpts{tools: "Read", manifest: manifestJSON("confidential", "Read")})
	dest := t.TempDir()
	req := installReq(t, src, dest)
	var got install.AdmissionResult
	req.Gate = func(_ context.Context, _ registry.SkillEntry, _ string, _ []string, _ install.ScanResult, adm install.AdmissionResult) error {
		got = adm
		return nil
	}
	item, err := install.InstallSkill(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := install.Digest(src)
	if got.Digest != want || !got.DataClassOver || got.DataClass != "confidential" {
		t.Fatalf("gate facts = %+v", got)
	}
	if item.Digest != want || item.DataClass != "confidential" {
		t.Fatalf("state = %+v", item)
	}
	if after, _ := install.Digest(filepath.Join(dest, "lint")); after != want {
		t.Fatalf("installed digest %s != %s", after, want)
	}
}

// Case 10 (install half): mismatch is a hard fail before gate and copy.
func TestInstallSkill_DigestMismatchFailsBeforeGateAndCopy(t *testing.T) {
	isolateEnv(t)
	src := writeSkill(t, skillOpts{})
	dest := t.TempDir()
	req := installReq(t, src, dest)
	req.Entry.Digest = "sha256:" + strings.Repeat("0", 64)
	gateCalled := false
	req.Gate = func(context.Context, registry.SkillEntry, string, []string, install.ScanResult, install.AdmissionResult) error {
		gateCalled = true
		return nil
	}
	_, err := install.InstallSkill(context.Background(), req)
	wantReason(t, err, install.ReasonDigestMismatch)
	if gateCalled {
		t.Error("gate ran after a hard admission failure")
	}
	if _, statErr := os.Stat(filepath.Join(dest, "lint")); !os.IsNotExist(statErr) {
		t.Error("destination written after hard admission failure")
	}
}

// Case 12: local-dir source mutated between hash and copy: the
// destination re-hash fails and the destination is removed.
func TestInstallSkill_SourceMutatedAfterAdmissionRejected(t *testing.T) {
	isolateEnv(t)
	src := writeSkill(t, skillOpts{})
	dest := t.TempDir()
	req := installReq(t, src, dest)
	req.Gate = func(context.Context, registry.SkillEntry, string, []string, install.ScanResult, install.AdmissionResult) error {
		return os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: lint\ndescription: swapped\n---\nevil\n"), 0o644)
	}
	_, err := install.InstallSkill(context.Background(), req)
	wantReason(t, err, install.ReasonDigestMismatch)
	if _, statErr := os.Stat(filepath.Join(dest, "lint")); !os.IsNotExist(statErr) {
		t.Fatal("destination left in place after re-hash mismatch")
	}
}

func TestInstallSkillMulti_AdmissionRunsWithoutGate(t *testing.T) {
	isolateEnv(t)
	src := writeSkill(t, skillOpts{manifest: `{"schema":"nope"}`})
	_, err := install.InstallSkillMulti(context.Background(), installReq(t, src, t.TempDir()))
	wantReason(t, err, install.ReasonManifestInvalid)
}

// Existing fixtures carry no gym.json: they must still install under
// the default require_manifest = warn.
func TestInstallSkill_NoManifestInstallsUnderDefaults(t *testing.T) {
	isolateEnv(t)
	src := writeSkill(t, skillOpts{})
	dest := t.TempDir()
	req := installReq(t, src, dest)
	req.Config.Admission = config.AdmissionConfig{} // zero value == defaults
	if _, err := install.InstallSkill(context.Background(), req); err != nil {
		t.Fatalf("no-manifest skill rejected under defaults: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "lint", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}
