package docmode

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// openInEditor launches the user's preferred editor on the file.
// Preference order: $PLANMAN_EDITOR, $VISUAL, $EDITOR, then the platform
// opener. Terminal editors (vim, nano) won't work when spawned from the
// server — set a GUI editor (e.g. "code", "subl") for the editor button.
func openInEditor(path string) error {
	for _, env := range []string{"PLANMAN_EDITOR", "VISUAL", "EDITOR"} {
		if cmd := strings.TrimSpace(os.Getenv(env)); cmd != "" {
			parts := strings.Fields(cmd)
			parts = append(parts, path)
			return startDetached(exec.Command(parts[0], parts[1:]...))
		}
	}
	switch runtime.GOOS {
	case "darwin":
		return startDetached(exec.Command("open", path))
	case "windows":
		return startDetached(exec.Command("cmd", "/c", "start", "", path))
	default:
		return startDetached(exec.Command("xdg-open", path))
	}
}

func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch editor: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
