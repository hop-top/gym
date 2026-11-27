package install

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"hop.top/gym/internal/state"
)

func TestCheckRecordPath(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "proj")
	skills := filepath.Join(root, ".gym", "skills")
	outside := filepath.Join(base, "outside")
	for _, d := range []string{filepath.Join(skills, "good"), outside, filepath.Join(outside, "evil")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(skills, "plainfile"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if os.Symlink(outside, filepath.Join(skills, "link")) != nil {
		t.Skip("symlinks unavailable")
	}
	linkedParent := filepath.Join(root, "linkparent")
	if err := os.Symlink(outside, linkedParent); err != nil {
		t.Fatal(err)
	}

	rec := func(name, path string) state.InstalledSkill {
		return state.InstalledSkill{Name: name, InstallPath: path}
	}
	cases := []struct {
		name  string
		rec   state.InstalledSkill
		scope string
		root  string
		trust []string
		ok    bool
	}{
		{"existing dir", rec("good", filepath.Join(skills, "good")), "project", root, nil, true},
		{"absent", rec("fresh", filepath.Join(skills, "fresh")), "project", root, nil, true},
		{"outside project", rec("evil", filepath.Join(outside, "evil")), "project", root, nil, false},
		{"outside but operator-trusted parent", rec("evil", filepath.Join(outside, "evil")), "project", root, []string{outside}, true},
		{"wrong base", rec("other", filepath.Join(skills, "good")), "project", root, nil, false},
		{"not clean", rec("good", skills+"/../skills/good"), "project", root, nil, false},
		{"relative", rec("good", ".gym/skills/good"), "project", root, nil, false},
		{"dotdot name", rec("..", filepath.Join(skills, "..")), "project", root, nil, false},
		{"file not dir", rec("plainfile", filepath.Join(skills, "plainfile")), "project", root, nil, false},
		{"symlink record", rec("link", filepath.Join(skills, "link")), "project", root, nil, false},
		{"parent through symlink", rec("evil", filepath.Join(linkedParent, "evil")), "project", root, nil, false},
		{"global not in trusted", rec("good", filepath.Join(skills, "good")), "global", root, nil, false},
		{"global trusted", rec("good", filepath.Join(skills, "good")), "global", root, []string{skills}, true},
		{"project scope without root", rec("good", filepath.Join(skills, "good")), "project", "", nil, false},
	}
	for _, c := range cases {
		err := CheckRecordPath(c.rec, c.scope, c.root, c.trust)
		if c.ok && err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
		if !c.ok {
			var re *RefusedError
			if !errors.As(err, &re) || re.Reason != ReasonInstallPathUntrusted {
				t.Errorf("%s: err = %v, want install-path-untrusted", c.name, err)
			} else if err.Error() != "install-path-untrusted: "+c.rec.InstallPath {
				t.Errorf("%s: message %q", c.name, err)
			}
		}
	}
}

func TestValidSkillName(t *testing.T) {
	for n, want := range map[string]bool{"a": true, "a.b": true, "": false, ".": false, "..": false, "a/b": false, `a\b`: false, "../x": false} {
		if ValidSkillName(n) != want {
			t.Errorf("ValidSkillName(%q) != %v", n, want)
		}
	}
}
