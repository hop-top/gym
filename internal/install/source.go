package install

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"hop.top/kit/go/console/progress"
)

func looksLikeURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "git@") || strings.HasPrefix(s, "ssh://")
}

func stageSource(ctx context.Context, source string) (string, func(), error) {
	if strings.HasPrefix(source, "file://") {
		source = strings.TrimPrefix(source, "file://")
	}
	if st, err := os.Stat(source); err == nil && st.IsDir() {
		return source, func() {}, nil
	}
	if !looksLikeURL(source) {
		return "", nil, fmt.Errorf("unsupported source %q: expected URL or existing directory", source)
	}
	tmp, err := os.MkdirTemp("", "skills-install-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }

	rep := progress.FromContext(ctx)
	rep.Emit(ctx, progress.Event{
		Phase: "clone",
		Item:  RedactURL(source),
		At:    time.Now().UTC(),
	})
	cmd := exec.CommandContext(ctx, "git", "clone", "--depth", "1", source, tmp)
	if out, err := cmd.CombinedOutput(); err != nil {
		ok := false
		rep.Emit(ctx, progress.Event{
			Phase: "clone",
			Item:  RedactURL(source),
			OK:    &ok,
			At:    time.Now().UTC(),
		})
		cleanup()
		return "", nil, fmt.Errorf("git clone failed: %s\n%s",
			redactText(err.Error(), source), redactText(strings.TrimSpace(string(out)), source))
	}
	ok := true
	rep.Emit(ctx, progress.Event{
		Phase: "clone",
		Item:  RedactURL(source),
		OK:    &ok,
		At:    time.Now().UTC(),
	})
	return tmp, cleanup, nil
}
